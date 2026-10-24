// -------------------------------------------------------------------------------
// HTTP Visits Collector Tests
//
// Author: Alex Freidah
//
// Covers the visits collector's wiring on the cursor-based poll: groups with
// visits ship and count per host with the cursor at the window end, groups
// without visits are dropped, the configured query is the one run, and a
// failed query leaves the cursor and counters alone.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
)

func TestVisitsPoll_ShipsCountsAndAdvances(t *testing.T) {
	metrics.HTTPVisitsTotal.Reset()
	cf := &fakeCloudflare{visits: []cloudflare.HTTPVisitsGroup{
		{Dimensions: cloudflare.HTTPVisitsDimensions{DatetimeMinute: "2026-03-13T10:00:00Z", ClientRequestHTTPHost: "docs.example.com"}, Sum: cloudflare.HTTPVisitsSum{Visits: 3}},
		{Dimensions: cloudflare.HTTPVisitsDimensions{DatetimeMinute: "2026-03-13T10:01:00Z", ClientRequestHTTPHost: "docs.example.com"}, Sum: cloudflare.HTTPVisitsSum{Visits: 2}},
		{Dimensions: cloudflare.HTTPVisitsDimensions{DatetimeMinute: "2026-03-13T10:01:00Z", ClientRequestHTTPHost: "api.example.com"}},
	}}
	push := &fakePusher{}
	query := cloudflare.NewHTTPVisitsQuery([]string{"Monitor%"})
	c := NewVisitsCollector(zoneTestConfig(cf, push), query)
	since := c.lastSeen

	c.poll(context.Background())

	if len(cf.visitsCalls) != 1 || cf.visitsCalls[0].scopeID != "zone1" || !cf.visitsCalls[0].since.Equal(since) {
		t.Fatalf("visits calls = %+v, want one for zone1 from the cursor %v", cf.visitsCalls, since)
	}
	if !reflect.DeepEqual(cf.visitsQueries[0], query) {
		t.Error("the collector did not run the configured visits query")
	}
	assertCounterValue(t, metrics.HTTPVisitsTotal.WithLabelValues("docs.example.com", "example.com"), 5)
	if len(push.entries) != 1 || len(push.entries[0]) != 2 {
		t.Fatalf("shipped = %+v, want the two groups with visits", push.entries)
	}
	if push.labels[0]["type"] != "http_visits" {
		t.Errorf("stream type = %q, want http_visits", push.labels[0]["type"])
	}
	if !c.lastSeen.Equal(cf.visitsCalls[0].until) {
		t.Errorf("cursor = %v, want the window end %v", c.lastSeen, cf.visitsCalls[0].until)
	}
}

func TestVisitsPoll_NoVisitsAdvancesWithoutShipping(t *testing.T) {
	cf := &fakeCloudflare{visits: []cloudflare.HTTPVisitsGroup{
		{Dimensions: cloudflare.HTTPVisitsDimensions{ClientRequestHTTPHost: "api.example.com"}},
	}}
	push := &fakePusher{}
	c := NewVisitsCollector(zoneTestConfig(cf, push), cloudflare.NewHTTPVisitsQuery(nil))

	c.poll(context.Background())

	if len(push.entries) != 0 {
		t.Errorf("shipped = %+v, want nothing for a window without visits", push.entries)
	}
	if !c.lastSeen.Equal(cf.visitsCalls[0].until) {
		t.Errorf("cursor = %v, want the window end %v", c.lastSeen, cf.visitsCalls[0].until)
	}
}

func TestVisitsPoll_FailureLeavesCursor(t *testing.T) {
	metrics.HTTPVisitsTotal.Reset()
	cf := &fakeCloudflare{
		visits: []cloudflare.HTTPVisitsGroup{{Dimensions: cloudflare.HTTPVisitsDimensions{ClientRequestHTTPHost: "docs.example.com"}, Sum: cloudflare.HTTPVisitsSum{Visits: 4}}},
		err:    errors.New("boom"),
	}
	c := NewVisitsCollector(zoneTestConfig(cf, &fakePusher{}), cloudflare.NewHTTPVisitsQuery(nil))
	since := c.lastSeen

	c.poll(context.Background())

	if !c.lastSeen.Equal(since) {
		t.Errorf("cursor = %v, want it left at %v", c.lastSeen, since)
	}
	assertCounterValue(t, metrics.HTTPVisitsTotal.WithLabelValues("docs.example.com", "example.com"), 0)
}
