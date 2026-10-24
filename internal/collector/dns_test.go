// -------------------------------------------------------------------------------
// DNS Analytics Collector Tests
//
// Author: Alex Freidah
//
// Covers the DNS collector's wiring on the shared poll templates: query groups
// ship and count by type and response code with the cursor at the window end,
// processing time quantiles become seconds, and the two polls are independent.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"errors"
	"testing"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
)

func TestDNSPoll_ShipsCountsAndAdvances(t *testing.T) {
	metrics.DNSQueriesTotal.Reset()
	cf := &fakeCloudflare{dnsGroups: []cloudflare.DNSQueryGroup{
		{Count: 15, Dimensions: cloudflare.DNSQueryDimensions{QueryName: "woodpecker.example.com", QueryType: "A", ResponseCode: "NXDOMAIN"}},
		{Count: 4, Dimensions: cloudflare.DNSQueryDimensions{QueryName: "other.example.com", QueryType: "A", ResponseCode: "NXDOMAIN"}},
	}}
	push := &fakePusher{}
	c := NewDNSCollector(zoneTestConfig(cf, push))
	since := c.lastSeen

	c.poll(context.Background())

	if len(cf.dnsCalls) != 1 || cf.dnsCalls[0].scopeID != "zone1" || !cf.dnsCalls[0].since.Equal(since) {
		t.Fatalf("dns calls = %+v, want one for zone1 from the cursor %v", cf.dnsCalls, since)
	}
	// Names collapse out of the counter; they stay in the Loki lines.
	assertCounterValue(t, metrics.DNSQueriesTotal.WithLabelValues("A", "NXDOMAIN", "example.com"), 19)
	if got := decodeLine[cloudflare.DNSQueryGroup](t, push.entries[0][0].Line); got.Dimensions.QueryName != "woodpecker.example.com" {
		t.Errorf("shipped group = %+v, want the query name in Loki", got)
	}
	if push.labels[0]["type"] != "dns" {
		t.Errorf("stream type = %q, want dns", push.labels[0]["type"])
	}
	if !c.lastSeen.Equal(cf.dnsCalls[0].until) {
		t.Errorf("cursor = %v, want the window end %v", c.lastSeen, cf.dnsCalls[0].until)
	}
}

func TestDNSPoll_ProcessingTimeInSeconds(t *testing.T) {
	metrics.DNSProcessingSeconds.Reset()
	cf := &fakeCloudflare{dnsProcessing: []cloudflare.DNSProcessingGroup{{
		Quantiles: cloudflare.DNSProcessingQuantiles{ProcessingTimeUsP50: 2763, ProcessingTimeUsP95: 6156, ProcessingTimeUsP99: 9146},
	}}}
	c := NewDNSCollector(zoneTestConfig(cf, &fakePusher{}))

	c.poll(context.Background())

	assertGaugeValue(t, metrics.DNSProcessingSeconds.WithLabelValues("0.5", "example.com"), 0.002763)
	assertGaugeValue(t, metrics.DNSProcessingSeconds.WithLabelValues("0.99", "example.com"), 0.009146)
}

func TestDNSPoll_ProcessingFailureLeavesQueriesAlone(t *testing.T) {
	metrics.DNSQueriesTotal.Reset()
	cf := &fakeCloudflare{
		dnsGroups:  []cloudflare.DNSQueryGroup{{Count: 3, Dimensions: cloudflare.DNSQueryDimensions{QueryType: "AAAA", ResponseCode: "NOERROR"}}},
		latencyErr: errors.New("boom"),
	}
	c := NewDNSCollector(zoneTestConfig(cf, &fakePusher{}))

	c.poll(context.Background())

	assertCounterValue(t, metrics.DNSQueriesTotal.WithLabelValues("AAAA", "NOERROR", "example.com"), 3)
}
