// -------------------------------------------------------------------------------
// HTTP Traffic Collector Tests
//
// Author: Alex Freidah
//
// Covers what is the HTTP collector's own on top of the shared cursor-based
// poll: how traffic groups map onto the request, origin, and bytes counters
// (normalized host, cache status, request source, origin vs edge status), that
// the counters accumulate across windows and count a window exactly once when
// a failed push is retried, and its spec wiring. Also the latency polls on top
// of the shared trailing poll: per-host quantiles in seconds for each kind,
// and their independence from the traffic poll.
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

// httpGroup builds a traffic group; fields left zero stay zero.
func httpGroup(count int, bytes int64, d cloudflare.HTTPRequestDimensions) cloudflare.HTTPRequestGroup {
	return cloudflare.HTTPRequestGroup{Count: count, Dimensions: d, Sum: cloudflare.HTTPRequestSum{EdgeResponseBytes: bytes}}
}

// resetHTTPMetrics clears the process-global HTTP counters so a test asserts
// absolute values.
func resetHTTPMetrics() {
	metrics.HTTPRequestsTotal.Reset()
	metrics.HTTPOriginRequestsTotal.Reset()
	metrics.HTTPBytesTotal.Reset()
}

// -------------------------------------------------------------------------
// UPDATE METRICS
// -------------------------------------------------------------------------

func TestUpdateMetrics_MapsDimensionsToLabels(t *testing.T) {
	resetHTTPMetrics()
	c := NewHTTPCollector(zoneTestConfig(&fakeCloudflare{}, &fakePusher{}))

	c.updateMetrics([]cloudflare.HTTPRequestGroup{
		// Same service under two spellings: case and port normalize away.
		httpGroup(10, 1000, cloudflare.HTTPRequestDimensions{
			ClientRequestHTTPHost: "G3.munchbox.cc:8080", ClientRequestHTTPMethodName: "GET", EdgeResponseStatus: 200,
			OriginResponseStatus: 200, CacheStatus: "dynamic", RequestSource: "eyeball", ClientCountryName: "US",
		}),
		httpGroup(5, 500, cloudflare.HTTPRequestDimensions{
			ClientRequestHTTPHost: "g3.munchbox.cc", ClientRequestHTTPMethodName: "GET", EdgeResponseStatus: 200,
			OriginResponseStatus: 200, CacheStatus: "dynamic", RequestSource: "eyeball", ClientCountryName: "US",
		}),
		// Origin never answered: edge 504 with origin status 0.
		httpGroup(3, 30, cloudflare.HTTPRequestDimensions{
			ClientRequestHTTPHost: "s3-orchestrator.munchbox.cc", ClientRequestHTTPMethodName: "GET", EdgeResponseStatus: 504,
			CacheStatus: "miss", RequestSource: "eyeball", ClientCountryName: "DE",
		}),
		// A Worker's own subrequest keeps its source.
		httpGroup(7, 700, cloudflare.HTTPRequestDimensions{
			ClientRequestHTTPHost: "s3.us-east-005.backblazeb2.com", ClientRequestHTTPMethodName: "PUT", EdgeResponseStatus: 200,
			OriginResponseStatus: 200, CacheStatus: "dynamic", RequestSource: "edgeWorkerFetch", ClientCountryName: "US",
		}),
	})

	assertCounterValue(t, metrics.HTTPRequestsTotal.WithLabelValues("GET", "200", "US", "g3.munchbox.cc", "dynamic", "eyeball", "example.com"), 15)
	assertCounterValue(t, metrics.HTTPOriginRequestsTotal.WithLabelValues("s3-orchestrator.munchbox.cc", "0", "504", "eyeball", "example.com"), 3)
	assertCounterValue(t, metrics.HTTPRequestsTotal.WithLabelValues("PUT", "200", "US", "s3.us-east-005.backblazeb2.com", "dynamic", "edgeWorkerFetch", "example.com"), 7)
	assertCounterValue(t, metrics.HTTPBytesTotal.WithLabelValues("edge", "g3.munchbox.cc", "eyeball", "example.com"), 1500)
	assertCounterValue(t, metrics.HTTPBytesTotal.WithLabelValues("edge", "s3.us-east-005.backblazeb2.com", "edgeWorkerFetch", "example.com"), 700)
}

func TestUpdateMetrics_AccumulatesAcrossWindows(t *testing.T) {
	resetHTTPMetrics()
	c := NewHTTPCollector(zoneTestConfig(&fakeCloudflare{}, &fakePusher{}))
	d := cloudflare.HTTPRequestDimensions{
		ClientRequestHTTPHost: "example.com", ClientRequestHTTPMethodName: "GET", EdgeResponseStatus: 200,
		OriginResponseStatus: 200, CacheStatus: "hit", RequestSource: "eyeball", ClientCountryName: "US",
	}

	c.updateMetrics([]cloudflare.HTTPRequestGroup{httpGroup(100, 5000, d)})
	c.updateMetrics([]cloudflare.HTTPRequestGroup{httpGroup(3, 100, d)})

	assertCounterValue(t, metrics.HTTPRequestsTotal.WithLabelValues("GET", "200", "US", "example.com", "hit", "eyeball", "example.com"), 103)
	assertCounterValue(t, metrics.HTTPBytesTotal.WithLabelValues("edge", "example.com", "eyeball", "example.com"), 5100)
}

