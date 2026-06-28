// -------------------------------------------------------------------------------
// Collector Consumer Interfaces
//
// Author: Alex Freidah
//
// Narrow, consumer-declared interfaces naming exactly the methods each collector
// calls on the Cloudflare API client and the Loki push client. The concrete
// *cloudflare.Client and *loki.Client satisfy these implicitly, so main.go wires
// the real clients while tests can substitute lightweight fakes at the seam. See
// the Interface Design section of docs/style-guide.md for the rationale.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
)

// -------------------------------------------------------------------------
// CLOUDFLARE
// -------------------------------------------------------------------------

// firewallQuerier is the firewall collector's view of the Cloudflare client.
type firewallQuerier interface {
	QueryFirewallEvents(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.FirewallEvent, error)
}

// httpQuerier is the HTTP collector's view of the Cloudflare client.
type httpQuerier interface {
	QueryHTTPRequests(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPRequestGroup, error)
}

// auditQuerier is the audit collector's view of the Cloudflare client.
type auditQuerier interface {
	QueryAuditLogs(ctx context.Context, accountID string, since, before time.Time) ([]cloudflare.AuditLogEvent, error)
}

// cloudflareQuerier composes the two zone-scoped queriers for the shared
// CollectorConfig that the firewall and HTTP collectors both build from. Each
// collector narrows it to the single query method it actually calls.
type cloudflareQuerier interface {
	firewallQuerier
	httpQuerier
}

// -------------------------------------------------------------------------
// LOKI
// -------------------------------------------------------------------------

// logPusher is the collectors' view of the Loki push client.
type logPusher interface {
	Push(ctx context.Context, labels map[string]string, entries []loki.Entry) error
}
