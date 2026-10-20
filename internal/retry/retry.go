// -------------------------------------------------------------------------------
// HTTP Retry Policy
//
// Author: Alex Freidah
//
// Shared retry behavior for the Cloudflare and Loki clients: which HTTP status
// codes warrant a retry, how long to wait before the next attempt, and the
// attempt loop itself. Each client supplies one attempt - building its request
// and reading the response its own way - and Policy.Do decides whether and
// when to try again, so the retry behavior stays identical in one place.
// -------------------------------------------------------------------------------

// Package retry holds the HTTP retry policy and attempt loop shared by the
// Cloudflare and Loki clients.
package retry

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// -------------------------------------------------------------------------
// POLICY
// -------------------------------------------------------------------------

// Policy is a retry policy: how many times to retry a retryable response and
// the backoff between attempts. It is a value rather than constants so tests
// can retry on a millisecond scale. MaxRetries is the number of additional
// attempts after the initial request for retryable HTTP status codes (429, 502,
// 503, 504). BaseDelay is the backoff before the first retry; it doubles on
// each retry after that unless the server sends Retry-After.
type Policy struct {
	MaxRetries int
	BaseDelay  time.Duration
}

// Default is the policy the clients use in production.
var Default = Policy{
	MaxRetries: 3,
	BaseDelay:  1 * time.Second,
}

// Result is the outcome of one HTTP attempt: the status and headers that decide
// whether to retry, and the response body as the client read it.
type Result struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// IsRetryable returns true for HTTP status codes that warrant a retry.
func IsRetryable(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// Delay computes the backoff duration for the given attempt, honoring the
// Retry-After header if present. attempt is zero-based, so the delay doubles
// from BaseDelay on each successive retry.
func (p Policy) Delay(header http.Header, attempt int) time.Duration {
	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return p.BaseDelay * (1 << attempt)
}

// -------------------------------------------------------------------------
// ATTEMPT LOOP
// -------------------------------------------------------------------------

// Do runs attempt until it returns a non-retryable status or the policy's
// retries are spent, backing off between attempts, and returns the last
// attempt's result. service names the remote end in the backoff warning, e.g.
// "Cloudflare API".
//
// An error from attempt itself (a request that could not be built or sent, or a
// body that could not be read) is returned straight away without retrying. If
// ctx is cancelled during a backoff, Do returns ctx.Err() with the result of the
// attempt that was being retried.
func (p Policy) Do(ctx context.Context, service string, attempt func() (Result, error)) (Result, error) {
	for n := 0; ; n++ {
		res, err := attempt()
		if err != nil {
			return Result{}, err
		}

		if !IsRetryable(res.StatusCode) || n == p.MaxRetries {
			return res, nil
		}

		delay := p.Delay(res.Header, n)
		slog.WarnContext(ctx, service+" returned retryable status, backing off",
			"status", res.StatusCode, "attempt", n+1, "delay", delay)

		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return res, ctx.Err()
		}
	}
}
