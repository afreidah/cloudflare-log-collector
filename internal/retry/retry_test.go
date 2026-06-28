// -------------------------------------------------------------------------------
// HTTP Retry Policy Tests
//
// Author: Alex Freidah
//
// Verifies the shared backoff policy: exponential delay from BaseDelay, the
// Retry-After header override, fallback on an unparseable header, and the set of
// retryable status codes.
// -------------------------------------------------------------------------------

package retry

import (
	"net/http"
	"testing"
	"time"
)

func TestDelay_DefaultBackoff(t *testing.T) {
	header := http.Header{}

	d0 := Delay(header, 0)
	d1 := Delay(header, 1)
	d2 := Delay(header, 2)

	if d0 != BaseDelay {
		t.Errorf("attempt 0: got %v, want %v", d0, BaseDelay)
	}
	if d1 != 2*BaseDelay {
		t.Errorf("attempt 1: got %v, want %v", d1, 2*BaseDelay)
	}
	if d2 != 4*BaseDelay {
		t.Errorf("attempt 2: got %v, want %v", d2, 4*BaseDelay)
	}
}

func TestDelay_RetryAfterHeader(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "10")

	d := Delay(header, 0)
	if d != 10*time.Second {
		t.Errorf("got %v, want 10s (from Retry-After header)", d)
	}
}

func TestDelay_InvalidRetryAfterFallsBack(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "not-a-number")

	d := Delay(header, 1)
	if d != 2*BaseDelay {
		t.Errorf("got %v, want %v (fallback to exponential)", d, 2*BaseDelay)
	}
}

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
