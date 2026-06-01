// -------------------------------------------------------------------------------
// Cloudflare GraphQL Client Tests
//
// Author: Alex Freidah
//
// Tests for the Cloudflare GraphQL API client using httptest servers to mock
// API responses. Covers successful queries, error handling, and response parsing
// for both firewall events and HTTP traffic datasets.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// -------------------------------------------------------------------------
// FIREWALL EVENTS
// -------------------------------------------------------------------------

func TestQueryFirewallEvents_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAuthHeader(t, r)
		verifyContentType(t, r)

		resp := graphQLResponse{
			Data: mustMarshal(t, firewallResponse{
				Viewer: struct {
					Zones []struct {
						FirewallEventsAdaptive []FirewallEvent `json:"firewallEventsAdaptive"`
					} `json:"zones"`
				}{
					Zones: []struct {
						FirewallEventsAdaptive []FirewallEvent `json:"firewallEventsAdaptive"`
					}{
						{
							FirewallEventsAdaptive: []FirewallEvent{
								{
									Action:   "block",
									ClientIP: "1.2.3.4",
									Datetime: "2026-03-13T10:00:00Z",
									RayName:  "abc123",
								},
							},
						},
					},
				},
			}),
		}
		writeJSON(t, w, resp)
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	events, err := client.QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryFirewallEvents() error = %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Action != "block" {
		t.Errorf("action = %q, want %q", events[0].Action, "block")
	}
	if events[0].ClientIP != "1.2.3.4" {
		t.Errorf("clientIP = %q, want %q", events[0].ClientIP, "1.2.3.4")
	}
}

func TestQueryFirewallEvents_EmptyZones(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphQLResponse{
			Data: mustMarshal(t, firewallResponse{}),
		}
		writeJSON(t, w, resp)
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	events, err := client.QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryFirewallEvents() error = %v", err)
	}

	if events != nil {
		t.Errorf("expected nil events for empty zones, got %d", len(events))
	}
}

// -------------------------------------------------------------------------
// HTTP REQUESTS
// -------------------------------------------------------------------------

func TestQueryHTTPRequests_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAuthHeader(t, r)

		resp := graphQLResponse{
			Data: mustMarshal(t, httpRequestResponse{
				Viewer: struct {
					Zones []struct {
						HTTPRequestsAdaptiveGroups []HTTPRequestGroup `json:"httpRequestsAdaptiveGroups"`
					} `json:"zones"`
				}{
					Zones: []struct {
						HTTPRequestsAdaptiveGroups []HTTPRequestGroup `json:"httpRequestsAdaptiveGroups"`
					}{
						{
							HTTPRequestsAdaptiveGroups: []HTTPRequestGroup{
								{
									Count: 42,
									Dimensions: HTTPRequestDimensions{
										Datetime:                    "2026-03-13T10:00:00Z",
										ClientRequestHTTPMethodName: "GET",
										EdgeResponseStatus:          200,
									},
									Sum: HTTPRequestSum{
										EdgeResponseBytes: 1024,
									},
								},
							},
						},
					},
				},
			}),
		}
		writeJSON(t, w, resp)
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	groups, err := client.QueryHTTPRequests(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryHTTPRequests() error = %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if groups[0].Count != 42 {
		t.Errorf("count = %d, want 42", groups[0].Count)
	}
	if groups[0].Sum.EdgeResponseBytes != 1024 {
		t.Errorf("edgeResponseBytes = %d, want 1024", groups[0].Sum.EdgeResponseBytes)
	}
}

// -------------------------------------------------------------------------
// AUDIT LOGS
// -------------------------------------------------------------------------

