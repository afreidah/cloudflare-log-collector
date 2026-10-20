// -------------------------------------------------------------------------------
// Firewall Event Collector
//
// Author: Alex Freidah
//
// Polls the Cloudflare firewallEventsAdaptive dataset on a configurable interval
// and ships events to Loki as structured JSON log lines, counting them by
// action. Its seek cursor follows the last event shipped (see cursor.go), and
// each cycle runs through the shared cursor-based poll in poll.go.
// -------------------------------------------------------------------------------

// Package collector runs the firewall, HTTP traffic, audit log, and RUM poll
// loops, orchestrating Cloudflare queries, Prometheus metrics, and Loki
// shipping. Behavior every collector shares lives in base.go.
package collector

import (
	"context"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"
)

// -------------------------------------------------------------------------
// ZONE COLLECTOR CONFIG
// -------------------------------------------------------------------------

// ZoneCollectorConfig holds the parameters for constructing a zone-scoped
// collector (firewall or HTTP). CF is a consumer-declared interface, not a
// concrete pointer, so the composition root (main.go) passes the real
// *cloudflare.Client while tests substitute fakes.
type ZoneCollectorConfig struct {
	CommonConfig
	CF       cloudflareQuerier
	ZoneID   string
	ZoneName string
}

// -------------------------------------------------------------------------
// FIREWALL COLLECTOR
// -------------------------------------------------------------------------

// FirewallCollector polls Cloudflare for firewall events and ships them to
// Loki. The base's scope is the zone name.
type FirewallCollector struct {
	base
	cf       firewallQuerier
	zoneID   string
	lastSeen time.Time
}

// NewFirewallCollector creates a firewall event collector for the given zone
// with the backfill window applied to the initial poll.
func NewFirewallCollector(cfg ZoneCollectorConfig) *FirewallCollector {
	c := &FirewallCollector{
		base: newBase(cfg.CommonConfig, identity{
			name:     "Firewall",
			stream:   DatasetFirewall.String(),
			scopeKey: "zone",
			scope:    cfg.ZoneName,
			record:   "firewall event",
		}),
		cf:     cfg.CF,
		zoneID: cfg.ZoneID,
	}
	c.lastSeen = c.cursorStart()
	return c
}

// Run polls on the configured interval and blocks until ctx is cancelled.
// Implements the lifecycle.Runner interface.
func (c *FirewallCollector) Run(ctx context.Context) error {
	return c.run(ctx, c.lastSeen, c.poll)
}

// poll runs one firewall event poll cycle; the cursor moves to the last event
// shipped.
func (c *FirewallCollector) poll(ctx context.Context) {
	pollCursor(ctx, &c.base, cursorPoll[cloudflare.FirewallEvent]{
		dataset:   DatasetFirewall,
		span:      "firewall.poll",
		title:     "Firewall events",
		countAttr: telemetry.AttrEventCount,
		cursor:    &c.lastSeen,
		advancer:  lastEvent[cloudflare.FirewallEvent]{timestamp: func(e cloudflare.FirewallEvent) string { return e.Datetime }},
		query: func(ctx context.Context, since, until time.Time) ([]cloudflare.FirewallEvent, error) {
			return c.cf.QueryFirewallEvents(ctx, c.zoneID, since, until)
		},
		count: func(events []cloudflare.FirewallEvent) {
			for i := range events {
				metrics.FirewallEventsTotal.WithLabelValues(events[i].Action, c.scope).Inc()
			}
		},
	})
}
