// -------------------------------------------------------------------------------
// Audit Log Collector Tests
//
// Author: Aaron Florey
//
// Covers what is audit's own on top of the shared cursor-based poll: its spec
// wiring (account ID, window, counters by action type, cursor to the last
// event) and the boundary dedupe that keeps an event re-returned at the
// inclusive `since` boundary from shipping twice.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
)

// auditEvent builds an audit event with the given ID, action type, and time.
func auditEvent(id, actionType, at string) cloudflare.AuditLogEvent {
	return cloudflare.AuditLogEvent{ID: id, Action: cloudflare.AuditAction{Type: actionType, Time: at}}
}

// shippedAuditIDs returns the IDs of every audit event pushed to Loki.
func shippedAuditIDs(t *testing.T, push *fakePusher) []string {
	t.Helper()
	var ids []string
	for _, batch := range push.entries {
		for _, e := range batch {
			ids = append(ids, decodeLine[cloudflare.AuditLogEvent](t, e.Line).ID)
		}
	}
	return ids
}

func TestAuditPoll_Wiring(t *testing.T) {
	metrics.AuditEventsTotal.Reset()
	cf := &fakeCloudflare{auditEvents: []cloudflare.AuditLogEvent{
		auditEvent("e1", "login", "2026-03-13T10:00:00Z"),
		auditEvent("e2", "settings.modify", "2026-03-13T10:00:05Z"),
	}}
	c := NewAuditCollector(auditTestConfig(cf, &fakePusher{}))
	since := c.lastSeen

	c.poll(context.Background())

	if len(cf.auditCalls) != 1 || cf.auditCalls[0].scopeID != "acct1" || !cf.auditCalls[0].since.Equal(since) {
		t.Fatalf("audit calls = %+v, want one for acct1 from the cursor %v", cf.auditCalls, since)
	}
	assertCounterValue(t, metrics.AuditEventsTotal.WithLabelValues("login", "acme"), 1)
	assertCounterValue(t, metrics.AuditEventsTotal.WithLabelValues("settings.modify", "acme"), 1)
	if want := time.Date(2026, 3, 13, 10, 0, 5, 0, time.UTC); !c.lastSeen.Equal(want) {
		t.Errorf("cursor = %v, want the last event %v", c.lastSeen, want)
	}
}

func TestAuditPoll_DedupesCursorBoundary(t *testing.T) {
	cf := &fakeCloudflare{}
	push := &fakePusher{}
	c := NewAuditCollector(auditTestConfig(cf, push))

	// First poll returns e1 and e2; the second re-returns e2 at the inclusive
	// `since` boundary alongside e3.
	cf.auditEvents = []cloudflare.AuditLogEvent{
		auditEvent("e1", "settings.modify", "2026-03-13T10:00:00Z"),
		auditEvent("e2", "settings.modify", "2026-03-13T10:00:05Z"),
	}
	c.poll(context.Background())
	cf.auditEvents = []cloudflare.AuditLogEvent{
		auditEvent("e2", "settings.modify", "2026-03-13T10:00:05Z"),
		auditEvent("e3", "settings.modify", "2026-03-13T10:00:09Z"),
	}
	c.poll(context.Background())

	if got, want := shippedAuditIDs(t, push), []string{"e1", "e2", "e3"}; !slices.Equal(got, want) {
		t.Errorf("shipped = %v, want %v (boundary event e2 must not ship twice)", got, want)
	}
}
