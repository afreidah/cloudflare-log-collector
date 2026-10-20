// -------------------------------------------------------------------------------
// HTTP Traffic Collector
//
// Author: Alex Freidah
//
// Polls the Cloudflare httpRequestsAdaptiveGroups dataset on a configurable
// interval. Updates Prometheus gauges with aggregated traffic statistics and
// ships raw traffic groups to Loki as structured JSON logs. Each poll cycle
// is wrapped in an OpenTelemetry span for trace correlation.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
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

// poll executes a single HTTP traffic collection cycle within a traced span.
func (c *HTTPCollector) poll(ctx context.Context) {
	ctx, span := telemetry.StartSpan(ctx, "http.poll",
		telemetry.AttrDataset.String(DatasetHTTP.String()),
		c.scopeAttr(),
	)
	defer span.End()

	start := time.Now()
	until := time.Now().UTC()

	groups, err := c.cf.QueryHTTPRequests(ctx, c.zoneID, c.lastSeen, until)
	c.recordPoll(DatasetHTTP, start, err)
	if err != nil {
		c.fail(ctx, span, "HTTP traffic poll failed", err)
		return
	}

	span.SetAttributes(attribute.Int("cflog.group_count", len(groups)))

	if len(groups) == 0 {
		slog.DebugContext(ctx, "No new HTTP traffic data")
		c.lastSeen = until
		return
	}

	slog.InfoContext(ctx, "HTTP traffic data fetched", "groups", len(groups))

	// --- Ship raw groups to Loki, then count the window and advance ---
	if err := shipThenCommit(ctx, &c.base, groups, func() {
		c.updateMetrics(groups)
		c.lastSeen = until
	}); err != nil {
		c.fail(ctx, span, "Failed to ship HTTP traffic to Loki", err)
		return
	}

	slog.InfoContext(ctx, "HTTP traffic pushed to Loki", "groups", len(groups))
}

// updateMetrics adds the latest poll window's counts to the cumulative HTTP
// counters. Poll windows never overlap (the cursor advances by exclusive
// datetime_gt), so adding each window's counts yields a correct running total
// without resetting between polls - which keeps the series immune to poll-window
// length and lets dashboards derive rates with rate()/increase().
func (c *HTTPCollector) updateMetrics(groups []cloudflare.HTTPRequestGroup) {
	var totalEdgeBytes int64

	for _, g := range groups {
		method := g.Dimensions.ClientRequestHTTPMethodName
		status := fmt.Sprintf("%d", g.Dimensions.EdgeResponseStatus)
		country := g.Dimensions.ClientCountryName

		metrics.HTTPRequestsTotal.WithLabelValues(method, status, country, c.scope).Add(float64(g.Count))

		totalEdgeBytes += g.Sum.EdgeResponseBytes
	}

	metrics.HTTPBytesTotal.WithLabelValues("edge", c.scope).Add(float64(totalEdgeBytes))
}
