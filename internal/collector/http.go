// -------------------------------------------------------------------------------
// HTTP Traffic Collector
//
// Author: Alex Freidah
//
// Polls the Cloudflare httpRequestsAdaptiveGroups dataset on a configurable
// interval. Accumulates aggregated traffic statistics into Prometheus counters -
// per host, with cache, origin, and request-source breakdowns - and ships raw
// traffic groups to Loki as structured JSON logs; the traffic cursor moves to
// the end of each handled window (see cursor.go) through the shared
// cursor-based poll. Per-host origin and edge latency quantiles are reported
// as gauges through the shared trailing poll. Both templates live in poll.go.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"github.com/prometheus/client_golang/prometheus"
)

// -------------------------------------------------------------------------
// HTTP COLLECTOR
// -------------------------------------------------------------------------

// HTTPCollector polls Cloudflare for HTTP traffic stats, updates Prometheus
// counters, and ships raw traffic data to Loki, and reports per-host origin
// and edge latency quantiles as gauges. The base's scope is the zone name.
// lastSeen is the traffic cursor; the latency polls use the trailing window
// and need no cursor.
type HTTPCollector struct {
	base
	cf       httpQuerier
	zoneID   string
	lastSeen time.Time
}

// NewHTTPCollector creates an HTTP traffic collector for the given zone
// with the backfill window applied to the initial poll.
func NewHTTPCollector(cfg ZoneCollectorConfig) *HTTPCollector {
	c := &HTTPCollector{
		base: newBase(cfg.CommonConfig, identity{
			name: "HTTP",
			// Ships as "http_traffic", not the dataset name, so existing Loki
			// queries keep matching.
			stream:   "http_traffic",
			scopeKey: "zone",
			scope:    cfg.ZoneName,
			record:   "HTTP traffic group",
		}),
		cf:     cfg.CF,
		zoneID: cfg.ZoneID,
	}
	c.lastSeen = c.cursorStart()
	return c
}

// Run polls on the configured interval and blocks until ctx is cancelled.
// Implements the lifecycle.Runner interface.
func (c *HTTPCollector) Run(ctx context.Context) error {
	return c.run(ctx, c.lastSeen, c.poll)
}

// poll runs the traffic poll and both latency polls. They are independent: a
// failure in one does not block or affect the others.
func (c *HTTPCollector) poll(ctx context.Context) {
	c.pollTraffic(ctx)
	pollLatency(ctx, c, DatasetHTTPOriginLatency, "HTTP origin latency", metrics.HTTPOriginLatencySeconds, c.cf.QueryHTTPOriginLatency)
	pollLatency(ctx, c, DatasetHTTPEdgeLatency, "HTTP edge latency", metrics.HTTPEdgeTTFBSeconds, c.cf.QueryHTTPEdgeLatency)
}

// -------------------------------------------------------------------------
// TRAFFIC (counters + Loki, advancing cursor)
// -------------------------------------------------------------------------

// pollTraffic runs one HTTP traffic poll cycle; the cursor moves to the end of
// each handled window.
func (c *HTTPCollector) pollTraffic(ctx context.Context) {
	pollCursor(ctx, &c.base, cursorPoll[cloudflare.HTTPRequestGroup]{
		dataset:   DatasetHTTP,
		span:      "http.poll",
		title:     "HTTP traffic groups",
		countAttr: telemetry.AttrGroupCount,
		cursor:    &c.lastSeen,
		advancer:  windowEnd[cloudflare.HTTPRequestGroup]{},
		query: func(ctx context.Context, since, until time.Time) ([]cloudflare.HTTPRequestGroup, error) {
			return c.cf.QueryHTTPRequests(ctx, c.zoneID, since, until)
		},
		count: c.updateMetrics,
	})
}

// updateMetrics adds the latest poll window's counts to the cumulative HTTP
// counters. Poll windows never overlap (the cursor advances by exclusive
// datetime_gt), so adding each window's counts yields a correct running total
// without resetting between polls - which keeps the series immune to poll-window
// length and lets dashboards derive rates with rate()/increase().
func (c *HTTPCollector) updateMetrics(groups []cloudflare.HTTPRequestGroup) {
	for i := range groups {
		d := groups[i].Dimensions
		count := float64(groups[i].Count)
		host := normalizeHost(d.ClientRequestHTTPHost)
		status := statusLabel(d.EdgeResponseStatus)

		c.add(metrics.HTTPRequestsTotal, count,
			d.ClientRequestHTTPMethodName, status, d.ClientCountryName, host, d.CacheStatus, d.RequestSource)
		c.add(metrics.HTTPOriginRequestsTotal, count,
			host, statusLabel(d.OriginResponseStatus), status, d.RequestSource)
		c.add(metrics.HTTPBytesTotal, float64(groups[i].Sum.EdgeResponseBytes), "edge", host, d.RequestSource)
	}
}

// -------------------------------------------------------------------------
// LATENCY (gauges, trailing window)
// -------------------------------------------------------------------------

// latencyQuery is a zone latency query on the Cloudflare client, returning one
// group per host with quantile set Q.
type latencyQuery[Q cloudflare.LatencyQuantiles] func(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPLatencyGroup[Q], error)

// pollLatency runs one trailing poll of a latency dataset, repopulating gauge
// with each host's p50, p95, and p99 in seconds. Origin and edge latency
// differ only in their dataset, gauge, and query, so both run through here.
func pollLatency[Q cloudflare.LatencyQuantiles](ctx context.Context, c *HTTPCollector, dataset Dataset, title string, gauge *prometheus.GaugeVec, query latencyQuery[Q]) {
	pollTrailing(ctx, &c.base, trailingPoll[cloudflare.HTTPLatencyGroup[Q]]{
		dataset:   dataset,
		span:      dataset.String() + ".poll",
		title:     title,
		countAttr: telemetry.AttrGroupCount,
		query: func(ctx context.Context, since, until time.Time) ([]cloudflare.HTTPLatencyGroup[Q], error) {
			return query(ctx, c.zoneID, since, until)
		},
		gauges: []*prometheus.GaugeVec{gauge},
		set: func(groups []cloudflare.HTTPLatencyGroup[Q]) {
			for i := range groups {
				host := normalizeHost(groups[i].Dimensions.ClientRequestHTTPHost)
				p := groups[i].Quantiles.Percentiles()
				c.setDuration(gauge, p.P50, time.Millisecond, "0.5", host)
				c.setDuration(gauge, p.P95, time.Millisecond, "0.95", host)
				c.setDuration(gauge, p.P99, time.Millisecond, "0.99", host)
			}
		},
	})
}
