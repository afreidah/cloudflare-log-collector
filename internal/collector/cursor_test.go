// -------------------------------------------------------------------------------
// Collector Cursor Tests
//
// Author: Alex Freidah
//
// Covers the two cursor advancers: windowEnd always moves to the window's end,
// empty or not; lastEvent moves to the last row's timestamp, stays put on an
// empty window, and stays put (with a warning) on an unparseable timestamp.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"testing"
	"time"
)

// cursorRow is a minimal row with a timestamp, for exercising cursor advancers
// and the cursor-based poll without a real dataset.
type cursorRow struct {
	At string `json:"at"`
}

// cursorRowTime is cursorRow's timestamp, for lastEvent.
func cursorRowTime(r cursorRow) string {
	return r.At
}

func TestWindowEnd_AdvancesToWindowEndEvenWhenEmpty(t *testing.T) {
	b := testBase(time.Minute)
	until := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

	for _, rows := range [][]cursorRow{nil, {{At: "2026-10-19T11:00:00Z"}}} {
		next, ok := windowEnd[cursorRow]{}.advance(context.Background(), &b, rows, until)
		if !ok || !next.Equal(until) {
			t.Errorf("rows=%v: next = %v, %v; want %v, true", rows, next, ok, until)
		}
	}
}

func TestLastEvent_AdvancesToLastRow(t *testing.T) {
	b := testBase(time.Minute)
	advancer := lastEvent[cursorRow]{timestamp: cursorRowTime}
	rows := []cursorRow{{At: "2026-10-19T11:00:00Z"}, {At: "2026-10-19T11:30:00.5Z"}}

	next, ok := advancer.advance(context.Background(), &b, rows, time.Now())
	if !ok {
		t.Fatal("expected the cursor to advance")
	}
	if want := time.Date(2026, 10, 19, 11, 30, 0, 500_000_000, time.UTC); !next.Equal(want) {
		t.Errorf("next = %v, want %v (the last row, not the window end)", next, want)
	}
}

func TestLastEvent_EmptyWindowStaysPut(t *testing.T) {
	b := testBase(time.Minute)

	if _, ok := (lastEvent[cursorRow]{timestamp: cursorRowTime}).advance(context.Background(), &b, nil, time.Now()); ok {
		t.Error("an empty window must not move an event cursor")
	}
}

func TestLastEvent_UnparseableTimestampStaysPut(t *testing.T) {
	b := testBase(time.Minute)
	rows := []cursorRow{{At: "not-a-timestamp"}}

	if _, ok := (lastEvent[cursorRow]{timestamp: cursorRowTime}).advance(context.Background(), &b, rows, time.Now()); ok {
		t.Error("an unparseable timestamp must not move the cursor")
	}
}
