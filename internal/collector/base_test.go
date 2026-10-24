// -------------------------------------------------------------------------------
// Collector Base Tests
//
// Author: Alex Freidah
//
// Covers what every collector inherits from its base: the stream labels and
// cursor start derived from its identity, poll-outcome metrics, the data
// metric helpers (duration conversion and the no-data sentinel, clearing one
// scope), and the poll loop's schedule and shutdown.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// testBase builds a base for a zone collector named "Test" polling example.com,
// with the given poll interval.
func testBase(interval time.Duration) base {
	common := testCommon(&fakePusher{})
	common.PollInterval = interval
	return newBase(common, identity{
		name:     "Test",
		stream:   "test_stream",
		scopeKey: "zone",
		scope:    "example.com",
		record:   "test record",
	})
}

// -------------------------------------------------------------------------
// IDENTITY
// -------------------------------------------------------------------------

// TestCollectors_IdentityAndCursorStart checks what each collector's
// constructor hands its base - the Loki stream labels (HTTP ships as
// "http_traffic", not its dataset name) - and that every cursor starts one
// backfill window back.
func TestCollectors_IdentityAndCursorStart(t *testing.T) {
	push := &fakePusher{}
	firewall := NewFirewallCollector(zoneTestConfig(&fakeCloudflare{}, push))
	httpc := NewHTTPCollector(zoneTestConfig(&fakeCloudflare{}, push))
	dns := NewDNSCollector(zoneTestConfig(&fakeCloudflare{}, push))
	audit := NewAuditCollector(auditTestConfig(&fakeCloudflare{}, push))
	rum := NewRUMCollector(rumTestConfig(&fakeRUMQuerier{}, push))

	tests := []struct {
		name   string
		labels map[string]string
		cursor time.Time
	}{
		{"firewall", map[string]string{"job": "cloudflare", "type": "firewall", "zone": "example.com"}, firewall.lastSeen},
		{"http", map[string]string{"job": "cloudflare", "type": "http_traffic", "zone": "example.com"}, httpc.lastSeen},
		{"dns", map[string]string{"job": "cloudflare", "type": "dns", "zone": "example.com"}, dns.lastSeen},
		{"audit", map[string]string{"job": "cloudflare", "type": "audit", "account": "acme"}, audit.lastSeen},
		{"rum", map[string]string{"job": "cloudflare", "type": "rum_pageload", "site": "example.com"}, rum.pageloadSeen},
	}
	got := map[string]map[string]string{"firewall": firewall.labels, "http": httpc.labels, "dns": dns.labels, "audit": audit.labels, "rum": rum.labels}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(got[tt.name]) != len(tt.labels) {
				t.Fatalf("labels = %v, want %v", got[tt.name], tt.labels)
			}
			for k, v := range tt.labels {
				if got[tt.name][k] != v {
					t.Errorf("labels[%q] = %q, want %q", k, got[tt.name][k], v)
				}
			}
			if age := time.Since(tt.cursor); age < time.Hour-time.Minute || age > time.Hour+time.Minute {
				t.Errorf("cursor starts %v back, want one backfill window (1h)", age)
			}
		})
	}
}

func TestBase_CursorStartIsOneBackfillWindowAgo(t *testing.T) {
	b := testBase(time.Minute)

	before := time.Now().UTC().Add(-time.Hour)
	got := b.cursorStart()
	after := time.Now().UTC().Add(-time.Hour)

	if got.Before(before) || got.After(after) {
		t.Errorf("cursorStart() = %v, want between %v and %v", got, before, after)
	}
}

// TestCollectors_RunPollsThenStops checks that each collector's Run hands its
// own poll to the shared loop: with the context already cancelled, Run polls
// once and returns cleanly.
func TestCollectors_RunPollsThenStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	push := &fakePusher{}

	zone, audit, rum := &fakeCloudflare{}, &fakeCloudflare{}, &fakeRUMQuerier{}
	runs := map[string]func(context.Context) error{
		"firewall": NewFirewallCollector(zoneTestConfig(zone, push)).Run,
		"http":     NewHTTPCollector(zoneTestConfig(zone, push)).Run,
		"dns":      NewDNSCollector(zoneTestConfig(zone, push)).Run,
		"audit":    NewAuditCollector(auditTestConfig(audit, push)).Run,
		"rum":      NewRUMCollector(rumTestConfig(rum, push)).Run,
	}
	for name, run := range runs {
		if err := run(ctx); err != nil {
			t.Errorf("%s: Run() error = %v, want nil on cancel", name, err)
		}
	}

	if len(zone.firewallCalls) != 1 || len(zone.httpCalls) != 1 || len(zone.dnsCalls) != 1 || len(audit.auditCalls) != 1 || len(rum.pageloadCalls) != 1 {
		t.Errorf("each collector should poll once: firewall=%d http=%d dns=%d audit=%d rum=%d",
			len(zone.firewallCalls), len(zone.httpCalls), len(zone.dnsCalls), len(audit.auditCalls), len(rum.pageloadCalls))
	}
}