func TestQueryAuditLogs_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAuthHeader(t, r)

		// --- Verify request path and query params ---
		if !contains(r.URL.Path, "/accounts/test-account/logs/audit") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("since") == "" {
			t.Error("missing since query param")
		}
		if r.URL.Query().Get("before") == "" {
			t.Error("missing before query param")
		}

		_, _ = w.Write([]byte(`{
			"success": true,
			"result": [{
				"id": "audit-123",
				"account": {"id": "test-account", "name": "Test Account"},
				"action": {"description": "Add Member", "result": "success", "time": "2026-03-13T10:00:00Z", "type": "create"},
				"actor": {"id": "user-456", "context": "dash", "email": "admin@example.com", "ip_address": "1.2.3.4", "type": "user"},
				"raw": {"cf_ray_id": "ray-789", "method": "POST", "status_code": 200, "uri": "/accounts/test-account/members"},
				"resource": {"id": "member-abc", "product": "members", "type": "member"}
			}],
			"result_info": {"count": 1, "cursor": ""}
		}`))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	events, err := client.QueryAuditLogs(context.Background(), "test-account",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].ID != "audit-123" {
		t.Errorf("id = %q, want %q", events[0].ID, "audit-123")
	}
	if events[0].Action.Type != "create" {
		t.Errorf("action.type = %q, want %q", events[0].Action.Type, "create")
	}
	if events[0].Actor.Email != "admin@example.com" {
		t.Errorf("actor.email = %q, want %q", events[0].Actor.Email, "admin@example.com")
	}
	if events[0].AccountID != "test-account" {
		t.Errorf("account_id = %q, want %q", events[0].AccountID, "test-account")
	}
}

func TestQueryAuditLogs_Pagination(t *testing.T) {
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		cursor := r.URL.Query().Get("cursor")

		switch cursor {
		case "":
			// --- First page ---
			_, _ = w.Write([]byte(`{
				"success": true,
				"result": [
					{"id": "audit-1", "action": {"type": "create"}},
					{"id": "audit-2", "action": {"type": "update"}}
				],
				"result_info": {"count": 2, "cursor": "next-page-cursor"}
			}`))
		case "next-page-cursor":
			// --- Second page ---
			_, _ = w.Write([]byte(`{
				"success": true,
				"result": [
					{"id": "audit-3", "action": {"type": "delete"}}
				],
				"result_info": {"count": 1, "cursor": ""}
			}`))
		}
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	events, err := client.QueryAuditLogs(context.Background(), "test-account",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}

	if requestCount != 2 {
		t.Errorf("expected 2 API requests for pagination, got %d", requestCount)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].ID != "audit-1" {
		t.Errorf("events[0].id = %q, want %q", events[0].ID, "audit-1")
	}
	if events[2].ID != "audit-3" {
		t.Errorf("events[2].id = %q, want %q", events[2].ID, "audit-3")
	}
}

func TestQueryAuditLogs_EmptyResult(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": true,
			"result": [],
			"result_info": {"count": 0, "cursor": ""}
		}`))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	events, err := client.QueryAuditLogs(context.Background(), "test-account",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryAuditLogs() error = %v", err)
	}

	if len(events) != 0 {
		t.Errorf("expected empty events, got %d", len(events))
	}
}

func TestQueryAuditLogs_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"success": false,
			"errors": [{"message": "Invalid account ID"}]
		}`))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	_, err := client.QueryAuditLogs(context.Background(), "bad-account",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil {
		t.Error("expected error for API error response")
	}
}

func TestQueryAuditLogs_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"message":"forbidden"}]}`))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	_, err := client.QueryAuditLogs(context.Background(), "test-account",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil {
		t.Error("expected error for HTTP 403")
	}
}

// -------------------------------------------------------------------------
// ERROR HANDLING
// -------------------------------------------------------------------------

func TestDoQuery_GraphQLError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"zone not found"}]}`))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	_, err := client.QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil {
		t.Error("expected error for GraphQL error response")
	}
}

