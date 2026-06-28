// -------------------------------------------------------------------------------
// Cloudflare API Client
//
// Authors: Alex Freidah, Aaron Florey
//
// Core client for the Cloudflare APIs: connection config and the single
// retrying HTTP executor that every query builds on. Rate limiting is handled
// with exponential backoff that honors Retry-After, and the final HTTP status
// is recorded on the active trace span. The per-dataset queries live in
// firewall.go, http.go, rum.go (GraphQL) and audit.go (REST); the shared GraphQL
// envelope and executor live in graphql.go.
// -------------------------------------------------------------------------------

// Package cloudflare is a client for the Cloudflare GraphQL Analytics and REST
// Audit Logs APIs, with query building, response parsing, and retry logic.
package cloudflare

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	// graphQLEndpoint is the Cloudflare Analytics GraphQL API URL.
	graphQLEndpoint = "https://api.cloudflare.com/client/v4/graphql"

	// maxResponseBytes caps the size of response bodies read from the API to
	// guard against unbounded memory allocation.
	maxResponseBytes = 10 << 20 // 10 MB

	// maxRetries is the number of additional attempts after the initial request
	// for retryable HTTP status codes (429, 502, 503, 504).
	maxRetries = 3

	// retryBaseDelay is the initial backoff duration before the first retry.
	retryBaseDelay = 1 * time.Second
)

// -------------------------------------------------------------------------
// CLIENT
// -------------------------------------------------------------------------

// httpDoer is the client's view of an HTTP transport: the single method it
// calls on *http.Client. Declaring it here lets tests inject a fake to drive
// transport-error paths the httptest server cannot easily produce, while the
// real *http.Client satisfies it implicitly. See the Interface Design section
// of docs/style-guide.md.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client talks to the Cloudflare GraphQL Analytics and REST Audit Logs APIs.
type Client struct {
	apiToken      string
	endpoint      string
	auditEndpoint string
	httpClient    httpDoer
}

// NewClient creates a Cloudflare API client.
func NewClient(apiToken string) *Client {
	return &Client{
		apiToken:      apiToken,
		endpoint:      graphQLEndpoint,
		auditEndpoint: auditLogsEndpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewTestClient creates a client pointing at a custom endpoint for testing.
func NewTestClient(endpoint, apiToken string) *Client {
	return &Client{
		apiToken:      apiToken,
		endpoint:      endpoint,
		auditEndpoint: endpoint + "/accounts/%s/logs/audit",
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}
}

// -------------------------------------------------------------------------
// HTTP TRANSPORT
// -------------------------------------------------------------------------

// doWithRetry sends the request produced by buildReq, retrying on retryable
// status codes with exponential backoff that honors Retry-After. The request is
// rebuilt for every attempt so no per-request state leaks across retries. The
// final HTTP status code is recorded on the active span and returned alongside
// the response body for the caller to interpret.
func (c *Client) doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) ([]byte, int, error) {
	var respBody []byte
	var statusCode int

	for attempt := range maxRetries + 1 {
		req, err := buildReq()
		if err != nil {
			return nil, 0, fmt.Errorf("create request: %w", err)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("http request: %w", err)
		}

		respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, 0, fmt.Errorf("read response: %w", err)
		}

		statusCode = resp.StatusCode

		if !isRetryable(statusCode) || attempt == maxRetries {
			break
		}

		delay := retryDelay(resp.Header, attempt)
		slog.WarnContext(ctx, "Cloudflare API returned retryable status, backing off",
			"status", statusCode, "attempt", attempt+1, "delay", delay)

		retryTimer := time.NewTimer(delay)
		select {
		case <-retryTimer.C:
		case <-ctx.Done():
			retryTimer.Stop()
			return nil, statusCode, ctx.Err()
		}
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.status_code", statusCode))

	return respBody, statusCode, nil
}

// isRetryable returns true for HTTP status codes that warrant a retry.
func isRetryable(statusCode int) bool {
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

// retryDelay computes the backoff duration for the given attempt, honoring
// the Retry-After header if present.
func retryDelay(header http.Header, attempt int) time.Duration {
	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return retryBaseDelay * (1 << attempt)
}
