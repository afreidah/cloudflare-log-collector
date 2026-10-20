// -------------------------------------------------------------------------------
// RUM / Web Analytics Collector
//
// Author: Alex Freidah
//
// Polls Cloudflare Web Analytics (RUM) for one site and emits browser-side
// telemetry. The two RUM datasets are handled differently because of their
// shapes: page loads are counted, so they advance a seek cursor (exclusive
// datetime_gt) and accumulate into counters - raw rows ship to Loki, bounded
// counters to Prometheus. Web vitals are p75 quantiles, which cannot be
// accumulated, so each poll reports the p75 over a fixed trailing window into
// gauges. The RUM datasets are account-scoped and identified by site tag.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
)

// rumIngestionLag holds the page-load query window's leading edge back from
// "now". RUM adaptive data lands with a processing delay, and the page-load
// cursor visits each window exactly once, so querying right up to "now" would
// read an empty (not-yet-ingested) window and then advance past it, permanently
// skipping data that arrives later. Web vitals re-query a fixed trailing window
// each poll and so self-heal without this. If page views still lag reality,
// raise this above Cloudflare's observed RUM delay.
const rumIngestionLag = 10 * time.Minute

// -------------------------------------------------------------------------
// RUM COLLECTOR CONFIG
// -------------------------------------------------------------------------

// RUMCollectorConfig holds the parameters for constructing a RUM collector.
// CF is a consumer-declared interface, not a concrete pointer, so the
// composition root (main.go) passes the real *cloudflare.Client while tests
// substitute fakes.
type RUMCollectorConfig struct {
	CommonConfig
	CF        rumQuerier
	AccountID string
	SiteTag   string
	SiteName  string
}

// -------------------------------------------------------------------------
// RUM COLLECTOR
// -------------------------------------------------------------------------

// RUMCollector polls Cloudflare Web Analytics for one site, shipping page-load
// detail to Loki and page-view/session counters plus Core Web Vitals gauges to
// Prometheus. The base's scope is the site name.
type RUMCollector struct {
	base
	cf        rumQuerier
	accountID string
	siteTag   string

	// pageloadSeen is the seek cursor for the page-view counter stream. It
	// advances only when a poll fully succeeds (queried, shipped, counted) so a
	// page view is counted exactly once across polls. Web vitals use a rolling
	// window instead and need no cursor.
	pageloadSeen time.Time
}

// NewRUMCollector creates a RUM collector for the given Web Analytics site with
// the backfill window applied to the initial page-load poll.
func NewRUMCollector(cfg RUMCollectorConfig) *RUMCollector {
	c := &RUMCollector{
		base: newBase(cfg.CommonConfig, identity{
			name:     "RUM",
			stream:   DatasetRUMPageload.String(),
			scopeKey: "site",
			scope:    cfg.SiteName,
			record:   "RUM page load",
		}),
		cf:        cfg.CF,
		accountID: cfg.AccountID,
		siteTag:   cfg.SiteTag,
	}
	c.pageloadSeen = c.cursorStart()
	return c
}

// Run polls on the configured interval and blocks until ctx is cancelled.
// Implements the lifecycle.Runner interface.
func (c *RUMCollector) Run(ctx context.Context) error {
	return c.run(ctx, c.pageloadSeen, c.poll)
}

// poll runs both RUM datasets. They are independent: a failure in one does not
// block or affect the other.
func (c *RUMCollector) poll(ctx context.Context) {
	c.pollPageloads(ctx)
	c.pollWebVitals(ctx)
}

// -------------------------------------------------------------------------
// PAGE LOADS (counters + Loki, advancing cursor)
// -------------------------------------------------------------------------

// pollPageloads collects page-load groups since the cursor, ships the raw rows
// to Loki, and accumulates bounded page-view/session counters.
func (c *RUMCollector) pollPageloads(ctx context.Context) {
	ctx, span := telemetry.StartSpan(ctx, "rum.pageload.poll",
		telemetry.AttrDataset.String(DatasetRUMPageload.String()),
		c.scopeAttr(),
	)
	defer span.End()

	start := time.Now()

	// Hold the leading edge back by the ingestion lag so this window's RUM data
	// has landed; otherwise the cursor would step past not-yet-ingested data.
	until := time.Now().UTC().Add(-rumIngestionLag)
	if !until.After(c.pageloadSeen) {
		return
	}

	groups, err := c.cf.QueryRUMPageloads(ctx, c.accountID, c.siteTag, c.pageloadSeen, until)
	c.recordPoll(DatasetRUMPageload, start, err)
	if err != nil {
		c.fail(ctx, span, "RUM pageload poll failed", err)
		return
	}

	span.SetAttributes(attribute.Int("cflog.group_count", len(groups)))

	if len(groups) == 0 {
		slog.DebugContext(ctx, "No new RUM page loads", "site", c.scope)
		c.pageloadSeen = until
		return
	}

	// Ship raw rows to Loki, then count the window and advance the cursor.
	if err := shipThenCommit(ctx, &c.base, groups, func() {
		c.updatePageloadMetrics(groups)
		c.pageloadSeen = until
	}); err != nil {
		c.fail(ctx, span, "Failed to ship RUM page loads to Loki", err)
		return
	}

	slog.InfoContext(ctx, "RUM page loads collected", "site", c.scope, "groups", len(groups))
}