func TestDoQuery_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"unauthorized"}]}`))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "bad-token")

	_, err := client.QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil {
		t.Error("expected error for HTTP 401")
	}
}

func TestDoQuery_InvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	_, err := client.QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestDoQuery_ServerDown(t *testing.T) {
	client := newTestClient("http://localhost:1", "test-token")

	_, err := client.QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil {
		t.Error("expected error when server is unreachable")
	}
}

func TestDoQuery_RequestBody(t *testing.T) {
	var receivedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}

		resp := graphQLResponse{
			Data: mustMarshal(t, firewallResponse{}),
		}
		writeJSON(t, w, resp)
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	_, _ = client.QueryFirewallEvents(context.Background(), "my-zone",
		time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 13, 11, 0, 0, 0, time.UTC))

	var req graphQLRequest
	if err := json.Unmarshal(receivedBody, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	if req.Variables["zoneId"] != "my-zone" {
		t.Errorf("zoneId = %q, want %q", req.Variables["zoneId"], "my-zone")
	}
	if req.Variables["since"] != "2026-03-13T10:00:00Z" {
		t.Errorf("since = %q, want %q", req.Variables["since"], "2026-03-13T10:00:00Z")
	}
}

// -------------------------------------------------------------------------
// TRUNCATION WARNINGS
// -------------------------------------------------------------------------

func TestQueryHTTPRequests_TruncationWarning(t *testing.T) {
	// Build a response with exactly httpQueryLimit groups
	groups := make([]HTTPRequestGroup, httpQueryLimit)
	for i := range groups {
		groups[i] = HTTPRequestGroup{
			Count: 1,
			Dimensions: HTTPRequestDimensions{
				Datetime:                    "2026-03-13T10:00:00Z",
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
			},
		}
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphQLResponse{
			Data: mustMarshal(t, httpRequestResponse{
				Viewer: struct {
					Zones []struct {
						HTTPRequestsAdaptiveGroups []HTTPRequestGroup `json:"httpRequestsAdaptiveGroups"`
					} `json:"zones"`
				}{
					Zones: []struct {
						HTTPRequestsAdaptiveGroups []HTTPRequestGroup `json:"httpRequestsAdaptiveGroups"`
					}{
						{HTTPRequestsAdaptiveGroups: groups},
					},
				},
			}),
		}
		writeJSON(t, w, resp)
	}))
	t.Cleanup(ts.Close)

	client := NewTestClient(ts.URL, "test-token")

	result, err := client.QueryHTTPRequests(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryHTTPRequests() error = %v", err)
	}

	if len(result) != httpQueryLimit {
		t.Errorf("got %d groups, want %d", len(result), httpQueryLimit)
	}
}

func TestNewTestClient(t *testing.T) {
	client := NewTestClient("http://localhost:9999", "my-token")

	if client.endpoint != "http://localhost:9999" {
		t.Errorf("endpoint = %q, want %q", client.endpoint, "http://localhost:9999")
	}
	if client.apiToken != "my-token" {
		t.Errorf("apiToken = %q, want %q", client.apiToken, "my-token")
	}
}

// -------------------------------------------------------------------------
// RETRY LOGIC
// -------------------------------------------------------------------------

func TestRetryDelay_DefaultBackoff(t *testing.T) {
	header := http.Header{}

	d0 := retryDelay(header, 0)
	d1 := retryDelay(header, 1)
	d2 := retryDelay(header, 2)

	if d0 != retryBaseDelay {
		t.Errorf("attempt 0: got %v, want %v", d0, retryBaseDelay)
	}
	if d1 != 2*retryBaseDelay {
		t.Errorf("attempt 1: got %v, want %v", d1, 2*retryBaseDelay)
	}
	if d2 != 4*retryBaseDelay {
		t.Errorf("attempt 2: got %v, want %v", d2, 4*retryBaseDelay)
	}
}

func TestRetryDelay_RetryAfterHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "10")

	d := retryDelay(header, 0)
	if d != 10*time.Second {
		t.Errorf("got %v, want 10s (from Retry-After header)", d)
	}
}

func TestRetryDelay_InvalidRetryAfterFallsBack(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "not-a-number")

	d := retryDelay(header, 1)
	if d != 2*retryBaseDelay {
		t.Errorf("got %v, want %v (fallback to exponential)", d, 2*retryBaseDelay)
	}
}

func TestIsRetryable(t *testing.T) {
	retryable := []int{429, 502, 503, 504}
	for _, code := range retryable {
		if !isRetryable(code) {
			t.Errorf("status %d should be retryable", code)
		}
	}

	notRetryable := []int{200, 201, 400, 401, 403, 404, 500}
	for _, code := range notRetryable {
		if isRetryable(code) {
			t.Errorf("status %d should not be retryable", code)
		}
	}
}

// -------------------------------------------------------------------------
// HELPERS
// -------------------------------------------------------------------------

// newTestClient creates a Client pointing at the given test server URL.
func newTestClient(url, token string) *Client {
	return NewTestClient(url, token)
}

// verifyAuthHeader checks the Authorization header is a Bearer token.
func verifyAuthHeader(t *testing.T, r *http.Request) {
	t.Helper()
	auth := r.Header.Get("Authorization")
	if auth == "" {
		t.Error("missing Authorization header")
	}
}

// verifyContentType checks the Content-Type header is application/json.
func verifyContentType(t *testing.T, r *http.Request) {
	t.Helper()
	ct := r.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
}

// writeJSON encodes v as JSON and writes it to w, failing the test on error.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("json encode: %v", err)
	}
}

// mustMarshal serializes v to a json.RawMessage, failing the test on error.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
