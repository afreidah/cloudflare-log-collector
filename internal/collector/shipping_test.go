// -------------------------------------------------------------------------------
// Loki Shipping Helper Tests
//
// Author: Alex Freidah
//
// Covers the ship-then-commit contract every collector relies on: the commit
// runs only after every batch has been accepted, never after a failed push, and
// a nil commit ships without side effects.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
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

func TestShipThenCommit_NilCommitShipsOnly(t *testing.T) {
	push := &fakePusher{}

	err := shipThenCommit(context.Background(), shippingBase(push, 100), []string{"a"}, nil)
	if err != nil {
		t.Fatalf("shipThenCommit() error = %v", err)
	}
	if len(push.entries) != 1 {
		t.Errorf("got %d pushes, want 1", len(push.entries))
	}
}
