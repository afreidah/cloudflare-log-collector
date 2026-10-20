// -------------------------------------------------------------------------------
// Poll Cycle Tests
//
// Author: Alex Freidah
//
// Tests for the firewall and HTTP collector poll orchestration. Verifies the
// full poll cycle: Cloudflare API query, Loki shipping, metric updates, and
// cursor advancement. Uses httptest servers for both Cloudflare and Loki.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
)

// -------------------------------------------------------------------------
// HELPERS
// -------------------------------------------------------------------------

// mockCFServer returns an httptest server that serves a canned GraphQL response.
func mockCFServer(t *testing.T, response any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}

// mockLokiServer returns an httptest server that accepts Loki pushes and
// tracks the number of requests received.
func mockLokiServer(t *testing.T, requestCount *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
}

// pollTestConfig builds the zone config the poll tests share: the real
// Cloudflare and Loki clients pointed at the given test servers.
func pollTestConfig(cfURL, lokiURL string) ZoneCollectorConfig {
	return ZoneCollectorConfig{
		CommonConfig: testCommon(loki.NewClient(lokiURL, "fake")),
		CF:           cloudflare.NewTestClient(cfURL, "test-token"),
		ZoneID:       "zone1",
		ZoneName:     "example.com",
	}
}

// cfFirewallResponse builds a GraphQL response containing the given events.
func cfFirewallResponse(events []cloudflare.FirewallEvent) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{
				"zones": []map[string]any{
					{"firewallEventsAdaptive": events},
				},
			},
		},
	}
}

// cfHTTPResponse builds a GraphQL response containing the given groups.
func cfHTTPResponse(groups []cloudflare.HTTPRequestGroup) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{
				"zones": []map[string]any{
					{"httpRequestsAdaptiveGroups": groups},
				},
			},
		},
	}
}

// -------------------------------------------------------------------------
// RUN (INITIAL POLL + CANCEL)
// -------------------------------------------------------------------------

func TestFirewallRun_InitialPollThenCancel(t *testing.T) {
	events := []cloudflare.FirewallEvent{
		{Action: "block", Datetime: time.Now().UTC().Format(time.RFC3339)},
	}

	cfServer := mockCFServer(t, cfFirewallResponse(events))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)
	cfg.PollInterval = time.Hour // only the initial poll runs before cancel

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- NewFirewallCollector(cfg).Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if lokiRequests != 1 {
		t.Errorf("got %d Loki requests, want 1 (initial poll)", lokiRequests)
	}
}

func TestHTTPRun_InitialPollThenCancel(t *testing.T) {
	groups := []cloudflare.HTTPRequestGroup{
		{
			Count: 10,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    time.Now().UTC().Format(time.RFC3339),
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
			},
		},
	}

	cfServer := mockCFServer(t, cfHTTPResponse(groups))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)
	cfg.PollInterval = time.Hour // only the initial poll runs before cancel

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- NewHTTPCollector(cfg).Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if lokiRequests != 1 {
		t.Errorf("got %d Loki requests, want 1 (initial poll)", lokiRequests)
	}
}

// -------------------------------------------------------------------------
// FIREWALL POLL
// -------------------------------------------------------------------------

func TestFirewallPoll_Success(t *testing.T) {
	events := []cloudflare.FirewallEvent{
		{
			Action:            "block",
			ClientIP:          "1.2.3.4",
			Datetime:          time.Now().UTC().Format(time.RFC3339),
			RayName:           "abc123",
			ClientCountryName: "US",
		},
	}

	cfServer := mockCFServer(t, cfFirewallResponse(events))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewFirewallCollector(cfg)
	c.poll(context.Background())

	if lokiRequests != 1 {
		t.Errorf("got %d Loki requests, want 1", lokiRequests)
	}
}

func TestFirewallPoll_NoEvents(t *testing.T) {
	cfServer := mockCFServer(t, cfFirewallResponse(nil))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewFirewallCollector(cfg)
	c.poll(context.Background())

	if lokiRequests != 0 {
		t.Errorf("got %d Loki requests, want 0 (no events)", lokiRequests)
	}
}

func TestFirewallPoll_CFError(t *testing.T) {
	cfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewFirewallCollector(cfg)
	c.poll(context.Background())

	if lokiRequests != 0 {
		t.Errorf("got %d Loki requests, want 0 (CF error should skip Loki)", lokiRequests)
	}
}

func TestFirewallPoll_AdvancesCursor(t *testing.T) {
	eventTime := "2026-03-14T12:00:00Z"
	events := []cloudflare.FirewallEvent{
		{Action: "block", Datetime: eventTime},
	}

	cfServer := mockCFServer(t, cfFirewallResponse(events))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewFirewallCollector(cfg)
	before := c.lastSeen
	c.poll(context.Background())

	expected, _ := time.Parse(time.RFC3339Nano, eventTime)
	if !c.lastSeen.Equal(expected) {
		t.Errorf("lastSeen = %v, want %v (was %v)", c.lastSeen, expected, before)
	}
}

