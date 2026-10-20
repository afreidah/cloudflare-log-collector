// -------------------------------------------------------------------------------
// HTTP Retry Policy Tests
//
// Author: Alex Freidah
//
// Verifies the shared retry behavior: exponential delay from BaseDelay, the
// Retry-After header override, fallback on an unparseable header, the set of
// retryable status codes, and the attempt loop - retrying until success or the
// retry budget is spent, stopping on non-retryable statuses and attempt errors,
// and returning promptly when cancelled mid-backoff.
// -------------------------------------------------------------------------------

package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// fastPolicy retries on a millisecond scale so the attempt loop can be tested
// without real backoff waits.
var fastPolicy = Policy{MaxRetries: 3, BaseDelay: time.Millisecond}

// -------------------------------------------------------------------------
// DELAY
// -------------------------------------------------------------------------

func TestDelay_DefaultBackoff(t *testing.T) {
	header := http.Header{}
	base := Default.BaseDelay

	d0 := Default.Delay(header, 0)
	d1 := Default.Delay(header, 1)
	d2 := Default.Delay(header, 2)

	if d0 != base {
		t.Errorf("attempt 0: got %v, want %v", d0, base)
	}
	if d1 != 2*base {
		t.Errorf("attempt 1: got %v, want %v", d1, 2*base)
	}
	if d2 != 4*base {
		t.Errorf("attempt 2: got %v, want %v", d2, 4*base)
	}
}

func TestDelay_RetryAfterHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "10")

	d := Default.Delay(header, 0)
	if d != 10*time.Second {
		t.Errorf("got %v, want 10s (from Retry-After header)", d)
	}
}

func TestDelay_InvalidRetryAfterFallsBack(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "not-a-number")

	d := Default.Delay(header, 1)
	if d != 2*Default.BaseDelay {
		t.Errorf("got %v, want %v (fallback to exponential)", d, 2*Default.BaseDelay)
	}
}

// -------------------------------------------------------------------------
// DO
// -------------------------------------------------------------------------

// statuses returns an attempt func that answers with each status in turn,
// repeating the last, and counts calls.
func statuses(calls *int, codes ...int) func() (Result, error) {
	return func() (Result, error) {
		code := codes[min(*calls, len(codes)-1)]
		*calls++
		return Result{StatusCode: code, Header: http.Header{}}, nil
	}
}

func TestDo_RetriesUntilSuccess(t *testing.T) {
	var calls int
	res, err := fastPolicy.Do(context.Background(), "test", statuses(&calls, 503, 429, 200))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if res.StatusCode != 200 {
		t.Errorf("status = %d, want 200", res.StatusCode)
	}
	if calls != 3 {
		t.Errorf("attempts = %d, want 3", calls)
	}
}

func TestDo_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int
	res, err := fastPolicy.Do(context.Background(), "test", statuses(&calls, 503))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if res.StatusCode != 503 {
		t.Errorf("status = %d, want the last attempt's 503", res.StatusCode)
	}
	if calls != fastPolicy.MaxRetries+1 {
		t.Errorf("attempts = %d, want %d (initial + retries)", calls, fastPolicy.MaxRetries+1)
	}
}

func TestDo_NonRetryableReturnsImmediately(t *testing.T) {
	var calls int
	res, err := fastPolicy.Do(context.Background(), "test", statuses(&calls, 500))
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if res.StatusCode != 500 || calls != 1 {
		t.Errorf("status = %d after %d attempts, want 500 after 1", res.StatusCode, calls)
	}
}

func TestDo_AttemptErrorIsNotRetried(t *testing.T) {
	var calls int
	_, err := fastPolicy.Do(context.Background(), "test", func() (Result, error) {
		calls++
		return Result{}, errors.New("connection refused")
	})
	if err == nil {
		t.Fatal("expected the attempt error to be returned")
	}
	if calls != 1 {
		t.Errorf("attempts = %d, want 1 (attempt errors are not retried)", calls)
	}
}

func TestDo_CancelDuringBackoffReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	slow := Policy{MaxRetries: 3, BaseDelay: time.Hour}

	var calls int
	done := make(chan error, 1)
	go func() {
		res, err := slow.Do(ctx, "test", statuses(&calls, 503))
		if res.StatusCode != 503 {
			t.Errorf("status = %d, want the retried attempt's 503", res.StatusCode)
		}
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Do did not return after cancel")
	}
}

// -------------------------------------------------------------------------
// RETRYABLE STATUSES
// -------------------------------------------------------------------------

func TestIsRetryable(t *testing.T) {
	retryable := []int{429, 502, 503, 504}
	for _, code := range retryable {
		if !IsRetryable(code) {
			t.Errorf("status %d should be retryable", code)
		}
	}

	notRetryable := []int{200, 201, 400, 401, 403, 404, 500}
	for _, code := range notRetryable {
		if IsRetryable(code) {
			t.Errorf("status %d should not be retryable", code)
		}
	}
}
