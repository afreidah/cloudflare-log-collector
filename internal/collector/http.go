// -------------------------------------------------------------------------------
// HTTP Traffic Collector
//
// Author: Alex Freidah
//
// Polls the Cloudflare httpRequestsAdaptiveGroups dataset on a configurable
// interval. Accumulates aggregated traffic statistics into Prometheus counters -
// per host, with cache, origin, and request-source breakdowns - and ships raw
// traffic groups to Loki as structured JSON logs. Its seek cursor
// moves to the end of each handled window (see cursor.go), and each cycle runs
// through the shared cursor-based poll in poll.go.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"
)

// -------------------------------------------------------------------------
// HTTP COLLECTOR
// -------------------------------------------------------------------------

// HTTPCollector polls Cloudflare for HTTP traffic stats, updates Prometheus
// counters, and ships raw traffic data to Loki. The base's scope is the zone
// name.
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

// poll runs one HTTP traffic poll cycle; the cursor moves to the end of each
// handled window.
func (c *HTTPCollector) poll(ctx context.Context) {
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

		metrics.HTTPRequestsTotal.WithLabelValues(
			d.ClientRequestHTTPMethodName, status, d.ClientCountryName, host, d.CacheStatus, d.RequestSource, c.scope,
		).Add(count)
		metrics.HTTPOriginRequestsTotal.WithLabelValues(
			host, statusLabel(d.OriginResponseStatus), status, d.RequestSource, c.scope,
		).Add(count)
		metrics.HTTPBytesTotal.WithLabelValues("edge", host, d.RequestSource, c.scope).Add(float64(groups[i].Sum.EdgeResponseBytes))
	}
}
