// -------------------------------------------------------------------------------
// RUM / Web Analytics Collector
//
// Author: Alex Freidah
//
// Polls Cloudflare Web Analytics (RUM) for one site and emits browser-side
// telemetry. The two RUM datasets are handled differently because of their
// shapes: page loads are counted, so they run through the shared cursor-based
// poll in poll.go, held back by the Web Analytics ingestion lag, and accumulate
// into counters - raw rows ship to Loki, bounded counters to Prometheus. Web
// vitals are p75 quantiles, which cannot be accumulated, so each poll reports
// the p75 over a fixed trailing window into gauges; re-querying that window
// every poll self-heals late data, so it needs no lag. The RUM datasets are
// account-scoped and identified by site tag. A site covers every host in its
// zone, so all RUM metrics carry a host label, and known bots are kept out of
// them: bot page loads ship to Loki but are not counted, and web vitals are
// queried for real browsers only.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"
)

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
// Prometheus. The base's scope is the site name. pageloadSeen is the seek
// cursor for the page-view counter stream; it advances only when a poll fully
// succeeds (queried, shipped, counted) so a page view is counted exactly once
// across polls. Web vitals use a rolling window instead and need no cursor.
type RUMCollector struct {
	base
	cf           rumQuerier
	accountID    string
	siteTag      string
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

// pollPageloads runs one page-load poll cycle: raw rows ship to Loki, bounded
// page-view/session counters accumulate, and the cursor moves to the end of
// each handled window.
func (c *RUMCollector) pollPageloads(ctx context.Context) {
	pollCursor(ctx, &c.base, cursorPoll[cloudflare.RUMPageloadGroup]{
		dataset:   DatasetRUMPageload,
		span:      "rum.pageload.poll",
		title:     "RUM page loads",
		countAttr: telemetry.AttrGroupCount,
		cursor:    &c.pageloadSeen,
		advancer:  windowEnd[cloudflare.RUMPageloadGroup]{},
		query: func(ctx context.Context, since, until time.Time) ([]cloudflare.RUMPageloadGroup, error) {
			return c.cf.QueryRUMPageloads(ctx, c.accountID, c.siteTag, since, until)
		},
		count: c.updatePageloadMetrics,
	})
}

// updatePageloadMetrics accumulates page-view and session counters at bounded
// cardinality (country x device x host, hosts being the sites that carry the
// beacon); request path and referer stay in Loki only. Known-bot rows have
// already shipped to Loki but are not visitors, so they are not counted.
func (c *RUMCollector) updatePageloadMetrics(groups []cloudflare.RUMPageloadGroup) {
	for i := range groups {
		d := groups[i].Dimensions
		if d.Bot != 0 {
			continue
		}

		host := normalizeHost(d.RequestHost)
		c.add(metrics.RUMPageviewsTotal, float64(groups[i].Count), d.CountryName, d.DeviceType, host)
		if visits := groups[i].Sum.Visits; visits > 0 {
			c.add(metrics.RUMSessionsTotal, float64(visits), d.CountryName, d.DeviceType, host)
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
	until := start.UTC()
	since := until.Add(-c.backfillWindow)

	groups, err := c.cf.QueryRUMWebVitals(ctx, c.accountID, c.siteTag, since, until)
	c.recordPoll(DatasetRUMWebVitals, start, err)
	if err != nil {
		c.fail(ctx, span, "RUM web vitals poll failed", err)
		return
	}

	span.SetAttributes(telemetry.AttrGroupCount.Int(len(groups)))

	// Clear this site's vitals before repopulating so a host or device type
	// absent from the latest window does not linger at a stale value.
	c.clear(metrics.RUMWebVitalSeconds)
	c.clear(metrics.RUMCumulativeLayoutShift)

	c.updateWebVitalsMetrics(groups)
	slog.DebugContext(ctx, "RUM web vitals collected", "site", c.scope, "groups", len(groups))
}

// updateWebVitalsMetrics sets the per-host, per-device Core Web Vitals gauges
// from the rolling-window quantiles. Known bots are already filtered out by the
// query.
func (c *RUMCollector) updateWebVitalsMetrics(groups []cloudflare.RUMWebVitalsGroup) {
	for i := range groups {
		host := normalizeHost(groups[i].Dimensions.RequestHost)
		device := groups[i].Dimensions.DeviceType
		q := groups[i].Quantiles

		c.setTimeVital("lcp", device, host, q.LargestContentfulPaintP75)
		c.setTimeVital("inp", device, host, q.InteractionToNextPaintP75)
		c.setTimeVital("fid", device, host, q.FirstInputDelayP75)
		c.setTimeVital("fcp", device, host, q.FirstContentfulPaintP75)
		c.setTimeVital("ttfb", device, host, q.TimeToFirstByteP75)

		// CLS is a unitless layout-shift score, not a duration.
		if cls := q.CumulativeLayoutShiftP75; cls >= 0 {
			c.set(metrics.RUMCumulativeLayoutShift, cls, device, host)
		}
	}
}

// setTimeVital records a time-based Core Web Vital, converting the API's
// microseconds to seconds. The -1 "no data" sentinel (e.g. no first-input delay
// when there was no interaction) is skipped so it never shows up as a real 0.
func (c *RUMCollector) setTimeVital(vital, device, host string, microseconds float64) {
	if microseconds < 0 {
		return
	}
	c.set(metrics.RUMWebVitalSeconds, microseconds/1e6, vital, device, host)
}
