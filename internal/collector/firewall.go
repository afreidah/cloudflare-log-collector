// -------------------------------------------------------------------------------
// Firewall Event Collector
//
// Author: Alex Freidah
//
// Polls the Cloudflare firewallEventsAdaptive dataset on a configurable interval
// and ships events to Loki as structured JSON log lines. Tracks the last-seen
// event timestamp for seek-based pagination to avoid duplicates across polls.
// Each poll cycle is wrapped in an OpenTelemetry span for trace correlation.
// -------------------------------------------------------------------------------

// Package collector runs the firewall, HTTP traffic, audit log, and RUM poll
// loops, orchestrating Cloudflare queries, Prometheus metrics, and Loki
// shipping. Behavior every collector shares lives in base.go.
package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
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

// poll executes a single firewall event collection cycle within a traced span.
func (c *FirewallCollector) poll(ctx context.Context) {
	ctx, span := telemetry.StartSpan(ctx, "firewall.poll",
		telemetry.AttrDataset.String(DatasetFirewall.String()),
		c.scopeAttr(),
	)
	defer span.End()

	start := time.Now()
	until := time.Now().UTC()

	events, err := c.cf.QueryFirewallEvents(ctx, c.zoneID, c.lastSeen, until)
	c.recordPoll(DatasetFirewall, start, err)
	if err != nil {
		c.fail(ctx, span, "Firewall poll failed", err)
		return
	}

	span.SetAttributes(attribute.Int("cflog.event_count", len(events)))

	if len(events) == 0 {
		slog.DebugContext(ctx, "No new firewall events")
		return
	}

	slog.InfoContext(ctx, "Firewall events fetched", "count", len(events))

	// --- Ship events to Loki in batches, then count them and advance ---
	if err := shipThenCommit(ctx, &c.base, events, func() {
		for i := range events {
			metrics.FirewallEventsTotal.WithLabelValues(events[i].Action, c.scope).Inc()
		}

		// Advance cursor to the last event's timestamp.
		lastEvent := events[len(events)-1]
		if t, ok := parseEventTime(lastEvent.Datetime); ok {
			c.lastSeen = t
		} else {
			slog.WarnContext(ctx, "Unparseable firewall event timestamp, cursor not advanced",
				"zone", c.scope, "datetime", lastEvent.Datetime)
		}
	}); err != nil {
		c.fail(ctx, span, "Failed to ship firewall events to Loki", err)
		return
	}

	slog.InfoContext(ctx, "Firewall events pushed to Loki", "count", len(events))
}
