// -------------------------------------------------------------------------------
// RUM / Web Analytics Collector Tests
//
// Author: Alex Freidah
//
// Covers what is RUM's own: the page-load spec wiring on top of the shared
// cursor-based poll (site, window, page-view/session counters, cursor to the
// window end), and the web-vitals rolling window - gauge conversion
// (microseconds to seconds, CLS as a unitless score) and the -1 "no data"
// sentinel being dropped rather than emitted as a zero.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeRUMQuerier implements rumQuerier with canned per-dataset responses and
// independent error control, recording every call's site tag and window.
type fakeRUMQuerier struct {
	pageloads      []cloudflare.RUMPageloadGroup
	webVitals      []cloudflare.RUMWebVitalsGroup
	pageloadErr    error
	webVitalsErr   error
	pageloadCalls  []queryCall
	webVitalsCalls []queryCall
}

func (f *fakeRUMQuerier) QueryRUMPageloads(_ context.Context, _, siteTag string, since, until time.Time) ([]cloudflare.RUMPageloadGroup, error) {
	f.pageloadCalls = append(f.pageloadCalls, queryCall{siteTag, since, until})
	return f.pageloads, f.pageloadErr
}

func (f *fakeRUMQuerier) QueryRUMWebVitals(_ context.Context, _, siteTag string, since, until time.Time) ([]cloudflare.RUMWebVitalsGroup, error) {
	f.webVitalsCalls = append(f.webVitalsCalls, queryCall{siteTag, since, until})
	return f.webVitals, f.webVitalsErr
}

// -------------------------------------------------------------------------
// PAGE LOADS
// -------------------------------------------------------------------------

func TestRUMPollPageloads_Wiring(t *testing.T) {
	metrics.RUMPageviewsTotal.Reset()
	metrics.RUMSessionsTotal.Reset()
	cf := &fakeRUMQuerier{pageloads: []cloudflare.RUMPageloadGroup{{
		Count:      9,
		Dimensions: cloudflare.RUMPageloadDimensions{RequestPath: "/", CountryName: "VN", DeviceType: "desktop"},
		Sum:        cloudflare.RUMPageloadSum{Visits: 1},
	}}}
	push := &fakePusher{}
	c := NewRUMCollector(rumTestConfig(cf, push))
	since := c.pageloadSeen

	c.pollPageloads(context.Background())

	if len(cf.pageloadCalls) != 1 || cf.pageloadCalls[0].scopeID != "site1" || !cf.pageloadCalls[0].since.Equal(since) {
		t.Fatalf("pageload calls = %+v, want one for site1 from the cursor %v", cf.pageloadCalls, since)
	}
	assertCounterValue(t, metrics.RUMPageviewsTotal.WithLabelValues("VN", "desktop", "example.com"), 9)
	assertCounterValue(t, metrics.RUMSessionsTotal.WithLabelValues("VN", "desktop", "example.com"), 1)
	if !c.pageloadSeen.Equal(cf.pageloadCalls[0].until) {
		t.Errorf("cursor = %v, want the window end %v", c.pageloadSeen, cf.pageloadCalls[0].until)
	}
	if len(push.entries) != 1 {
		t.Errorf("got %d Loki pushes, want 1", len(push.entries))
	}
}

// -------------------------------------------------------------------------
// WEB VITALS
// -------------------------------------------------------------------------

func TestRUMPollWebVitals_ConvertsAndDropsSentinel(t *testing.T) {
	metrics.RUMWebVitalSeconds.Reset()
	metrics.RUMCumulativeLayoutShift.Reset()

	cf := &fakeRUMQuerier{
		webVitals: []cloudflare.RUMWebVitalsGroup{
			{
				Dimensions: cloudflare.RUMWebVitalsDimensions{DeviceType: "desktop"},
				Quantiles: cloudflare.RUMWebVitalsQuantiles{
					LargestContentfulPaintP75: 1396000, // 1.396 s
					InteractionToNextPaintP75: 48000,   // 0.048 s
					FirstInputDelayP75:        -1,      // no-data sentinel -> dropped
					FirstContentfulPaintP75:   1200000, // 1.2 s
					TimeToFirstByteP75:        591500,  // 0.5915 s
					CumulativeLayoutShiftP75:  0.012,   // unitless
				},
			},
		},
	}
	c := NewRUMCollector(rumTestConfig(cf, &fakePusher{}))

	c.pollWebVitals(context.Background())

	// Microseconds are converted to seconds.
	assertGaugeValue(t, metrics.RUMWebVitalSeconds.WithLabelValues("lcp", "desktop", "example.com"), 1.396)
	assertGaugeValue(t, metrics.RUMWebVitalSeconds.WithLabelValues("ttfb", "desktop", "example.com"), 0.5915)

	// CLS is a unitless score on its own metric.
	assertGaugeValue(t, metrics.RUMCumulativeLayoutShift.WithLabelValues("desktop", "example.com"), 0.012)

	// The -1 sentinel (fid) is dropped: only lcp, inp, fcp, ttfb are emitted.
	if got := testutil.CollectAndCount(metrics.RUMWebVitalSeconds); got != 4 {
		t.Errorf("web vital seconds series = %d, want 4 (fid sentinel dropped)", got)
	}
}

func TestRUMPollWebVitals_QueryErrorKeepsGauges(t *testing.T) {
	metrics.RUMWebVitalSeconds.Reset()
	metrics.RUMWebVitalSeconds.WithLabelValues("lcp", "desktop", "example.com").Set(1.0)

	cf := &fakeRUMQuerier{webVitalsErr: errors.New("boom")}
	c := NewRUMCollector(rumTestConfig(cf, &fakePusher{}))

	c.pollWebVitals(context.Background())

	// On query error the gauges are left untouched (last known value retained).
	assertGaugeValue(t, metrics.RUMWebVitalSeconds.WithLabelValues("lcp", "desktop", "example.com"), 1.0)
}

func TestRUMPoll_RunsBothDatasets(t *testing.T) {
	cf := &fakeRUMQuerier{}
	c := NewRUMCollector(rumTestConfig(cf, &fakePusher{}))

	c.poll(context.Background())

	if len(cf.pageloadCalls) != 1 || len(cf.webVitalsCalls) != 1 {
		t.Errorf("one poll should query both datasets; pageloads=%d webvitals=%d",
			len(cf.pageloadCalls), len(cf.webVitalsCalls))
	}
}
