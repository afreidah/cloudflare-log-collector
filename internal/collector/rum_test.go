// -------------------------------------------------------------------------------
// RUM / Web Analytics Collector Tests
//
// Author: Alex Freidah
//
// Drives the RUM collector through an in-memory fake querier. Covers page-load
// counting and Loki shipping with cursor advancement, the no-double-count
// guarantee on a Loki failure, web-vital gauge conversion (microseconds to
// seconds, CLS as a unitless score), and the -1 "no data" sentinel being
// dropped rather than emitted as a zero.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	io_prometheus "github.com/prometheus/client_model/go"
)

// fakeRUMQuerier implements rumQuerier with canned per-dataset responses and
// independent error control, recording how many times each method was called.
type fakeRUMQuerier struct {
	pageloads    []cloudflare.RUMPageloadGroup
	webVitals    []cloudflare.RUMWebVitalsGroup
	pageloadErr  error
	webVitalsErr error

	pageloadCalls  int
	webVitalsCalls int
}

func (f *fakeRUMQuerier) QueryRUMPageloads(_ context.Context, _, _ string, _, _ time.Time) ([]cloudflare.RUMPageloadGroup, error) {
	f.pageloadCalls++
	return f.pageloads, f.pageloadErr
}

func (f *fakeRUMQuerier) QueryRUMWebVitals(_ context.Context, _, _ string, _, _ time.Time) ([]cloudflare.RUMWebVitalsGroup, error) {
	f.webVitalsCalls++
	return f.webVitals, f.webVitalsErr
}

// rumTestCollector builds a RUM collector wired to the given fakes.
func rumTestCollector(cf rumQuerier, push logPusher) *RUMCollector {
	return NewRUMCollector(RUMCollectorConfig{
		CF:             cf,
		Loki:           push,
		AccountID:      "acct1",
		SiteTag:        "site1",
		SiteName:       "example.com",
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      100,
	})
}

// -------------------------------------------------------------------------
// PAGE LOADS
// -------------------------------------------------------------------------

func TestRUMPollPageloads_CountsShipsAndAdvances(t *testing.T) {
	metrics.RUMPageviewsTotal.Reset()
	metrics.RUMSessionsTotal.Reset()

	cf := &fakeRUMQuerier{
		pageloads: []cloudflare.RUMPageloadGroup{
			{
				Count:      9,
				Dimensions: cloudflare.RUMPageloadDimensions{RequestPath: "/", CountryName: "VN", DeviceType: "desktop"},
				Sum:        cloudflare.RUMPageloadSum{Visits: 1},
			},
		},
	}
	push := &fakePusher{}
	c := rumTestCollector(cf, push)

	before := c.pageloadSeen
	c.pollPageloads(context.Background())

	if cf.pageloadCalls != 1 {
		t.Errorf("pageload querier called %d times, want 1", cf.pageloadCalls)
	}
	if len(push.labels) != 1 {
		t.Fatalf("got %d Loki pushes, want 1", len(push.labels))
	}
	if push.labels[0]["type"] != DatasetRUMPageload.String() {
		t.Errorf("stream type = %q, want %q", push.labels[0]["type"], DatasetRUMPageload.String())
	}
	if push.labels[0]["site"] != "example.com" {
		t.Errorf("stream site = %q, want example.com", push.labels[0]["site"])
	}

	assertCounterValue(t, metrics.RUMPageviewsTotal.WithLabelValues("VN", "desktop", "example.com"), 9)
	assertCounterValue(t, metrics.RUMSessionsTotal.WithLabelValues("VN", "desktop", "example.com"), 1)

	if !c.pageloadSeen.After(before) {
		t.Error("pageloadSeen cursor should advance after a successful poll")
	}
}

func TestRUMPollPageloads_LokiErrorDoesNotCountOrAdvance(t *testing.T) {
	metrics.RUMPageviewsTotal.Reset()

	cf := &fakeRUMQuerier{
		pageloads: []cloudflare.RUMPageloadGroup{
			{Count: 5, Dimensions: cloudflare.RUMPageloadDimensions{CountryName: "US", DeviceType: "mobile"}},
		},
	}
	push := &fakePusher{err: errors.New("loki down")}
	c := rumTestCollector(cf, push)

	before := c.pageloadSeen
	c.pollPageloads(context.Background())

	// Shipping failed, so the page view must not be counted and the cursor must
	// not advance (the same window is retried next poll - no double counting).
	assertCounterValue(t, metrics.RUMPageviewsTotal.WithLabelValues("US", "mobile", "example.com"), 0)
	if c.pageloadSeen != before {
		t.Error("pageloadSeen cursor must not advance when the Loki push fails")
	}
}

func TestRUMPollPageloads_QueryErrorSkipsLoki(t *testing.T) {
	cf := &fakeRUMQuerier{pageloadErr: errors.New("boom")}
	push := &fakePusher{}
	c := rumTestCollector(cf, push)

	c.pollPageloads(context.Background())

	if cf.pageloadCalls != 1 {
		t.Errorf("pageload querier called %d times, want 1", cf.pageloadCalls)
	}
	if len(push.labels) != 0 {
		t.Errorf("got %d Loki pushes, want 0 on query error", len(push.labels))
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
	c := rumTestCollector(cf, &fakePusher{})

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
	c := rumTestCollector(cf, &fakePusher{})

	c.pollWebVitals(context.Background())

	// On query error the gauges are left untouched (last known value retained).
	assertGaugeValue(t, metrics.RUMWebVitalSeconds.WithLabelValues("lcp", "desktop", "example.com"), 1.0)
}

// -------------------------------------------------------------------------
// RUN
// -------------------------------------------------------------------------

func TestRUMRun_InitialPollThenCancel(t *testing.T) {
	cf := &fakeRUMQuerier{
		pageloads: []cloudflare.RUMPageloadGroup{
			{Count: 3, Dimensions: cloudflare.RUMPageloadDimensions{CountryName: "US", DeviceType: "desktop"}},
		},
	}
	c := rumTestCollector(cf, &fakePusher{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	time.Sleep(100 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if cf.pageloadCalls < 1 || cf.webVitalsCalls < 1 {
		t.Errorf("initial poll should query both datasets; pageloads=%d webvitals=%d",
			cf.pageloadCalls, cf.webVitalsCalls)
	}
}

// -------------------------------------------------------------------------
// HELPERS
// -------------------------------------------------------------------------

// assertGaugeValue reads the current value of a Prometheus gauge and asserts it
// matches the expected value.
func assertGaugeValue(t *testing.T, gauge prometheus.Gauge, expected float64) {
	t.Helper()

	var m io_prometheus.Metric
	if err := gauge.Write(&m); err != nil {
		t.Fatalf("failed to read gauge: %v", err)
	}
	if m.Gauge.GetValue() != expected {
		t.Errorf("gauge value = %v, want %v", m.Gauge.GetValue(), expected)
	}
}
