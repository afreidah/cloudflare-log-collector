// -------------------------------------------------------------------------------
// DNS Analytics Collector
//
// Author: Alex Freidah
//
// Polls a zone's authoritative DNS analytics: query groups ship to Loki and
// count into Prometheus through the cursor-based poll, and processing time
// quantiles are gauges through the trailing poll.
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

// DNSCollector polls one zone's DNS analytics. The base's scope is the zone
// name; lastSeen is the query cursor.
type DNSCollector struct {
	base
	cf       dnsQuerier
	zoneID   string
	lastSeen time.Time
}

// NewDNSCollector creates a DNS collector for the given zone with the backfill
// window applied to the initial poll.
func NewDNSCollector(cfg ZoneCollectorConfig) *DNSCollector {
	c := &DNSCollector{
		base: newBase(cfg.CommonConfig, identity{
			name:     "DNS",
			stream:   DatasetDNS.String(),
			scopeKey: "zone",
			scope:    cfg.ZoneName,
			record:   "DNS query group",
		}),
		cf:     cfg.CF,
		zoneID: cfg.ZoneID,
	}
	c.lastSeen = c.cursorStart()
	return c
}

// Run polls on the configured interval and blocks until ctx is cancelled.
// Implements the lifecycle.Runner interface.
func (c *DNSCollector) Run(ctx context.Context) error {
	return c.run(ctx, c.lastSeen, c.poll)
}

// poll runs the query poll and the processing time poll independently.
func (c *DNSCollector) poll(ctx context.Context) {
	c.pollQueries(ctx)
	c.pollProcessing(ctx)
}

// pollQueries ships query groups to Loki and counts them by type and response
// code; the cursor moves to the end of each handled window.
func (c *DNSCollector) pollQueries(ctx context.Context) {
	pollCursor(ctx, &c.base, cursorPoll[cloudflare.DNSQueryGroup]{
		dataset:   DatasetDNS,
		span:      "dns.poll",
		title:     "DNS query groups",
		countAttr: telemetry.AttrGroupCount,
		cursor:    &c.lastSeen,
		advancer:  windowEnd[cloudflare.DNSQueryGroup]{},
		query: func(ctx context.Context, since, until time.Time) ([]cloudflare.DNSQueryGroup, error) {
			return c.cf.QueryDNSQueries(ctx, c.zoneID, since, until)
		},
		count: func(groups []cloudflare.DNSQueryGroup) {
			for i := range groups {
				d := groups[i].Dimensions
				c.add(metrics.DNSQueriesTotal, float64(groups[i].Count), d.QueryType, d.ResponseCode)
			}
		},
	})
}

// pollProcessing reports the zone's processing time p50, p95, and p99,
// reported by Cloudflare in microseconds, over the trailing window.
func (c *DNSCollector) pollProcessing(ctx context.Context) {
	pollTrailing(ctx, &c.base, trailingPoll[cloudflare.DNSProcessingGroup]{
		dataset:   DatasetDNSProcessing,
		span:      "dns.processing.poll",
		title:     "DNS processing time",
		countAttr: telemetry.AttrGroupCount,
		query: func(ctx context.Context, since, until time.Time) ([]cloudflare.DNSProcessingGroup, error) {
			return c.cf.QueryDNSProcessingTime(ctx, c.zoneID, since, until)
		},
		gauges: []*prometheus.GaugeVec{metrics.DNSProcessingSeconds},
		set: func(groups []cloudflare.DNSProcessingGroup) {
			for i := range groups {
				c.setPercentiles(metrics.DNSProcessingSeconds, groups[i].Quantiles.Percentiles(), time.Microsecond)
			}
		},
	})
}