// -------------------------------------------------------------------------
// HTTP POLL
// -------------------------------------------------------------------------

func TestHTTPPoll_Success(t *testing.T) {
	groups := []cloudflare.HTTPRequestGroup{
		{
			Count: 42,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    time.Now().UTC().Format(time.RFC3339),
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
				ClientCountryName:           "US",
			},
			Sum: cloudflare.HTTPRequestSum{
				EdgeResponseBytes: 1024,
			},
		},
	}

	cfServer := mockCFServer(t, cfHTTPResponse(groups))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewHTTPCollector(cfg)
	c.poll(context.Background())

	if lokiRequests != 1 {
		t.Errorf("got %d Loki requests, want 1", lokiRequests)
	}
}

func TestHTTPPoll_NoGroups(t *testing.T) {
	cfServer := mockCFServer(t, cfHTTPResponse(nil))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewHTTPCollector(cfg)
	before := c.lastSeen
	c.poll(context.Background())

	if lokiRequests != 0 {
		t.Errorf("got %d Loki requests, want 0 (no groups)", lokiRequests)
	}

	// lastSeen should still advance on empty results
	if !c.lastSeen.After(before) {
		t.Error("lastSeen should advance even with no groups")
	}
}

func TestHTTPPoll_CFError(t *testing.T) {
	cfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(cfServer.Close)

	var lokiRequests int
	lokiServer := mockLokiServer(t, &lokiRequests)
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewHTTPCollector(cfg)
	c.poll(context.Background())

	if lokiRequests != 0 {
		t.Errorf("got %d Loki requests, want 0 (CF error should skip Loki)", lokiRequests)
	}
}

func TestHTTPPoll_LokiError_DoesNotAdvanceCursor(t *testing.T) {
	metrics.HTTPRequestsTotal.Reset()
	metrics.HTTPBytesTotal.Reset()

	groups := []cloudflare.HTTPRequestGroup{
		{
			Count: 10,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    time.Now().UTC().Format(time.RFC3339),
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
			},
		},
	}

	cfServer := mockCFServer(t, cfHTTPResponse(groups))
	t.Cleanup(cfServer.Close)

	lokiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("loki error"))
	}))
	t.Cleanup(lokiServer.Close)

	cfg := pollTestConfig(cfServer.URL, lokiServer.URL)

	c := NewHTTPCollector(cfg)
	before := c.lastSeen
	c.poll(context.Background())

	if c.lastSeen != before {
		t.Error("lastSeen should not advance when Loki push fails")
	}

	// The window was not shipped, so it must not be counted either.
	assertCounterValue(t, metrics.HTTPRequestsTotal.WithLabelValues("GET", "200", "", "example.com"), 0)
	assertCounterValue(t, metrics.HTTPBytesTotal.WithLabelValues("edge", "example.com"), 0)
}

// TestHTTPPoll_LokiRecovers_CountsWindowOnce is the regression test for the
// double count: a window whose push fails is re-queried on the next poll, and
// its counts must land exactly once when that retry succeeds.
func TestHTTPPoll_LokiRecovers_CountsWindowOnce(t *testing.T) {
	metrics.HTTPRequestsTotal.Reset()
	metrics.HTTPBytesTotal.Reset()

	cf := &fakeCloudflare{
		httpGroups: []cloudflare.HTTPRequestGroup{
			{
				Count: 10,
				Dimensions: cloudflare.HTTPRequestDimensions{
					ClientRequestHTTPMethodName: "GET",
					EdgeResponseStatus:          200,
					ClientCountryName:           "US",
				},
				Sum: cloudflare.HTTPRequestSum{EdgeResponseBytes: 5000},
			},
		},
	}
	push := &fakePusher{err: errors.New("loki down")}

	c := NewHTTPCollector(ZoneCollectorConfig{
		CommonConfig: testCommon(push),
		CF:           cf,
		ZoneID:       "zone1",
		ZoneName:     "example.com",
	})

	// First poll: Loki rejects the push, so nothing is counted.
	c.poll(context.Background())
	assertCounterValue(t, metrics.HTTPRequestsTotal.WithLabelValues("GET", "200", "US", "example.com"), 0)

	// Second poll: Loki is back and the same window comes round again.
	push.err = nil
	c.poll(context.Background())

	assertCounterValue(t, metrics.HTTPRequestsTotal.WithLabelValues("GET", "200", "US", "example.com"), 10)
	assertCounterValue(t, metrics.HTTPBytesTotal.WithLabelValues("edge", "example.com"), 5000)
	if cf.httpCalls != 2 {
		t.Errorf("http querier called %d times, want 2", cf.httpCalls)
	}
}
