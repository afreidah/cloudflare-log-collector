// -------------------------------------------------------------------------------
// Firewall Event Collector Tests
//
// Author: Alex Freidah
//
// Covers the firewall collector's spec wiring on top of the shared cursor-based
// poll: the zone and window it queries, events counted by action, the cursor
// following the last event, and events shipped as firewall JSON. The poll
// cycle itself is covered once in poll_test.go.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
)

func TestFirewallPoll_Wiring(t *testing.T) {
	metrics.FirewallEventsTotal.Reset()
	cf := &fakeCloudflare{firewallEvents: []cloudflare.FirewallEvent{
		{Action: "block", ClientIP: "1.2.3.4", Datetime: "2026-03-13T10:00:00Z"},
		{Action: "block", ClientIP: "5.6.7.8", Datetime: "2026-03-13T10:00:05Z"},
		{Action: "managed_challenge", ClientIP: "9.9.9.9", Datetime: "2026-03-13T10:00:09Z"},
	}}
	push := &fakePusher{}
	c := NewFirewallCollector(zoneTestConfig(cf, push))
	since := c.lastSeen

	c.poll(context.Background())

	if len(cf.firewallCalls) != 1 || cf.firewallCalls[0].scopeID != "zone1" || !cf.firewallCalls[0].since.Equal(since) {
		t.Fatalf("firewall calls = %+v, want one for zone1 from the cursor %v", cf.firewallCalls, since)
	}
	assertCounterValue(t, metrics.FirewallEventsTotal.WithLabelValues("block", "example.com"), 2)
	assertCounterValue(t, metrics.FirewallEventsTotal.WithLabelValues("managed_challenge", "example.com"), 1)
	if want := time.Date(2026, 3, 13, 10, 0, 9, 0, time.UTC); !c.lastSeen.Equal(want) {
		t.Errorf("cursor = %v, want the last event %v", c.lastSeen, want)
	}
	if got := decodeLine[cloudflare.FirewallEvent](t, push.entries[0][1].Line); got.ClientIP != "5.6.7.8" {
		t.Errorf("shipped event = %+v, want the second firewall event", got)
	}
}