// -------------------------------------------------------------------------
// POLL WINDOW
// -------------------------------------------------------------------------

func TestPollWindow_EndsOneLagBeforeNow(t *testing.T) {
	b := testBase(time.Minute)
	b.ingestionLag = 3 * time.Minute
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	until, ok := b.pollWindow(now, now.Add(-time.Hour))
	if !ok {
		t.Fatal("window ending after the cursor should be open")
	}
	if want := now.Add(-3 * time.Minute); !until.Equal(want) {
		t.Errorf("until = %v, want %v", until, want)
	}
}

func TestPollWindow_ClosedUntilLagHasPassed(t *testing.T) {
	b := testBase(time.Minute)
	b.ingestionLag = 3 * time.Minute
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	// The cursor already sits at or past now - lag: nothing new has landed.
	for _, cursor := range []time.Time{now.Add(-3 * time.Minute), now.Add(-time.Minute)} {
		if _, ok := b.pollWindow(now, cursor); ok {
			t.Errorf("cursor %v: window should be closed", cursor)
		}
	}
}

// -------------------------------------------------------------------------
// RECORD POLL
// -------------------------------------------------------------------------

func TestRecordPoll_SuccessAndError(t *testing.T) {
	metrics.PollTotal.Reset()
	metrics.LastPollTimestamp.Reset()
	b := testBase(time.Minute)

	b.recordPoll(DatasetFirewall, time.Now(), nil)
	b.recordPoll(DatasetFirewall, time.Now(), errors.New("boom"))

	assertCounterValue(t, metrics.PollTotal.WithLabelValues("firewall", "example.com", "success"), 1)
	assertCounterValue(t, metrics.PollTotal.WithLabelValues("firewall", "example.com", "error"), 1)
	if testutil.ToFloat64(metrics.LastPollTimestamp.WithLabelValues("firewall", "example.com")) == 0 {
		t.Error("last poll timestamp should be set by the successful poll")
	}
}

func TestRecordPoll_ErrorDoesNotSetLastPoll(t *testing.T) {
	metrics.LastPollTimestamp.Reset()
	b := testBase(time.Minute)

	b.recordPoll(DatasetAudit, time.Now(), errors.New("boom"))

	if got := testutil.CollectAndCount(metrics.LastPollTimestamp); got != 0 {
		t.Errorf("last poll timestamp series = %d, want 0 after only a failed poll", got)
	}
}

// -------------------------------------------------------------------------
// DATA METRICS
// -------------------------------------------------------------------------

// testGauge is an unregistered gauge ending in the scope label, like every
// collector data metric.
func testGauge() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "test_gauge"}, []string{"kind", "zone"})
}

func TestSetDuration_ConvertsUnitsAndSkipsSentinel(t *testing.T) {
	b := testBase(time.Minute)
	gauge := testGauge()

	b.setDuration(gauge, 250, time.Millisecond, "ms")
	b.setDuration(gauge, 1396000, time.Microsecond, "us")
	b.setDuration(gauge, -1, time.Millisecond, "none")

	assertGaugeValue(t, gauge.WithLabelValues("ms", "example.com"), 0.25)
	assertGaugeValue(t, gauge.WithLabelValues("us", "example.com"), 1.396)
	if got := testutil.CollectAndCount(gauge); got != 2 {
		t.Errorf("series = %d, want 2 (the -1 no-data sentinel is skipped, not set to 0)", got)
	}
}

func TestClear_RemovesOnlyThisScopesSeries(t *testing.T) {
	b := testBase(time.Minute)
	gauge := testGauge()
	b.set(gauge, 1, "a")
	gauge.WithLabelValues("a", "other.example").Set(2)

	b.clear(gauge)

	if got := testutil.CollectAndCount(gauge); got != 1 {
		t.Fatalf("series = %d, want 1 (only the other scope's)", got)
	}
	assertGaugeValue(t, gauge.WithLabelValues("a", "other.example"), 2)
}

// -------------------------------------------------------------------------
// POLL LOOP
// -------------------------------------------------------------------------

func TestRun_PollsImmediatelyThenOnTickUntilCancel(t *testing.T) {
	b := testBase(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	polls := make(chan struct{}, 10)

	done := make(chan error, 1)
	go func() {
		done <- b.run(ctx, time.Now(), func(context.Context) { polls <- struct{}{} })
	}()

	// One initial poll plus at least one ticked poll.
	for range 2 {
		select {
		case <-polls:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for a poll")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v, want nil on cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return after cancel")
	}
}
