// -------------------------------------------------------------------------------
// Audit Log Collector
//
// Author: Aaron Florey
//
// Polls the Cloudflare Account Audit Logs REST API on a configurable interval
// and ships events to Loki as structured JSON log lines. Tracks the last-seen
// event timestamp as a seek cursor and dedupes the inclusive `since` boundary
// by event ID so no event ships twice across polls. Each poll cycle is wrapped
// in an OpenTelemetry span for trace correlation.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// -------------------------------------------------------------------------
// AUDIT COLLECTOR CONFIG
// -------------------------------------------------------------------------

// AuditCollectorConfig holds the parameters for constructing an audit collector.
// CF is a consumer-declared interface, not a concrete pointer, so the
// composition root (main.go) passes the real *cloudflare.Client while tests
// substitute fakes.
type AuditCollectorConfig struct {
	CommonConfig
	CF          auditQuerier
	AccountID   string
	AccountName string
}

// -------------------------------------------------------------------------
// AUDIT COLLECTOR
// -------------------------------------------------------------------------

// AuditCollector polls Cloudflare for account audit logs and ships them to
// Loki. The base's scope is the account name.
type AuditCollector struct {
	base
	cf        auditQuerier
	accountID string
	lastSeen  time.Time

	// shippedAtCursor holds the IDs of events already shipped at the lastSeen
	// boundary, so they can be dropped if the API returns them again. See
	// dropAlreadyShipped for why audit needs this and firewall does not.
	shippedAtCursor map[string]struct{}
}

// NewAuditCollector creates an audit log collector for the given account
// with the backfill window applied to the initial poll.
func NewAuditCollector(cfg AuditCollectorConfig) *AuditCollector {
	c := &AuditCollector{
		base: newBase(cfg.CommonConfig, identity{
			name:     "Audit",
			stream:   DatasetAudit.String(),
			scopeKey: "account",
			scope:    cfg.AccountName,
			record:   "audit event",
		}),
		cf:        cfg.CF,
		accountID: cfg.AccountID,
	}
	c.lastSeen = c.cursorStart()
	return c
}

// Run polls on the configured interval and blocks until ctx is cancelled.
// Implements the lifecycle.Runner interface.
func (c *AuditCollector) Run(ctx context.Context) error {
	return c.run(ctx, c.lastSeen, c.poll)
}

// poll executes a single audit log collection cycle within a traced span.
func (c *AuditCollector) poll(ctx context.Context) {
	ctx, span := telemetry.StartSpan(ctx, "audit.poll",
		telemetry.AttrDataset.String(DatasetAudit.String()),
		c.scopeAttr(),
	)
	defer span.End()

	start := time.Now()
	before := time.Now().UTC()

	events, err := c.cf.QueryAuditLogs(ctx, c.accountID, c.lastSeen, before)
	c.recordPoll(DatasetAudit, start, err)
	if err != nil {
		c.fail(ctx, span, "Audit poll failed", err)
		return
	}

	// Drop events already shipped at the previous cursor boundary.
	events = c.dropAlreadyShipped(events)

	span.SetAttributes(attribute.Int("cflog.event_count", len(events)))

	if len(events) == 0 {
		slog.DebugContext(ctx, "No new audit events", "account", c.scope)
		return
	}

	slog.InfoContext(ctx, "Audit events fetched", "account", c.scope, "count", len(events))

	// --- Ship events to Loki in batches, then count them and advance ---
	if err := shipThenCommit(ctx, &c.base, events, func() {
		for i := range events {
			metrics.AuditEventsTotal.WithLabelValues(events[i].Action.Type, c.scope).Inc()
		}

		// Advance cursor to the last event's timestamp and record the IDs sharing
		// that timestamp so the next poll can drop them if the API returns them again.
		lastEvent := events[len(events)-1]
		if t, ok := parseEventTime(lastEvent.Action.Time); ok {
			c.lastSeen = t
			c.shippedAtCursor = boundaryIDs(events, lastEvent.Action.Time)
		} else {
			slog.WarnContext(ctx, "Unparseable audit event timestamp, cursor not advanced",
				"account", c.scope, "time", lastEvent.Action.Time)
		}
	}); err != nil {
		c.fail(ctx, span, "Failed to ship audit events to Loki", err)
		return
	}

	slog.InfoContext(ctx, "Audit events pushed to Loki", "account", c.scope, "count", len(events))
}

// dropAlreadyShipped removes events already shipped on a previous poll. The
// audit logs REST API exposes only a `since` lower bound (with no exclusive
// variant and undocumented inclusivity), so the event(s) at the cursor boundary
// can be returned again on the next poll. The firewall collector avoids this
// with the GraphQL datetime_gt (exclusive) filter; the REST API offers no such
// option, so audit dedupes the boundary by event ID instead. This is safe
// whether `since` is inclusive or exclusive: when exclusive, the boundary set
// simply never matches.
func (c *AuditCollector) dropAlreadyShipped(events []cloudflare.AuditLogEvent) []cloudflare.AuditLogEvent {
	if len(c.shippedAtCursor) == 0 {
		return events
	}

	filtered := events[:0]
	for i := range events {
		if _, dup := c.shippedAtCursor[events[i].ID]; dup {
			continue
		}
		filtered = append(filtered, events[i])
	}
	return filtered
}

// boundaryIDs returns the IDs of events whose timestamp equals the cursor
// boundary. Comparing the raw timestamp string avoids a re-parse and matches
// exactly what the API returned.
func boundaryIDs(events []cloudflare.AuditLogEvent, boundary string) map[string]struct{} {
	ids := make(map[string]struct{})
	for i := range events {
		if events[i].Action.Time == boundary {
			ids[events[i].ID] = struct{}{}
		}
	}
	return ids
}
