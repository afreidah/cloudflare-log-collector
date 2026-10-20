// -------------------------------------------------------------------------------
// Poll Cycle Tests
//
// Author: Alex Freidah
//
// Tests for the poll cycle: the cursor-based poll template on its own (lagged
// window, closed-window skip, empty windows per cursor advancer, ship-then-commit,
// failure paths, filtering), and the firewall and HTTP collectors driving it
// end to end against httptest servers for both Cloudflare and Loki.
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

// -------------------------------------------------------------------------
// CURSOR-BASED POLL TEMPLATE
// -------------------------------------------------------------------------

// templatePoll is a pollCursor fixture: a base shipping to push with the given
// ingestion lag, and a spec over cursorRow whose query returns rows/err and
// records the window it was asked for. counted collects the rows passed to
// count, and advancedWith the rows passed to advanced.
type templatePoll struct {
	b            base
	push         *fakePusher
	cursor       time.Time
	rows         []cursorRow
	err          error
	queries      int
	since, until time.Time
	counted      []cursorRow
	advancedWith []cursorRow
}

func newTemplatePoll(lag time.Duration) *templatePoll {
	tp := &templatePoll{push: &fakePusher{}}
	common := testCommon(tp.push)
	common.IngestionLag = lag
	tp.b = newBase(common, identity{name: "Test", stream: "test", scopeKey: "zone", scope: "example.com", record: "test row"})
	tp.cursor = time.Now().UTC().Add(-time.Hour)
	return tp
}

func (tp *templatePoll) spec(advancer cursorAdvancer[cursorRow]) cursorPoll[cursorRow] {
	return cursorPoll[cursorRow]{
		dataset:   DatasetFirewall,
		span:      "test.poll",
		title:     "Test rows",
		countAttr: "cflog.test_count",
		cursor:    &tp.cursor,
		advancer:  advancer,
		query: func(_ context.Context, since, until time.Time) ([]cursorRow, error) {
			tp.queries++
			tp.since, tp.until = since, until
			return tp.rows, tp.err
		},
		count:    func(rows []cursorRow) { tp.counted = append(tp.counted, rows...) },
		advanced: func(rows []cursorRow) { tp.advancedWith = rows },
	}
}

func TestPollCursor_QueriesUpToNowMinusLag(t *testing.T) {
	tp := newTemplatePoll(3 * time.Minute)
	since := tp.cursor

	before := time.Now().UTC()
	pollCursor(context.Background(), &tp.b, tp.spec(windowEnd[cursorRow]{}))
	after := time.Now().UTC()

	if !tp.since.Equal(since) {
		t.Errorf("since = %v, want the cursor %v", tp.since, since)
	}
	if tp.until.Before(before.Add(-3*time.Minute)) || tp.until.After(after.Add(-3*time.Minute)) {
		t.Errorf("until = %v, want now - 3m", tp.until)
	}
}

func TestPollCursor_ClosedWindowSkipsWithoutQuerying(t *testing.T) {
	tp := newTemplatePoll(3 * time.Minute)
	tp.cursor = time.Now().UTC().Add(-time.Minute) // inside the lag
	cursor := tp.cursor

	pollCursor(context.Background(), &tp.b, tp.spec(windowEnd[cursorRow]{}))

	if tp.queries != 0 {
		t.Errorf("queries = %d, want 0 for a window not yet ingested", tp.queries)
	}
	if !tp.cursor.Equal(cursor) {
		t.Errorf("cursor moved to %v, want it to stay at %v", tp.cursor, cursor)
	}
}

func TestPollCursor_EmptyWindowEndAdvancesEventStays(t *testing.T) {
	window := newTemplatePoll(0)
	pollCursor(context.Background(), &window.b, window.spec(windowEnd[cursorRow]{}))
	if !window.cursor.Equal(window.until) {
		t.Errorf("windowEnd cursor = %v, want the window end %v", window.cursor, window.until)
	}

	event := newTemplatePoll(0)
	cursor := event.cursor
	pollCursor(context.Background(), &event.b, event.spec(lastEvent[cursorRow]{timestamp: cursorRowTime}))
	if !event.cursor.Equal(cursor) {
		t.Errorf("lastEvent cursor moved to %v on an empty window, want %v", event.cursor, cursor)
	}

	if len(window.push.entries)+len(event.push.entries) != 0 {
		t.Error("an empty window must not push to Loki")
	}
}

func TestPollCursor_ShipsCountsAndAdvances(t *testing.T) {
	tp := newTemplatePoll(0)
	tp.rows = []cursorRow{{At: "2026-10-19T11:00:00Z"}, {At: "2026-10-19T11:05:00Z"}}

	pollCursor(context.Background(), &tp.b, tp.spec(lastEvent[cursorRow]{timestamp: cursorRowTime}))

	if len(tp.push.entries) != 1 || len(tp.push.entries[0]) != 2 {
		t.Fatalf("pushes = %v, want one push of 2 rows", tp.push.entries)
	}
	if len(tp.counted) != 2 {
		t.Errorf("counted %d rows, want 2", len(tp.counted))
	}
	if want := time.Date(2026, 10, 19, 11, 5, 0, 0, time.UTC); !tp.cursor.Equal(want) {
		t.Errorf("cursor = %v, want the last row %v", tp.cursor, want)
	}
	if len(tp.advancedWith) != 2 {
		t.Errorf("advanced ran with %d rows, want 2", len(tp.advancedWith))
	}
}

func TestPollCursor_QueryErrorLeavesEverythingAlone(t *testing.T) {
	tp := newTemplatePoll(0)
	tp.err = errors.New("boom")
	cursor := tp.cursor

	pollCursor(context.Background(), &tp.b, tp.spec(windowEnd[cursorRow]{}))

	if !tp.cursor.Equal(cursor) || len(tp.push.entries) != 0 || len(tp.counted) != 0 {
		t.Errorf("query error must not move the cursor, ship, or count (cursor %v, pushes %d, counted %d)",
			tp.cursor, len(tp.push.entries), len(tp.counted))
	}
}

func TestPollCursor_PushErrorDoesNotCountOrAdvance(t *testing.T) {
	tp := newTemplatePoll(0)
	tp.push.err = errors.New("loki down")
	tp.rows = []cursorRow{{At: "2026-10-19T11:00:00Z"}}
	cursor := tp.cursor

	pollCursor(context.Background(), &tp.b, tp.spec(lastEvent[cursorRow]{timestamp: cursorRowTime}))

	if !tp.cursor.Equal(cursor) || len(tp.counted) != 0 || tp.advancedWith != nil {
		t.Errorf("failed push must not count, advance, or run advanced (cursor %v, counted %d)",
			tp.cursor, len(tp.counted))
	}
}

func TestPollCursor_FilterRunsBeforeShipAndCount(t *testing.T) {
	tp := newTemplatePoll(0)
	tp.rows = []cursorRow{{At: "2026-10-19T11:00:00Z"}, {At: "2026-10-19T11:05:00Z"}}
	spec := tp.spec(lastEvent[cursorRow]{timestamp: cursorRowTime})
	spec.filter = func(rows []cursorRow) []cursorRow { return rows[:1] }

	pollCursor(context.Background(), &tp.b, spec)

	if len(tp.counted) != 1 || len(tp.push.entries[0]) != 1 {
		t.Errorf("filtered rows: counted %d, shipped %d; want 1 and 1", len(tp.counted), len(tp.push.entries[0]))
	}
	if want := time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC); !tp.cursor.Equal(want) {
		t.Errorf("cursor = %v, want the last kept row %v", tp.cursor, want)
	}
}