// -------------------------------------------------------------------------
// POLL
// -------------------------------------------------------------------------

func TestHTTPPoll_Wiring(t *testing.T) {
	cf := &fakeCloudflare{httpGroups: []cloudflare.HTTPRequestGroup{
		httpGroup(1, 10, cloudflare.HTTPRequestDimensions{ClientRequestHTTPHost: "example.com", RequestSource: "eyeball"}),
	}}
	push := &fakePusher{}
	c := NewHTTPCollector(zoneTestConfig(cf, push))
	since := c.lastSeen

	c.poll(context.Background())

	if len(cf.httpCalls) != 1 || cf.httpCalls[0].scopeID != "zone1" || !cf.httpCalls[0].since.Equal(since) {
		t.Fatalf("http calls = %+v, want one for zone1 from the cursor %v", cf.httpCalls, since)
	}
	if !c.lastSeen.Equal(cf.httpCalls[0].until) {
		t.Errorf("cursor = %v, want the window end %v", c.lastSeen, cf.httpCalls[0].until)
	}
	if got := decodeLine[cloudflare.HTTPRequestGroup](t, push.entries[0][0].Line); got.Dimensions.ClientRequestHTTPHost != "example.com" {
		t.Errorf("shipped group = %+v, want the HTTP group", got)
	}
}

// TestHTTPPoll_LokiRecovers_CountsWindowOnce is the regression test for the
// double count: a window whose push fails is re-queried on the next poll, and
// its counts must land exactly once when that retry succeeds.
func TestHTTPPoll_LokiRecovers_CountsWindowOnce(t *testing.T) {
	resetHTTPMetrics()
	d := cloudflare.HTTPRequestDimensions{
		ClientRequestHTTPHost: "example.com", ClientRequestHTTPMethodName: "GET", EdgeResponseStatus: 200,
		CacheStatus: "dynamic", RequestSource: "eyeball", ClientCountryName: "US",
	}
	cf := &fakeCloudflare{httpGroups: []cloudflare.HTTPRequestGroup{httpGroup(10, 5000, d)}}
	push := &fakePusher{err: errors.New("loki down")}
	c := NewHTTPCollector(zoneTestConfig(cf, push))
	requests := metrics.HTTPRequestsTotal.WithLabelValues("GET", "200", "US", "example.com", "dynamic", "eyeball", "example.com")

	// First poll: Loki rejects the push, so nothing is counted.
	c.poll(context.Background())
	assertCounterValue(t, requests, 0)

	// Second poll: Loki is back and the same window comes round again.
	push.err = nil
	c.poll(context.Background())

	assertCounterValue(t, requests, 10)
	assertCounterValue(t, metrics.HTTPBytesTotal.WithLabelValues("edge", "example.com", "eyeball", "example.com"), 5000)
	if len(cf.httpCalls) != 2 {
		t.Errorf("http querier called %d times, want 2", len(cf.httpCalls))
	}
}

// -------------------------------------------------------------------------
// LATENCY
// -------------------------------------------------------------------------

// -------------------------------------------------------------------------
// UNIQUE IPS
// -------------------------------------------------------------------------

func TestHTTPPoll_UniqueIPsForLastCompleteHourAndDay(t *testing.T) {
	metrics.HTTPUniqueIPs.Reset()
	cf := &fakeCloudflare{
		hourlyIPs: []cloudflare.HTTPUniqueIPsGroup{{Uniq: cloudflare.HTTPUniq{Uniques: 23}}},
		dailyIPs:  []cloudflare.HTTPUniqueIPsGroup{{Uniq: cloudflare.HTTPUniq{Uniques: 583}}},
	}
	c := NewHTTPCollector(zoneTestConfig(cf, &fakePusher{}))

	c.poll(context.Background())

	// Each rollup is asked for exactly its last whole period, never the
	// current, partial one.
	for name, tt := range map[string]struct {
		calls  []queryCall
		period time.Duration
	}{"hourly": {cf.hourlyCalls, time.Hour}, "daily": {cf.dailyCalls, 24 * time.Hour}} {
		if len(tt.calls) != 1 {
			t.Fatalf("%s calls = %d, want 1", name, len(tt.calls))
		}
		w := tt.calls[0]
		if w.until.Sub(w.since) != tt.period || !w.until.Equal(w.until.Truncate(tt.period)) || w.until.After(time.Now()) {
			t.Errorf("%s window = [%v, %v), want the last complete %v", name, w.since, w.until, tt.period)
		}
	}
	assertGaugeValue(t, metrics.HTTPUniqueIPs.WithLabelValues("1h", "example.com"), 23)
	assertGaugeValue(t, metrics.HTTPUniqueIPs.WithLabelValues("1d", "example.com"), 583)
}

