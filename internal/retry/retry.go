// -------------------------------------------------------------------------------
// HTTP Retry Policy
//
// Author: Alex Freidah
//
// Shared backoff policy for the Cloudflare and Loki clients: which HTTP status
// codes warrant a retry, and how long to wait before the next attempt. Both
// clients drive their own request loop (their bodies and metrics differ) but
// share this policy so the retry behavior stays identical in one place.
// -------------------------------------------------------------------------------

package retry

import (
	"net/http"
	"strconv"
	"time"
)

const (
	// MaxRetries is the number of additional attempts after the initial request
	// for retryable HTTP status codes (429, 502, 503, 504).
	MaxRetries = 3

	// BaseDelay is the initial backoff duration before the first retry.
	BaseDelay = 1 * time.Second
)

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
func Delay(header http.Header, attempt int) time.Duration {
	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return BaseDelay * (1 << attempt)
}
