// -------------------------------------------------------------------------------
// Audit Log Collector
//
// Author: Aaron Florey
//
// Polls the Cloudflare Account Audit Logs REST API on a configurable interval
// and ships events to Loki as structured JSON log lines. Its seek cursor follows
// the last event shipped (see cursor.go), and the inclusive `since` boundary is
// deduped by event ID so no event ships twice across polls. Each cycle runs
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
// Loki. The base's scope is the account name. shippedAtCursor holds the IDs of
// events already shipped at the lastSeen boundary, so they can be dropped if
// the API returns them again; see dropAlreadyShipped for why audit needs this
// and firewall does not.
type AuditCollector struct {
	base
	cf              auditQuerier
	accountID       string
	lastSeen        time.Time
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

// poll runs one audit log poll cycle. The cursor moves to the last event
// shipped, and the IDs sharing that timestamp are recorded so the next poll
// can drop them if the API returns them again.
func (c *AuditCollector) poll(ctx context.Context) {
	pollCursor(ctx, &c.base, cursorPoll[cloudflare.AuditLogEvent]{
		dataset:   DatasetAudit,
		span:      "audit.poll",
		title:     "Audit events",
		countAttr: telemetry.AttrEventCount,
		cursor:    &c.lastSeen,
		advancer:  lastEvent[cloudflare.AuditLogEvent]{timestamp: auditEventTime},
		query: func(ctx context.Context, since, before time.Time) ([]cloudflare.AuditLogEvent, error) {
			return c.cf.QueryAuditLogs(ctx, c.accountID, since, before)
		},
		filter: c.dropAlreadyShipped,
		count: func(events []cloudflare.AuditLogEvent) {
			for i := range events {
				metrics.AuditEventsTotal.WithLabelValues(events[i].Action.Type, c.scope).Inc()
			}
		},
		advanced: func(events []cloudflare.AuditLogEvent) {
			c.shippedAtCursor = boundaryIDs(events, auditEventTime(events[len(events)-1]))
		},
	})
}

// auditEventTime is an audit event's timestamp, which the cursor follows.
func auditEventTime(e cloudflare.AuditLogEvent) string {
	return e.Action.Time
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
