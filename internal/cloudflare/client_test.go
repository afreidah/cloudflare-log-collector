// -------------------------------------------------------------------------------
// Cloudflare GraphQL Client Tests
//
// Authors: Alex Freidah, Aaron Florey
//
// Tests for the Cloudflare API client using httptest servers to mock API
// responses: the audit logs REST API (results, pagination, errors), the
// GraphQL transport's error handling, and retries under the client's retry
// policy. Every Analytics dataset query is covered in graphql_test.go.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/retry"
)

// -------------------------------------------------------------------------
// AUDIT LOGS
// -------------------------------------------------------------------------

func TestQueryAuditLogs_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAuthHeader(t, r)

		// Verify the request path and query params.
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
			// First page.
			_, _ = w.Write([]byte(`{
				"success": true,
				"result": [
					{"id": "audit-1", "action": {"type": "create"}},
					{"id": "audit-2", "action": {"type": "update"}}
				],
				"result_info": {"count": 2, "cursor": "next-page-cursor"}
			}`))
		case "next-page-cursor":
			// Second page.
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
// RETRY
// -------------------------------------------------------------------------

// The retry policy itself (retryable statuses, backoff, Retry-After) is shared
// and tested in internal/retry; these cover the client's use of it.

func TestDoQuery_RetriesRateLimitThenSucceeds(t *testing.T) {
	var requests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		writeJSON(t, w, datasetResponse("zones", "firewallEventsAdaptive", []FirewallEvent{{Action: "block"}}))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")
	client.retryPolicy = retry.Policy{MaxRetries: 3, BaseDelay: time.Millisecond}

	events, err := client.QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryFirewallEvents() error = %v, want success after one retry", err)
	}
	if len(events) != 1 {
		t.Errorf("got %d events, want 1", len(events))
	}
	if requests != 2 {
		t.Errorf("got %d requests, want 2 (one 429, one retry)", requests)
	}
}

func TestDoQuery_CancelDuringBackoffReturnsContextError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")
	client.retryPolicy = retry.Policy{MaxRetries: 3, BaseDelay: time.Hour}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	t.Cleanup(cancel)

	_, err := client.QueryFirewallEvents(ctx, "test-zone", time.Now().Add(-1*time.Hour), time.Now())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded from the cancelled backoff", err)
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

// datasetResponse wraps rows in the GraphQL envelope a dataset query returns:
// data.viewer.<scope>[0].<field> = rows, where scope is "zones" or "accounts".
func datasetResponse(scope, field string, rows any) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{
				scope: []any{
					map[string]any{field: rows},
				},
			},
		},
	}
}

// contains checks if s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
