// -------------------------------------------------------------------------------
// Loki Shipping Helper Tests
//
// Author: Alex Freidah
//
// Covers the shipping every collector shares, once: each record becomes one JSON
// log line on the collector's stream, lines go out in batches, an empty window
// pushes nothing, a failed push stops the ship, and the commit runs only after
// every batch is accepted.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// shippingBase builds a base that ships to push in batches of batchSize.
func shippingBase(push *fakePusher, batchSize int) *base {
	common := testCommon(push)
	common.BatchSize = batchSize
	b := newBase(common, identity{name: "Test", stream: "test", scopeKey: "zone", scope: "example.com", record: "test record"})
	return &b
}

// -------------------------------------------------------------------------
// SHIP
// -------------------------------------------------------------------------

func TestShipThenCommit_SendsOneJSONLinePerRecord(t *testing.T) {
	push := &fakePusher{}
	rows := []cursorRow{{At: "2026-10-19T11:00:00Z"}, {At: "2026-10-19T11:05:00Z"}}

	if err := shipThenCommit(context.Background(), shippingBase(push, 100), rows, nil); err != nil {
		t.Fatalf("shipThenCommit() error = %v", err)
	}

	if len(push.entries) != 1 || len(push.entries[0]) != 2 {
		t.Fatalf("pushes = %v, want one push of 2 lines", push.entries)
	}
	if push.labels[0]["type"] != "test" || push.labels[0]["zone"] != "example.com" {
		t.Errorf("stream labels = %v, want the collector's stream", push.labels[0])
	}
	var got cursorRow
	if err := json.Unmarshal([]byte(push.entries[0][1].Line), &got); err != nil || got != rows[1] {
		t.Errorf("line = %q (err %v), want the JSON of %v", push.entries[0][1].Line, err, rows[1])
	}
}

func TestShipThenCommit_SplitsIntoBatches(t *testing.T) {
	push := &fakePusher{}
	rows := make([]cursorRow, 7)

	if err := shipThenCommit(context.Background(), shippingBase(push, 3), rows, nil); err != nil {
		t.Fatalf("shipThenCommit() error = %v", err)
	}

	if len(push.entries) != 3 {
		t.Errorf("got %d pushes, want 3 (batches of 3 from 7 records)", len(push.entries))
	}
}

func TestShipThenCommit_EmptyPushesNothing(t *testing.T) {
	push := &fakePusher{}

	if err := shipThenCommit[cursorRow](context.Background(), shippingBase(push, 100), nil, nil); err != nil {
		t.Fatalf("shipThenCommit() error = %v", err)
	}
	if len(push.entries) != 0 {
		t.Errorf("got %d pushes, want 0 for no records", len(push.entries))
	}
}

func TestShipThenCommit_SkipsRecordsThatCannotMarshal(t *testing.T) {
	push := &fakePusher{}
	rows := []any{map[string]string{"ok": "first"}, func() {}, map[string]string{"ok": "last"}}

	if err := shipThenCommit(context.Background(), shippingBase(push, 100), rows, nil); err != nil {
		t.Fatalf("shipThenCommit() error = %v", err)
	}
	if len(push.entries) != 1 || len(push.entries[0]) != 2 {
		t.Errorf("pushes = %v, want one push of the 2 marshalable records", push.entries)
	}
}

func TestShipThenCommit_StopsAtFirstFailedBatch(t *testing.T) {
	push := &fakePusher{err: errors.New("loki down")}

	err := shipThenCommit(context.Background(), shippingBase(push, 1), make([]cursorRow, 3), nil)
	if err == nil {
		t.Fatal("expected the push error to be returned")
	}
	if len(push.entries) != 1 {
		t.Errorf("got %d pushes, want 1 (no batches after a failure)", len(push.entries))
	}
}

// -------------------------------------------------------------------------
// COMMIT
// -------------------------------------------------------------------------

func TestShipThenCommit_CommitsAfterSuccessfulShip(t *testing.T) {
	push := &fakePusher{}
	committed := false

	err := shipThenCommit(context.Background(), shippingBase(push, 1), []string{"a", "b"}, func() {
		if len(push.entries) != 2 {
			t.Errorf("commit ran after %d pushes, want 2 (all batches shipped first)", len(push.entries))
		}
		committed = true
	})
	if err != nil {
		t.Fatalf("shipThenCommit() error = %v", err)
	}
	if !committed {
		t.Error("commit should run once every batch is accepted")
	}
}

func TestShipThenCommit_SkipsCommitOnPushError(t *testing.T) {
	push := &fakePusher{err: errors.New("loki down")}
	committed := false

	err := shipThenCommit(context.Background(), shippingBase(push, 100), []string{"a"}, func() { committed = true })
	if err == nil {
		t.Fatal("expected the push error to be returned")
	}
	if committed {
		t.Error("commit must not run when the push fails")
	}
}
