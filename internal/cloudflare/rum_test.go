// -------------------------------------------------------------------------------
// Cloudflare RUM / Web Analytics Client Tests
//
// Author: Alex Freidah
//
// Tests for the account-scoped RUM query methods using httptest servers. Covers
// response parsing for both RUM datasets, the empty-accounts case, GraphQL
// errors, and that the account/site variables are sent in the request.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// rumDataResponse wraps the given account-node payload in the GraphQL data
// envelope the client expects.
func rumDataResponse(dataset string, groups any) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{
				"accounts": []any{
					map[string]any{dataset: groups},
				},
			},
		},
	}
}

// -------------------------------------------------------------------------
// PAGE LOADS
// -------------------------------------------------------------------------

func TestQueryRUMPageloads_Success(t *testing.T) {
	var gotVars map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAuthHeader(t, r)
		verifyContentType(t, r)
		gotVars = decodeQueryVariables(t, r)

		groups := []RUMPageloadGroup{
			{
				Count: 9,
				Dimensions: RUMPageloadDimensions{
					RequestPath: "/guides/", CountryName: "VN", DeviceType: "desktop", RefererHost: "ref.example",
				},
				Sum: RUMPageloadSum{Visits: 1},
			},
		}
		writeJSON(t, w, rumDataResponse("rumPageloadEventsAdaptiveGroups", groups))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	groups, err := client.QueryRUMPageloads(context.Background(), "acct1", "site1",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryRUMPageloads() error = %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if groups[0].Count != 9 {
		t.Errorf("count = %d, want 9", groups[0].Count)
	}
	if groups[0].Sum.Visits != 1 {
		t.Errorf("visits = %d, want 1", groups[0].Sum.Visits)
	}
	if groups[0].Dimensions.RequestPath != "/guides/" {
		t.Errorf("requestPath = %q, want %q", groups[0].Dimensions.RequestPath, "/guides/")
	}

	// --- The account and site identifiers must be sent as query variables ---
	if gotVars["accountId"] != "acct1" {
		t.Errorf("accountId variable = %v, want acct1", gotVars["accountId"])
	}
	if gotVars["siteTag"] != "site1" {
		t.Errorf("siteTag variable = %v, want site1", gotVars["siteTag"])
	}
}

func TestQueryRUMPageloads_NoAccounts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"data": map[string]any{"viewer": map[string]any{"accounts": []any{}}}})
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	groups, err := client.QueryRUMPageloads(context.Background(), "acct1", "site1",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryRUMPageloads() error = %v", err)
	}
	if groups != nil {
		t.Errorf("got %v, want nil for empty accounts", groups)
	}
}

func TestQueryRUMPageloads_GraphQLError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data":   nil,
			"errors": []map[string]any{{"message": "not authorized for that account"}},
		})
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	_, err := client.QueryRUMPageloads(context.Background(), "acct1", "site1",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil {
		t.Fatal("expected error from GraphQL errors payload")
	}
}

// -------------------------------------------------------------------------
// WEB VITALS
// -------------------------------------------------------------------------

func TestQueryRUMWebVitals_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyAuthHeader(t, r)

		groups := []RUMWebVitalsGroup{
			{
				Count:      12,
				Dimensions: RUMWebVitalsDimensions{DeviceType: "desktop"},
				Quantiles: RUMWebVitalsQuantiles{
					LargestContentfulPaintP75: 1396000,
					FirstInputDelayP75:        -1,
					CumulativeLayoutShiftP75:  0.012,
				},
			},
		}
		writeJSON(t, w, rumDataResponse("rumWebVitalsEventsAdaptiveGroups", groups))
	}))
	t.Cleanup(ts.Close)

	client := newTestClient(ts.URL, "test-token")

	groups, err := client.QueryRUMWebVitals(context.Background(), "acct1", "site1",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryRUMWebVitals() error = %v", err)
	}

	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if groups[0].Dimensions.DeviceType != "desktop" {
		t.Errorf("deviceType = %q, want desktop", groups[0].Dimensions.DeviceType)
	}
	if groups[0].Quantiles.LargestContentfulPaintP75 != 1396000 {
		t.Errorf("lcp p75 = %v, want 1396000", groups[0].Quantiles.LargestContentfulPaintP75)
	}
	if groups[0].Quantiles.FirstInputDelayP75 != -1 {
		t.Errorf("fid p75 = %v, want -1 (no-data sentinel preserved)", groups[0].Quantiles.FirstInputDelayP75)
	}
	if groups[0].Quantiles.CumulativeLayoutShiftP75 != 0.012 {
		t.Errorf("cls p75 = %v, want 0.012", groups[0].Quantiles.CumulativeLayoutShiftP75)
	}
}

// decodeQueryVariables reads the GraphQL request body and returns its variables.
func decodeQueryVariables(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var req struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req.Variables
}