// updatePageloadMetrics accumulates page-view and session counters at bounded
// cardinality (country x device); request path and referer stay in Loki only.
func (c *RUMCollector) updatePageloadMetrics(groups []cloudflare.RUMPageloadGroup) {
	for i := range groups {
		d := groups[i].Dimensions
		metrics.RUMPageviewsTotal.WithLabelValues(d.CountryName, d.DeviceType, c.scope).Add(float64(groups[i].Count))
		if visits := groups[i].Sum.Visits; visits > 0 {
			metrics.RUMSessionsTotal.WithLabelValues(d.CountryName, d.DeviceType, c.scope).Add(float64(visits))
		}
	}
}


// -------------------------------------------------------------------------
// WEB VITALS (gauges, rolling window)
// -------------------------------------------------------------------------

// pollWebVitals reports the Core Web Vitals p75 over a fixed trailing window.
// Quantiles cannot be accumulated, so there is no cursor: each poll re-queries
// the rolling window and overwrites the gauges.
func (c *RUMCollector) pollWebVitals(ctx context.Context) {
	ctx, span := telemetry.StartSpan(ctx, "rum.webvitals.poll",
		telemetry.AttrDataset.String(DatasetRUMWebVitals.String()),
		c.scopeAttr(),
	)
	defer span.End()

	start := time.Now()
	until := time.Now().UTC()
	since := until.Add(-c.backfillWindow)

	groups, err := c.cf.QueryRUMWebVitals(ctx, c.accountID, c.siteTag, since, until)
	c.recordPoll(DatasetRUMWebVitals, start, err)
	if err != nil {
		c.fail(ctx, span, "RUM web vitals poll failed", err)
		return
	}

	span.SetAttributes(attribute.Int("cflog.group_count", len(groups)))

	// Clear this site's vitals before repopulating so a device type absent from
	// the latest window does not linger at a stale value.
	metrics.RUMWebVitalSeconds.DeletePartialMatch(prometheus.Labels{"site": c.scope})
	metrics.RUMCumulativeLayoutShift.DeletePartialMatch(prometheus.Labels{"site": c.scope})

	c.updateWebVitalsMetrics(groups)
	slog.DebugContext(ctx, "RUM web vitals collected", "site", c.scope, "groups", len(groups))
}

// updateWebVitalsMetrics sets the per-device Core Web Vitals gauges from the
// rolling-window quantiles.
func (c *RUMCollector) updateWebVitalsMetrics(groups []cloudflare.RUMWebVitalsGroup) {
	for i := range groups {
		device := groups[i].Dimensions.DeviceType
		q := groups[i].Quantiles

		c.setTimeVital("lcp", device, q.LargestContentfulPaintP75)
		c.setTimeVital("inp", device, q.InteractionToNextPaintP75)
		c.setTimeVital("fid", device, q.FirstInputDelayP75)
		c.setTimeVital("fcp", device, q.FirstContentfulPaintP75)
		c.setTimeVital("ttfb", device, q.TimeToFirstByteP75)

		// CLS is a unitless layout-shift score, not a duration.
		if cls := q.CumulativeLayoutShiftP75; cls >= 0 {
			metrics.RUMCumulativeLayoutShift.WithLabelValues(device, c.scope).Set(cls)
		}
	}
}

// setTimeVital records a time-based Core Web Vital, converting the API's
// microseconds to seconds. The -1 "no data" sentinel (e.g. no first-input delay
// when there was no interaction) is skipped so it never shows up as a real 0.
func (c *RUMCollector) setTimeVital(vital, device string, microseconds float64) {
	if microseconds < 0 {
		return
	}
	metrics.RUMWebVitalSeconds.WithLabelValues(vital, device, c.scope).Set(microseconds / 1e6)
}
