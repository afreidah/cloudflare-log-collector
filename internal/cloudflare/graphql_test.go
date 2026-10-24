// -------------------------------------------------------------------------------
// Cloudflare GraphQL Dataset Tests
//
// Author: Alex Freidah
//
// Covers every Analytics dataset query through the shared queryDataset, once:
// each sends its generated document with its scope variables plus the window
// and its limit, decodes its rows exactly (catching JSON tag drift on any row
// type), and returns no rows when the response has no zone/account node. Also covers queryDataset's edge
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

// decodeRequest reads the GraphQL request body: the query sent and its
// variables.
func decodeRequest(t *testing.T, r *http.Request) graphQLRequest {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	var req graphQLRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	return req
}

// datasetCase is one dataset query under test: the built dataset it runs, the
// scope variables it must send, sample rows for the server to return, and a
// call that runs the query and returns its rows. window is the since and until
// it must send; unset, the RFC3339 window bounds.
type datasetCase struct {
	name   string
	info   datasetInfo
	vars   map[string]any
	window []string
	rows   any
	call   func(c *Client, since, until time.Time) (any, error)
}

func datasetCases() []datasetCase {
	ctx := context.Background()
	return []datasetCase{
		{
			name: "firewall",
			info: firewallDataset.datasetInfo,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []FirewallEvent{{Action: "block", ClientIP: "1.2.3.4", ClientRequestHTTPHost: "example.com", Datetime: "2026-03-13T10:00:00Z", RayName: "abc123", RuleID: "r1", Source: "waf", ClientCountryName: "US"}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryFirewallEvents(ctx, "zone1", since, until)
			},
		},
		{
			name: "http",
			info: httpDataset.datasetInfo,
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
			name: "http origin latency",
			info: httpOriginLatencyDataset.datasetInfo,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []HTTPLatencyGroup[OriginLatencyQuantiles]{{Count: 40, Dimensions: HTTPLatencyDimensions{ClientRequestHTTPHost: "g3.munchbox.cc"},
				Quantiles: OriginLatencyQuantiles{OriginResponseDurationMsP50: 14, OriginResponseDurationMsP95: 23, OriginResponseDurationMsP99: 26}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryHTTPOriginLatency(ctx, "zone1", since, until)
			},
		},
		{
			name: "http edge latency",
			info: httpEdgeLatencyDataset.datasetInfo,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []HTTPLatencyGroup[EdgeLatencyQuantiles]{{Count: 157, Dimensions: HTTPLatencyDimensions{ClientRequestHTTPHost: "b2-proxy.munchbox.cc"},
				Quantiles: EdgeLatencyQuantiles{EdgeTimeToFirstByteMsP50: 271, EdgeTimeToFirstByteMsP95: 882, EdgeTimeToFirstByteMsP99: 1769}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryHTTPEdgeLatency(ctx, "zone1", since, until)
			},
		},
		{
			name: "http unique ips hourly",
			info: httpUniqueIPsHourlyDataset.datasetInfo,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []HTTPUniqueIPsGroup{{Uniq: HTTPUniq{Uniques: 11}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryHTTPUniqueIPsHourly(ctx, "zone1", since, until)
			},
		},
		{
			name:   "http unique ips daily",
			info:   httpUniqueIPsDailyDataset.datasetInfo,
			vars:   map[string]any{"zoneId": "zone1"},
			window: []string{"2026-03-13", "2026-03-13"},
			rows:   []HTTPUniqueIPsGroup{{Uniq: HTTPUniq{Uniques: 583}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryHTTPUniqueIPsDaily(ctx, "zone1", since, until)
			},
		},
		{
			name: "dns queries",
			info: dnsQueryDataset.datasetInfo,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []DNSQueryGroup{{Count: 15, Dimensions: DNSQueryDimensions{
				DatetimeMinute: "2026-03-13T10:00:00Z", QueryName: "woodpecker.munchbox.cc", QueryType: "A",
				ResponseCode: "NXDOMAIN", ResponseCached: 1, ColoName: "LAX",
			}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryDNSQueries(ctx, "zone1", since, until)
			},
		},
		{
			name: "dns processing",
			info: dnsProcessingDataset.datasetInfo,
			vars: map[string]any{"zoneId": "zone1"},
			rows: []DNSProcessingGroup{{Count: 53, Quantiles: DNSProcessingQuantiles{
				ProcessingTimeUsP50: 2763, ProcessingTimeUsP95: 6156, ProcessingTimeUsP99: 9146,
			}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryDNSProcessingTime(ctx, "zone1", since, until)
			},
		},
		{
			name: "rum pageloads",
			info: rumPageloadDataset.datasetInfo,
			vars: map[string]any{"accountId": "acct1", "siteTag": "site1"},
			rows: []RUMPageloadGroup{{Count: 9, Dimensions: RUMPageloadDimensions{
				DatetimeMinute: "2026-03-13T10:00:00Z", RequestHost: "vagabond.munchbox.cc", RequestPath: "/guides/",
				CountryName: "VN", DeviceType: "desktop", RefererHost: "ref.example", Bot: 1,
			}, Sum: RUMPageloadSum{Visits: 1}}},
			call: func(c *Client, since, until time.Time) (any, error) {
				return c.QueryRUMPageloads(ctx, "acct1", "site1", since, until)
			},
		},
		{
			name: "rum web vitals",
			info: rumWebVitalsDataset.datasetInfo,
			vars: map[string]any{"accountId": "acct1", "siteTag": "site1"},
			rows: []RUMWebVitalsGroup{{Count: 12, Dimensions: RUMWebVitalsDimensions{RequestHost: "vagabond.munchbox.cc", DeviceType: "desktop"}, Quantiles: RUMWebVitalsQuantiles{
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
			var got graphQLRequest
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				verifyAuthHeader(t, r)
				verifyContentType(t, r)
				got = decodeRequest(t, r)
				writeJSON(t, w, datasetResponse(tc.info.node, tc.info.field, tc.rows))
			}))
			t.Cleanup(ts.Close)

			rows, err := tc.call(newTestClient(ts.URL, "test-token"), since, until)
			if err != nil {
				t.Fatalf("query error = %v", err)
			}
			if !reflect.DeepEqual(rows, tc.rows) {
				t.Errorf("rows = %+v, want %+v", rows, tc.rows)
			}

			if got.Query != tc.info.query {
				t.Errorf("sent query is not the dataset's generated document:\n%s", got.Query)
			}
			window := tc.window
			if window == nil {
				window = []string{"2026-03-13T10:00:00Z", "2026-03-13T11:00:00Z"}
			}
			want := map[string]any{"since": window[0], "until": window[1], "limit": float64(tc.info.limit)}
			maps.Copy(want, tc.vars)
			if !reflect.DeepEqual(got.Variables, want) {
				t.Errorf("variables = %v, want %v", got.Variables, want)
			}
		})
	}
}

func TestDatasetQueries_NoScopeNodeReturnsNoRows(t *testing.T) {
	for _, tc := range datasetCases() {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, map[string]any{"data": map[string]any{"viewer": map[string]any{tc.info.node: []any{}}}})
			}))
			t.Cleanup(ts.Close)

			got, err := tc.call(newTestClient(ts.URL, "test-token"), time.Now().Add(-time.Hour), time.Now())
			if err != nil {
				t.Fatalf("query error = %v", err)
			}
			if n := reflect.ValueOf(got).Len(); n != 0 {
				t.Errorf("got %d rows, want 0 when the %s node is empty", n, tc.info.node)
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
