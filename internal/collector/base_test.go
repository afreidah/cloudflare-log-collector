// -------------------------------------------------------------------------------
// Collector Base Tests
//
// Author: Alex Freidah
//
// Covers what every collector inherits from its base: the stream labels and
// cursor start derived from its identity, poll-outcome metrics, and the poll
// loop's schedule and shutdown.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// testBase builds a base for a zone collector named "Test" polling example.com,
// with the given poll interval.
func testBase(interval time.Duration) base {
	common := testCommon(&fakePusher{})
	common.PollInterval = interval
	return newBase(common, identity{
		name:     "Test",
		stream:   "test_stream",
		scopeKey: "zone",
		scope:    "example.com",
		record:   "test record",
	})
}

// -------------------------------------------------------------------------
// IDENTITY
// -------------------------------------------------------------------------

func TestNewBase_StreamLabels(t *testing.T) {
	b := testBase(time.Minute)

	want := map[string]string{"job": "cloudflare", "type": "test_stream", "zone": "example.com"}
	if len(b.labels) != len(want) {
		t.Fatalf("labels = %v, want %v", b.labels, want)
	}
	for k, v := range want {
		if b.labels[k] != v {
			t.Errorf("labels[%q] = %q, want %q", k, b.labels[k], v)
		}
	}
}

func TestBase_CursorStartIsOneBackfillWindowAgo(t *testing.T) {
	b := testBase(time.Minute)

	before := time.Now().UTC().Add(-time.Hour)
	got := b.cursorStart()
	after := time.Now().UTC().Add(-time.Hour)

	if got.Before(before) || got.After(after) {
		t.Errorf("cursorStart() = %v, want between %v and %v", got, before, after)
	}
}

// -------------------------------------------------------------------------
// RECORD POLL
// -------------------------------------------------------------------------

func TestRecordPoll_SuccessAndError(t *testing.T) {
	metrics.PollTotal.Reset()
	metrics.LastPollTimestamp.Reset()
	b := testBase(time.Minute)

	b.recordPoll(DatasetFirewall, time.Now(), nil)
	b.recordPoll(DatasetFirewall, time.Now(), errors.New("boom"))

	assertCounterValue(t, metrics.PollTotal.WithLabelValues("firewall", "example.com", "success"), 1)
	assertCounterValue(t, metrics.PollTotal.WithLabelValues("firewall", "example.com", "error"), 1)
	if testutil.ToFloat64(metrics.LastPollTimestamp.WithLabelValues("firewall", "example.com")) == 0 {
		t.Error("last poll timestamp should be set by the successful poll")
	}
}

func TestRecordPoll_ErrorDoesNotSetLastPoll(t *testing.T) {
	metrics.LastPollTimestamp.Reset()
	b := testBase(time.Minute)

	b.recordPoll(DatasetAudit, time.Now(), errors.New("boom"))

	if got := testutil.CollectAndCount(metrics.LastPollTimestamp); got != 0 {
		t.Errorf("last poll timestamp series = %d, want 0 after only a failed poll", got)
	}
}

// -------------------------------------------------------------------------
// POLL LOOP
// -------------------------------------------------------------------------

func TestRun_PollsImmediatelyThenOnTickUntilCancel(t *testing.T) {
	b := testBase(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	polls := make(chan struct{}, 10)

	done := make(chan error, 1)
	go func() {
		done <- b.run(ctx, time.Now(), func(context.Context) { polls <- struct{}{} })
	}()

	// One initial poll plus at least one ticked poll.
	for range 2 {
		select {
		case <-polls:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for a poll")
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v, want nil on cancel", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return after cancel")
	}
}
