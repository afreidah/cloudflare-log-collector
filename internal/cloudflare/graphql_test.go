// -------------------------------------------------------------------------------
// Cloudflare GraphQL Dataset Tests
//
// Author: Alex Freidah
//
// Covers every Analytics dataset query through the shared queryDataset, once:
// each sends its scope variables plus the window and its limit, decodes its
// rows exactly (catching JSON tag drift on any row type), and returns no rows
// when the response has no zone/account node. Also covers queryDataset's edge
// cases: a missing dataset field, malformed rows, and a full (possibly
// truncated) page.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

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

// datasetCase is one dataset query under test: the dataset it runs, the scope
// variables it must send, sample rows for the server to return, and a call
// that runs the query and returns its rows.
type datasetCase struct {
	name string
	ds   dataset
	vars map[string]any
	rows any
	call func(c *Client, since, until time.Time) (any, error)
}

func datasetCases() []datasetCase {
	ctx := context.Background()
	return []datasetCase{
		{
			name: "firewall",
			ds:   firewallDataset,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []FirewallEvent{{Action: "block", ClientIP: "1.2.3.4", ClientRequestHTTPHost: "example.com", Datetime: "2026-03-13T10:00:00Z", RayName: "abc123", RuleID: "r1", Source: "waf", ClientCountryName: "US"}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryFirewallEvents(ctx, "zone1", since, until)
			},
		},
		{
			name: "http",
			ds:   httpDataset,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []HTTPRequestGroup{{Count: 42, Dimensions: HTTPRequestDimensions{
				DatetimeMinute: "2026-03-13T10:00:00Z", ClientRequestHTTPHost: "example.com", ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus: 504, OriginResponseStatus: 0, CacheStatus: "miss", RequestSource: "eyeball", ClientCountryName: "US",
			}, Sum: HTTPRequestSum{EdgeResponseBytes: 1024}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryHTTPRequests(ctx, "zone1", since, until)
			},
		},
		{
			name: "rum pageloads",
			ds:   rumPageloadDataset,
			vars: map[string]any{"accountId": "acct1", "siteTag": "site1"},
			rows: []RUMPageloadGroup{{Count: 9, Dimensions: RUMPageloadDimensions{
				RequestPath: "/guides/", CountryName: "VN", DeviceType: "desktop", RefererHost: "ref.example",
			}, Sum: RUMPageloadSum{Visits: 1}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryRUMPageloads(ctx, "acct1", "site1", since, until)
			},
		},
		{
			name: "rum web vitals",
			ds:   rumWebVitalsDataset,
			vars: map[string]any{"accountId": "acct1", "siteTag": "site1"},
			rows: []RUMWebVitalsGroup{{Count: 12, Dimensions: RUMWebVitalsDimensions{DeviceType: "desktop"}, Quantiles: RUMWebVitalsQuantiles{
				LargestContentfulPaintP75: 1396000, InteractionToNextPaintP75: 48000, FirstInputDelayP75: -1,
				FirstContentfulPaintP75: 1200000, TimeToFirstByteP75: 591500, CumulativeLayoutShiftP75: 0.012,
			}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryRUMWebVitals(ctx, "acct1", "site1", since, until)
			},
		},
	}
}

func TestDatasetQueries_SendVariablesAndDecodeRows(t *testing.T) {
	since := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)

	for _, tc := range datasetCases() {
		t.Run(tc.name, func(t *testing.T) {
			var gotVars map[string]any
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				verifyAuthHeader(t, r)
				verifyContentType(t, r)
				gotVars = decodeQueryVariables(t, r)
				writeJSON(t, w, datasetResponse(tc.ds.scope, tc.ds.field, tc.rows))
			}))
			t.Cleanup(ts.Close)

			got, err := tc.call(newTestClient(ts.URL, "test-token"), since, until)
			if err != nil {
				t.Fatalf("query error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.rows) {
				t.Errorf("rows = %+v, want %+v", got, tc.rows)
			}

			want := map[string]any{"since": "2026-03-13T10:00:00Z", "until": "2026-03-13T11:00:00Z", "limit": float64(tc.ds.limit)}
			maps.Copy(want, tc.vars)
			if !reflect.DeepEqual(gotVars, want) {
				t.Errorf("variables = %v, want %v", gotVars, want)
			}
		})
	}
}

func TestDatasetQueries_NoScopeNodeReturnsNoRows(t *testing.T) {
	for _, tc := range datasetCases() {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{"data": map[string]any{"viewer": map[string]any{tc.ds.scope: []any{}}}})
			}))
			t.Cleanup(ts.Close)

			got, err := tc.call(newTestClient(ts.URL, "test-token"), time.Now().Add(-time.Hour), time.Now())
			if err != nil {
				t.Fatalf("query error = %v", err)
			}
			if n := reflect.ValueOf(got).Len(); n != 0 {
				t.Errorf("got %d rows, want 0 when the %s node is empty", n, tc.ds.scope)
			}
		})
	}
}

// -------------------------------------------------------------------------
// QUERY DATASET EDGE CASES
// -------------------------------------------------------------------------

func TestQueryDataset_MissingFieldReturnsNoRows(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A zone node with no dataset field (e.g. the dataset is not enabled).
		writeJSON(t, w, map[string]any{"data": map[string]any{"viewer": map[string]any{"zones": []any{map[string]any{}}}}})
	}))
	t.Cleanup(ts.Close)

	events, err := newTestClient(ts.URL, "test-token").QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryFirewallEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events, want 0 when the dataset field is absent", len(events))
	}
}

func TestQueryDataset_MalformedRowsIsAnError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, datasetResponse("zones", "firewallEventsAdaptive", "not-a-list"))
	}))
	t.Cleanup(ts.Close)

	_, err := newTestClient(ts.URL, "test-token").QueryFirewallEvents(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err == nil || !contains(err.Error(), "firewallEventsAdaptive response parse") {
		t.Errorf("err = %v, want a firewallEventsAdaptive response parse error", err)
	}
}

func TestQueryDataset_FullPageIsReturnedInFull(t *testing.T) {
	groups := make([]HTTPRequestGroup, httpDataset.limit)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, datasetResponse("zones", "httpRequestsAdaptiveGroups", groups))
	}))
	t.Cleanup(ts.Close)

	result, err := newTestClient(ts.URL, "test-token").QueryHTTPRequests(context.Background(), "test-zone",
		time.Now().Add(-1*time.Hour), time.Now())
	if err != nil {
		t.Fatalf("QueryHTTPRequests() error = %v", err)
	}

	// A full page may be truncated; it is still returned in full and only
	// flagged with a warning.
	if len(result) != httpDataset.limit {
		t.Errorf("got %d groups, want %d", len(result), httpDataset.limit)
	}
}
