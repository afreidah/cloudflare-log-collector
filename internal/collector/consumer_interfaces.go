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

// httpQuerier is the HTTP collector's view of the Cloudflare client: traffic
// for the counters, the two per-host latency distributions, and the hourly and
// daily unique-IP rollups.
type httpQuerier interface {
	QueryHTTPRequests(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPRequestGroup, error)
	QueryHTTPOriginLatency(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPLatencyGroup[cloudflare.OriginLatencyQuantiles], error)
	QueryHTTPEdgeLatency(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPLatencyGroup[cloudflare.EdgeLatencyQuantiles], error)
	QueryHTTPUniqueIPsHourly(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPUniqueIPsGroup, error)
	QueryHTTPUniqueIPsDaily(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPUniqueIPsGroup, error)
}

// visitsQuerier is the visits collector's view of the Cloudflare client.
type visitsQuerier interface {
	QueryHTTPVisits(ctx context.Context, q cloudflare.HTTPVisitsQuery, zoneID string, since, until time.Time) ([]cloudflare.HTTPVisitsGroup, error)
}

// dnsQuerier is the DNS collector's view of the Cloudflare client: query
// groups for the counters, and the zone's processing time distribution.
type dnsQuerier interface {
	QueryDNSQueries(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.DNSQueryGroup, error)
	QueryDNSProcessingTime(ctx context.Context, zoneID string, since, until time.Time) ([]cloudflare.DNSProcessingGroup, error)
}

// auditQuerier is the audit collector's view of the Cloudflare client.
type auditQuerier interface {
	QueryAuditLogs(ctx context.Context, accountID string, since, before time.Time) ([]cloudflare.AuditLogEvent, error)
}

// rumQuerier is the RUM collector's view of the Cloudflare client. The RUM
// collector polls both account-scoped Web Analytics datasets in one cycle, so
// it declares both query methods.
type rumQuerier interface {
	QueryRUMPageloads(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]cloudflare.RUMPageloadGroup, error)
	QueryRUMWebVitals(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]cloudflare.RUMWebVitalsGroup, error)
}

// cloudflareQuerier composes the zone-scoped queriers for the shared
// ZoneCollectorConfig that the firewall, HTTP, visits, and DNS collectors all
// build from. Each collector narrows it to the query methods it actually calls.
type cloudflareQuerier interface {
	firewallQuerier
	httpQuerier
	visitsQuerier
	dnsQuerier
}

// -------------------------------------------------------------------------
// LOKI
// -------------------------------------------------------------------------

// logPusher is the collectors' view of the Loki push client.
type logPusher interface {
	Push(ctx context.Context, labels map[string]string, entries []loki.Entry) error
}
