// -------------------------------------------------------------------------------
// Collector Consumer Interface Tests
//
// Author: Alex Freidah
//
// Two guarantees for the consumer-declared seam. First, compile-time assertions
// that the concrete *cloudflare.Client and *loki.Client still satisfy the narrow
// interfaces, so a signature drift in either client breaks the build here rather
// than at the call site. Second, runtime tests driving each collector's poll
// through lightweight in-memory fakes — exercising the seam the interfaces exist
// to create, with no httptest server in sight.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
)

// --- Compile-time assertions: the real clients satisfy the narrow interfaces ---

var (
	_ cloudflareQuerier = (*cloudflare.Client)(nil)
	_ firewallQuerier   = (*cloudflare.Client)(nil)
	_ httpQuerier       = (*cloudflare.Client)(nil)
	_ auditQuerier      = (*cloudflare.Client)(nil)
	_ logPusher         = (*loki.Client)(nil)
)

// -------------------------------------------------------------------------
// FAKES
// -------------------------------------------------------------------------

// fakeCloudflare implements cloudflareQuerier and auditQuerier with canned
// responses, recording how many times each query method was called.
type fakeCloudflare struct {
	firewallEvents []cloudflare.FirewallEvent
	httpGroups     []cloudflare.HTTPRequestGroup
	auditEvents    []cloudflare.AuditLogEvent
	err            error

	firewallCalls int
	httpCalls     int
	auditCalls    int
}

func (f *fakeCloudflare) QueryFirewallEvents(_ context.Context, _ string, _, _ time.Time) ([]cloudflare.FirewallEvent, error) {
	f.firewallCalls++
	return f.firewallEvents, f.err
}

func (f *fakeCloudflare) QueryHTTPRequests(_ context.Context, _ string, _, _ time.Time) ([]cloudflare.HTTPRequestGroup, error) {
	f.httpCalls++
	return f.httpGroups, f.err
}

func (f *fakeCloudflare) QueryAuditLogs(_ context.Context, _ string, _, _ time.Time) ([]cloudflare.AuditLogEvent, error) {
	f.auditCalls++
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

// -------------------------------------------------------------------------
// FIREWALL SEAM
// -------------------------------------------------------------------------

func TestFirewallPoll_ViaFakeQuerier(t *testing.T) {
	cf := &fakeCloudflare{
		firewallEvents: []cloudflare.FirewallEvent{
			{Action: "block", ClientIP: "1.2.3.4", Datetime: "2026-03-14T12:00:00Z"},
		},
	}
	push := &fakePusher{}

	c := NewFirewallCollector(CollectorConfig{
		CF:             cf,
		Loki:           push,
		ZoneID:         "zone1",
		ZoneName:       "example.com",
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      100,
	})
	c.poll(context.Background())

	if cf.firewallCalls != 1 {
		t.Errorf("firewall querier called %d times, want 1", cf.firewallCalls)
	}
	if len(push.labels) != 1 {
		t.Fatalf("got %d Loki pushes, want 1", len(push.labels))
	}
	if got := push.labels[0]["type"]; got != DatasetFirewall.String() {
		t.Errorf("stream type = %q, want %q", got, DatasetFirewall.String())
	}
	if push.labels[0]["zone"] != "example.com" {
		t.Errorf("stream zone = %q, want %q", push.labels[0]["zone"], "example.com")
	}
}

func TestFirewallPoll_ViaFakeQuerier_Error(t *testing.T) {
	cf := &fakeCloudflare{err: errors.New("boom")}
	push := &fakePusher{}

	c := NewFirewallCollector(CollectorConfig{
		CF:             cf,
		Loki:           push,
		ZoneID:         "zone1",
		ZoneName:       "example.com",
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      100,
	})
	c.poll(context.Background())

	if cf.firewallCalls != 1 {
		t.Errorf("firewall querier called %d times, want 1", cf.firewallCalls)
	}
	if len(push.labels) != 0 {
		t.Errorf("got %d Loki pushes, want 0 on query error", len(push.labels))
	}
}

// -------------------------------------------------------------------------
// HTTP SEAM
// -------------------------------------------------------------------------

func TestHTTPPoll_ViaFakeQuerier(t *testing.T) {
	cf := &fakeCloudflare{
		httpGroups: []cloudflare.HTTPRequestGroup{
			{
				Count: 7,
				Dimensions: cloudflare.HTTPRequestDimensions{
					Datetime:                    "2026-03-14T12:00:00Z",
					ClientRequestHTTPMethodName: "GET",
					EdgeResponseStatus:          200,
				},
			},
		},
	}
	push := &fakePusher{}

	c := NewHTTPCollector(CollectorConfig{
		CF:             cf,
		Loki:           push,
		ZoneID:         "zone1",
		ZoneName:       "example.com",
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      100,
	})
	c.poll(context.Background())

	if cf.httpCalls != 1 {
		t.Errorf("http querier called %d times, want 1", cf.httpCalls)
	}
	if len(push.labels) != 1 {
		t.Fatalf("got %d Loki pushes, want 1", len(push.labels))
	}
	// The HTTP collector ships under the "http_traffic" stream label, distinct
	// from the DatasetHTTP value used for metrics and span attributes.
	if got := push.labels[0]["type"]; got != "http_traffic" {
		t.Errorf("stream type = %q, want %q", got, "http_traffic")
	}
}

// -------------------------------------------------------------------------
// AUDIT SEAM
// -------------------------------------------------------------------------

func TestAuditPoll_ViaFakeQuerier(t *testing.T) {
	cf := &fakeCloudflare{
		auditEvents: []cloudflare.AuditLogEvent{
			{ID: "evt1", Action: cloudflare.AuditAction{Time: "2026-03-14T12:00:00Z", Type: "login"}},
		},
	}
	push := &fakePusher{}

	c := NewAuditCollector(AuditCollectorConfig{
		CF:             cf,
		Loki:           push,
		AccountID:      "acct1",
		AccountName:    "acme",
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      100,
	})
	c.poll(context.Background())

	if cf.auditCalls != 1 {
		t.Errorf("audit querier called %d times, want 1", cf.auditCalls)
	}
	if len(push.labels) != 1 {
		t.Fatalf("got %d Loki pushes, want 1", len(push.labels))
	}
	if got := push.labels[0]["type"]; got != DatasetAudit.String() {
		t.Errorf("stream type = %q, want %q", got, DatasetAudit.String())
	}
	if push.labels[0]["account"] != "acme" {
		t.Errorf("stream account = %q, want %q", push.labels[0]["account"], "acme")
	}
}