func TestHTTPPoll_UniqueIPsEmptyWindowIsZero(t *testing.T) {
	metrics.HTTPUniqueIPs.Reset()
	metrics.HTTPUniqueIPs.WithLabelValues("1h", "example.com").Set(9)
	c := NewHTTPCollector(zoneTestConfig(&fakeCloudflare{}, &fakePusher{}))

	c.poll(context.Background())

	// No rows means no clients in the hour, not a missing value.
	assertGaugeValue(t, metrics.HTTPUniqueIPs.WithLabelValues("1h", "example.com"), 0)
}

// resetLatencyMetrics clears the process-global latency gauges so a test
// asserts exact series.
func resetLatencyMetrics() {
	metrics.HTTPOriginLatencySeconds.Reset()
	metrics.HTTPEdgeTTFBSeconds.Reset()
}

func TestHTTPPoll_LatencyQuantilesPerHost(t *testing.T) {
	resetLatencyMetrics()
	cf := &fakeCloudflare{
		originLatency: []cloudflare.HTTPLatencyGroup[cloudflare.OriginLatencyQuantiles]{{
			Dimensions: cloudflare.HTTPLatencyDimensions{ClientRequestHTTPHost: "G3.munchbox.cc:443"},
			Quantiles:  cloudflare.OriginLatencyQuantiles{OriginResponseDurationMsP50: 25, OriginResponseDurationMsP95: 36, OriginResponseDurationMsP99: 380},
		}},
		edgeLatency: []cloudflare.HTTPLatencyGroup[cloudflare.EdgeLatencyQuantiles]{{
			Dimensions: cloudflare.HTTPLatencyDimensions{ClientRequestHTTPHost: "b2-proxy.munchbox.cc"},
			Quantiles:  cloudflare.EdgeLatencyQuantiles{EdgeTimeToFirstByteMsP50: 271, EdgeTimeToFirstByteMsP95: 882, EdgeTimeToFirstByteMsP99: 1769},
		}},
	}
	c := NewHTTPCollector(zoneTestConfig(cf, &fakePusher{}))

	c.poll(context.Background())

	// Each latency query runs once for the zone over the trailing window.
	for name, calls := range map[string][]queryCall{"origin": cf.originCalls, "edge": cf.edgeCalls} {
		if len(calls) != 1 || calls[0].scopeID != "zone1" || calls[0].until.Sub(calls[0].since) != 15*time.Minute {
			t.Errorf("%s latency calls = %+v, want one for zone1 over the 15m trailing window", name, calls)
		}
	}

	// Milliseconds become seconds, each quantile is its own series, and the
	// host is normalized like the traffic host label.
	assertGaugeValue(t, metrics.HTTPOriginLatencySeconds.WithLabelValues("0.5", "g3.munchbox.cc", "example.com"), 0.025)
	assertGaugeValue(t, metrics.HTTPOriginLatencySeconds.WithLabelValues("0.95", "g3.munchbox.cc", "example.com"), 0.036)
	assertGaugeValue(t, metrics.HTTPOriginLatencySeconds.WithLabelValues("0.99", "g3.munchbox.cc", "example.com"), 0.38)
	assertGaugeValue(t, metrics.HTTPEdgeTTFBSeconds.WithLabelValues("0.95", "b2-proxy.munchbox.cc", "example.com"), 0.882)

	// A Worker host has edge latency but no origin latency.
	if got := testutil.CollectAndCount(metrics.HTTPOriginLatencySeconds); got != 3 {
		t.Errorf("origin latency series = %d, want 3 (one host x 3 quantiles)", got)
	}
}

func TestHTTPPoll_LatencyFailureLeavesTrafficAndGaugesAlone(t *testing.T) {
	resetHTTPMetrics()
	resetLatencyMetrics()
	metrics.HTTPOriginLatencySeconds.WithLabelValues("0.95", "g3.munchbox.cc", "example.com").Set(0.04)
	cf := &fakeCloudflare{
		httpGroups: []cloudflare.HTTPRequestGroup{httpGroup(4, 40, cloudflare.HTTPRequestDimensions{
			ClientRequestHTTPHost: "example.com", ClientRequestHTTPMethodName: "GET", EdgeResponseStatus: 200,
			CacheStatus: "dynamic", RequestSource: "eyeball", ClientCountryName: "US",
		})},
		latencyErr: errors.New("boom"),
	}
	c := NewHTTPCollector(zoneTestConfig(cf, &fakePusher{}))

	c.poll(context.Background())

	// The polls are independent: traffic is still counted and the cursor
	// moves, and the failed latency poll keeps its last values.
	assertCounterValue(t, metrics.HTTPRequestsTotal.WithLabelValues("GET", "200", "US", "example.com", "dynamic", "eyeball", "example.com"), 4)
	if !c.lastSeen.Equal(cf.httpCalls[0].until) {
		t.Errorf("cursor = %v, want the traffic window end %v", c.lastSeen, cf.httpCalls[0].until)
	}
	assertGaugeValue(t, metrics.HTTPOriginLatencySeconds.WithLabelValues("0.95", "g3.munchbox.cc", "example.com"), 0.04)
}
