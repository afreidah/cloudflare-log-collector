// -------------------------------------------------------------------------------
// Poll Cycle Tests
//
// Author: Alex Freidah
//
// Tests for the poll templates on their own. The cursor-based poll: lagged
// window, closed-window skip, empty windows per cursor advancer,
// ship-then-commit, failure paths, filtering. The trailing poll: a window
// ending now, replacing only its own scope's series, keeping gauges on a
// failed query, clearing them on an empty window. Plus one end-to-end poll
// through the real Cloudflare and Loki clients against httptest servers,
// proving the whole chain is wired together. Each collector's own wiring is
// tested beside it.
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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// -------------------------------------------------------------------------
// END TO END
// -------------------------------------------------------------------------

func TestFirewallPoll_EndToEndThroughRealClients(t *testing.T) {
	cfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"viewer": map[string]any{
			"zones": []any{map[string]any{"firewallEventsAdaptive": []cloudflare.FirewallEvent{
				{Action: "block", ClientIP: "1.2.3.4", Datetime: "2026-03-13T10:00:00Z"},
			}}},
		}}})
	}))
	t.Cleanup(cfServer.Close)

	var pushes int
	lokiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushes++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(lokiServer.Close)

	c := NewFirewallCollector(zoneTestConfig(
		cloudflare.NewTestClient(cfServer.URL, "test-token"),
		loki.NewClient(lokiServer.URL, "fake"),
	))
	c.poll(context.Background())

	if pushes != 1 {
		t.Errorf("got %d Loki pushes, want 1", pushes)
	}
	if want := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC); !c.lastSeen.Equal(want) {
		t.Errorf("cursor = %v, want the shipped event %v", c.lastSeen, want)
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

// -------------------------------------------------------------------------
// TRAILING POLL TEMPLATE
// -------------------------------------------------------------------------

// trailing is a pollTrailing fixture: a base with the given trailing window,
// an unregistered gauge, and a spec over cursorRow whose query returns
// rows/err and records the window it was asked for. set writes one series per
// row, keyed by the row's timestamp.
type trailing struct {
	b            base
	gauge        *prometheus.GaugeVec
	rows         []cursorRow
	err          error
	since, until time.Time
	setWith      []cursorRow
}

func newTrailing(window time.Duration) *trailing {
	tr := &trailing{gauge: testGauge()}
	common := testCommon(&fakePusher{})
	common.TrailingWindow = window
	tr.b = newBase(common, identity{name: "Test", stream: "test", scopeKey: "zone", scope: "example.com", record: "test row"})
	return tr
}

func (tr *trailing) spec() trailingPoll[cursorRow] {
	return trailingPoll[cursorRow]{
		dataset:   DatasetHTTPOriginLatency,
		span:      "test.trailing",
		title:     "Test quantiles",
		countAttr: "cflog.test_count",
		query: func(_ context.Context, since, until time.Time) ([]cursorRow, error) {
			tr.since, tr.until = since, until
			return tr.rows, tr.err
		},
		gauges: []*prometheus.GaugeVec{tr.gauge},
		set: func(rows []cursorRow) {
			tr.setWith = rows
			for _, r := range rows {
				tr.b.set(tr.gauge, 1, r.At)
			}
		},
	}
}

func TestPollTrailing_QueriesTheWindowEndingNow(t *testing.T) {
	tr := newTrailing(15 * time.Minute)

	before := time.Now().UTC()
	pollTrailing(context.Background(), &tr.b, tr.spec())
	after := time.Now().UTC()

	if tr.until.Before(before) || tr.until.After(after) {
		t.Errorf("until = %v, want now (no ingestion lag)", tr.until)
	}
	if got := tr.until.Sub(tr.since); got != 15*time.Minute {
		t.Errorf("window = %v, want the trailing window 15m", got)
	}
}

func TestPollTrailing_WindowFuncOverridesTrailingWindow(t *testing.T) {
	tr := newTrailing(15 * time.Minute)
	spec := tr.spec()
	spec.window = lastComplete(time.Hour)

	pollTrailing(context.Background(), &tr.b, spec)

	if got := tr.until.Sub(tr.since); got != time.Hour || !tr.until.Equal(tr.until.Truncate(time.Hour)) {
		t.Errorf("window = [%v, %v), want the last whole hour", tr.since, tr.until)
	}
}

func TestLastComplete_ReportsTheLastWholePeriod(t *testing.T) {
	now := time.Date(2026, 10, 21, 9, 42, 7, 0, time.UTC)
	tests := []struct {
		period       time.Duration
		since, until time.Time
	}{
		{time.Hour, time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC), time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)},
		{24 * time.Hour, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		since, until := lastComplete(tt.period)(now)
		if !since.Equal(tt.since) || !until.Equal(tt.until) {
			t.Errorf("lastComplete(%v) = [%v, %v), want [%v, %v)", tt.period, since, until, tt.since, tt.until)
		}
	}

	// On a boundary the period just ended is complete and is the one reported.
	since, _ := lastComplete(time.Hour)(time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC))
	if want := time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC); !since.Equal(want) {
		t.Errorf("at 09:00 since = %v, want %v", since, want)
	}
}

func TestPollTrailing_ReplacesThisScopesSeries(t *testing.T) {
	tr := newTrailing(time.Hour)
	tr.b.set(tr.gauge, 1, "stale")
	tr.gauge.WithLabelValues("kept", "other.example").Set(2)
	tr.rows = []cursorRow{{At: "fresh"}}

	pollTrailing(context.Background(), &tr.b, tr.spec())

	// The stale series is gone, the fresh one set, another scope untouched.
	if got := testutil.CollectAndCount(tr.gauge); got != 2 {
		t.Errorf("series = %d, want 2 (fresh + the other scope's)", got)
	}
	assertGaugeValue(t, tr.gauge.WithLabelValues("fresh", "example.com"), 1)
	assertGaugeValue(t, tr.gauge.WithLabelValues("kept", "other.example"), 2)
}

func TestPollTrailing_QueryErrorKeepsGauges(t *testing.T) {
	tr := newTrailing(time.Hour)
	tr.b.set(tr.gauge, 1, "last")
	tr.err = errors.New("boom")

	pollTrailing(context.Background(), &tr.b, tr.spec())

	if tr.setWith != nil {
		t.Error("set must not run after a failed query")
	}
	assertGaugeValue(t, tr.gauge.WithLabelValues("last", "example.com"), 1)
}

func TestPollTrailing_EmptyWindowClearsGauges(t *testing.T) {
	tr := newTrailing(time.Hour)
	tr.b.set(tr.gauge, 1, "gone")

	pollTrailing(context.Background(), &tr.b, tr.spec())

	// A successful empty window means no data now, not the last value.
	if got := testutil.CollectAndCount(tr.gauge); got != 0 {
		t.Errorf("series = %d, want 0 after an empty window", got)
	}
}
