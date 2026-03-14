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
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
)

// -------------------------------------------------------------------------
// HTTP COLLECTOR
// -------------------------------------------------------------------------

// HTTPCollector polls Cloudflare for HTTP traffic stats, updates Prometheus
// gauges, and ships raw traffic data to Loki.
type HTTPCollector struct {
	cf           *cloudflare.Client
	loki         *loki.Client
	zoneID       string
	zoneName     string
	pollInterval time.Duration
	lastSeen     time.Time
	batchSize    int
}

// NewHTTPCollector creates an HTTP traffic collector for the given zone
// with the backfill window applied to the initial poll.
func NewHTTPCollector(cf *cloudflare.Client, lokiClient *loki.Client, zoneID, zoneName string, pollInterval, backfillWindow time.Duration, batchSize int) *HTTPCollector {
	return &HTTPCollector{
		cf:           cf,
		loki:         lokiClient,
		zoneID:       zoneID,
		zoneName:     zoneName,
		pollInterval: pollInterval,
		lastSeen:     time.Now().UTC().Add(-backfillWindow),
		batchSize:    batchSize,
	}
}

// Run starts the polling loop and blocks until ctx is cancelled. Implements
// the lifecycle.Service interface.
func (c *HTTPCollector) Run(ctx context.Context) error {
	slog.Info("HTTP collector started",
		"zone", c.zoneName,
		"poll_interval", c.pollInterval,
		"backfill_from", c.lastSeen.Format(time.RFC3339),
	)

	// --- Initial poll on startup ---
	c.poll(ctx)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("HTTP collector stopped")
			return nil
		case <-ticker.C:
			c.poll(ctx)
		}
	}
}

// poll executes a single HTTP traffic collection cycle within a traced span.
func (c *HTTPCollector) poll(ctx context.Context) {
	ctx, span := telemetry.StartSpan(ctx, "http.poll",
		telemetry.AttrDataset.String("http"),
		attribute.String("cflog.zone", c.zoneName),
	)
	defer span.End()

	start := time.Now()
	until := time.Now().UTC()

	groups, err := c.cf.QueryHTTPRequests(ctx, c.zoneID, c.lastSeen, until)
	if err != nil {
		slog.ErrorContext(ctx, "HTTP traffic poll failed", "error", err)
		metrics.PollTotal.WithLabelValues("http", "error").Inc()
		metrics.PollDuration.WithLabelValues("http").Observe(time.Since(start).Seconds())
		return
	}

	metrics.PollTotal.WithLabelValues("http", "success").Inc()
	metrics.PollDuration.WithLabelValues("http").Observe(time.Since(start).Seconds())
	metrics.LastPollTimestamp.WithLabelValues("http").Set(float64(time.Now().Unix()))

	span.SetAttributes(attribute.Int("cflog.group_count", len(groups)))

	if len(groups) == 0 {
		slog.DebugContext(ctx, "No new HTTP traffic data")
		c.lastSeen = until
		return
	}

	slog.InfoContext(ctx, "HTTP traffic data fetched", "groups", len(groups))

	// --- Update Prometheus gauges ---
	c.updateMetrics(groups)

	// --- Ship raw groups to Loki ---
	if err := c.shipToLoki(ctx, groups); err != nil {
		slog.ErrorContext(ctx, "Failed to ship HTTP traffic to Loki", "error", err)
	} else {
		slog.InfoContext(ctx, "HTTP traffic pushed to Loki", "groups", len(groups))
	}

	c.lastSeen = until
}

// updateMetrics resets and repopulates Prometheus gauges from the latest poll data.
func (c *HTTPCollector) updateMetrics(groups []cloudflare.HTTPRequestGroup) {
	// --- Reset gauges before repopulating ---
	metrics.HTTPRequests.Reset()
	metrics.HTTPBytes.Reset()

	// --- Aggregate totals across all groups ---
	var totalEdgeBytes int64

	for _, g := range groups {
		method := g.Dimensions.ClientRequestHTTPMethodName
		status := fmt.Sprintf("%d", g.Dimensions.EdgeResponseStatus)
		country := g.Dimensions.ClientCountryName

		metrics.HTTPRequests.WithLabelValues(method, status, country).Add(float64(g.Count))

		totalEdgeBytes += g.Sum.EdgeResponseBytes
	}

	metrics.HTTPBytes.WithLabelValues("edge").Set(float64(totalEdgeBytes))
}

// shipToLoki sends HTTP traffic groups to Loki as JSON log lines in batches.
func (c *HTTPCollector) shipToLoki(ctx context.Context, groups []cloudflare.HTTPRequestGroup) error {
	labels := map[string]string{
		"job":  "cloudflare",
		"type": "http_traffic",
		"zone": c.zoneName,
	}

	entries := make([]loki.Entry, 0, len(groups))
	for _, g := range groups {
		line, err := json.Marshal(g)
		if err != nil {
			slog.WarnContext(ctx, "Failed to marshal HTTP traffic group", "error", err)
			continue
		}

		t, err := time.Parse(time.RFC3339Nano, g.Dimensions.Datetime)
		if err != nil {
			t = time.Now().UTC()
		}

		entries = append(entries, loki.NewEntry(t, string(line)))
	}

	// --- Send in batches ---
	for i := 0; i < len(entries); i += c.batchSize {
		end := min(i+c.batchSize, len(entries))

		if err := c.loki.Push(ctx, labels, entries[i:end]); err != nil {
			return err
		}
	}

	return nil
}
