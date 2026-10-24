// -------------------------------------------------------------------------------
// HTTP Visits Collector
//
// Author: Alex Freidah
//
// Polls a zone's server-side visit counts per minute and host through the
// cursor-based poll: groups with visits ship to Loki and count into
// cflog_http_visits_total. It needs no browser beacon, so it keeps counting
// when Web Analytics cannot.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"slices"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"
)

// VisitsCollector polls one zone's visits. The base's scope is the zone name;
// query is the visits dataset with the configured user agents excluded, and
// lastSeen is the cursor.
type VisitsCollector struct {
	base
	cf       visitsQuerier
	query    cloudflare.HTTPVisitsQuery
	zoneID   string
	lastSeen time.Time
}

// NewVisitsCollector creates a visits collector for the given zone running
// query, with the backfill window applied to the initial poll.
func NewVisitsCollector(cfg ZoneCollectorConfig, query cloudflare.HTTPVisitsQuery) *VisitsCollector {
	c := &VisitsCollector{
		base: newBase(cfg.CommonConfig, identity{
			name:     "HTTP visits",
			stream:   DatasetHTTPVisits.String(),
			scopeKey: "zone",
			scope:    cfg.ZoneName,
			record:   "HTTP visits group",
		}),
		cf:     cfg.CF,
		query:  query,
		zoneID: cfg.ZoneID,
	}
	c.lastSeen = c.cursorStart()
	return c
}

// Run polls on the configured interval and blocks until ctx is cancelled.
// Implements the lifecycle.Runner interface.
func (c *VisitsCollector) Run(ctx context.Context) error {
	return c.run(ctx, c.lastSeen, c.poll)
}

// poll counts the visits in the next window; the cursor moves to the end of
// each handled window. Groups without visits (requests that were not page
// views, such as assets and API calls) are dropped before shipping.
func (c *VisitsCollector) poll(ctx context.Context) {
	pollCursor(ctx, &c.base, cursorPoll[cloudflare.HTTPVisitsGroup]{
		dataset:   DatasetHTTPVisits,
		span:      "http_visits.poll",
		title:     "HTTP visits groups",
		countAttr: telemetry.AttrGroupCount,
		query: func(ctx context.Context, since, until time.Time) ([]cloudflare.HTTPVisitsGroup, error) {
			return c.cf.QueryHTTPVisits(ctx, c.query, c.zoneID, since, until)
		},
		filter: func(groups []cloudflare.HTTPVisitsGroup) []cloudflare.HTTPVisitsGroup {
			return slices.DeleteFunc(groups, func(g cloudflare.HTTPVisitsGroup) bool { return g.Sum.Visits == 0 })
		},
		cursor:   &c.lastSeen,
		advancer: windowEnd[cloudflare.HTTPVisitsGroup]{},
		count: func(groups []cloudflare.HTTPVisitsGroup) {
			for i := range groups {
				host := normalizeHost(groups[i].Dimensions.ClientRequestHTTPHost)
				c.add(metrics.HTTPVisitsTotal, float64(groups[i].Sum.Visits), host)
			}
		},
	})
}
