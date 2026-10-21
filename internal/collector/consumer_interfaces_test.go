// -------------------------------------------------------------------------------
// Collector Consumer Interface Tests
//
// Author: Alex Freidah
//
// Compile-time assertions that the concrete *cloudflare.Client and *loki.Client
// still satisfy the narrow consumer-declared interfaces, so a signature drift in
// either client breaks the build here rather than at the call site. Also holds
// the in-memory fakes the collector tests drive through those interfaces; the
// fakes record the arguments each query received so tests can check a
// collector's wiring without an httptest server.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
)

var (
	_ cloudflareQuerier = (*cloudflare.Client)(nil)
	_ firewallQuerier   = (*cloudflare.Client)(nil)
	_ httpQuerier       = (*cloudflare.Client)(nil)
	_ auditQuerier      = (*cloudflare.Client)(nil)
	_ rumQuerier        = (*cloudflare.Client)(nil)
	_ logPusher         = (*loki.Client)(nil)
)

// -------------------------------------------------------------------------
// FAKES
// -------------------------------------------------------------------------

// queryCall records the arguments of one query: the scope ID it was asked for
// (zone or account) and the window (since, until].
type queryCall struct {
	scopeID string
	since   time.Time
	until   time.Time
}

// fakeCloudflare implements cloudflareQuerier and auditQuerier with canned
// responses and a shared error, recording every call's arguments per dataset.
// latencyErr fails only the two latency queries, so a test can tell the HTTP
// collector's polls apart.
type fakeCloudflare struct {
	firewallEvents []cloudflare.FirewallEvent
	httpGroups     []cloudflare.HTTPRequestGroup
	originLatency  []cloudflare.HTTPLatencyGroup[cloudflare.OriginLatencyQuantiles]
	edgeLatency    []cloudflare.HTTPLatencyGroup[cloudflare.EdgeLatencyQuantiles]
	auditEvents    []cloudflare.AuditLogEvent
	err            error
	latencyErr     error
	firewallCalls  []queryCall
	httpCalls      []queryCall
	originCalls    []queryCall
	edgeCalls      []queryCall
	auditCalls     []queryCall
}

func (f *fakeCloudflare) QueryFirewallEvents(_ context.Context, zoneID string, since, until time.Time) ([]cloudflare.FirewallEvent, error) {
	f.firewallCalls = append(f.firewallCalls, queryCall{zoneID, since, until})
	return f.firewallEvents, f.err
}

func (f *fakeCloudflare) QueryHTTPRequests(_ context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPRequestGroup, error) {
	f.httpCalls = append(f.httpCalls, queryCall{zoneID, since, until})
	return f.httpGroups, f.err
}

func (f *fakeCloudflare) QueryHTTPOriginLatency(_ context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPLatencyGroup[cloudflare.OriginLatencyQuantiles], error) {
	f.originCalls = append(f.originCalls, queryCall{zoneID, since, until})
	return f.originLatency, f.latencyErr
}

func (f *fakeCloudflare) QueryHTTPEdgeLatency(_ context.Context, zoneID string, since, until time.Time) ([]cloudflare.HTTPLatencyGroup[cloudflare.EdgeLatencyQuantiles], error) {
	f.edgeCalls = append(f.edgeCalls, queryCall{zoneID, since, until})
	return f.edgeLatency, f.latencyErr
}

func (f *fakeCloudflare) QueryAuditLogs(_ context.Context, accountID string, since, before time.Time) ([]cloudflare.AuditLogEvent, error) {
	f.auditCalls = append(f.auditCalls, queryCall{accountID, since, before})
	return f.auditEvents, f.err
}

// fakePusher implements logPusher, capturing the labels and entries of every
// push so tests can assert what would have been shipped to Loki.
type fakePusher struct {
	labels  []map[string]string
	entries [][]loki.Entry
	err     error
}

func (f *fakePusher) Push(_ context.Context, labels map[string]string, entries []loki.Entry) error {
	f.labels = append(f.labels, labels)
	f.entries = append(f.entries, entries)
	return f.err
}
