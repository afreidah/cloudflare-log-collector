// -------------------------------------------------------------------------------
// Cloudflare API Client
//
// Authors: Alex Freidah, Aaron Florey
//
// Core client for the Cloudflare APIs: connection config and the single
// retrying HTTP executor that every query builds on. Rate limiting is handled
// by the shared retry policy (exponential backoff that honors Retry-After), and
// the final HTTP status is recorded on the active trace span. The per-dataset
// queries live in firewall.go, http.go, rum.go (GraphQL) and audit.go (REST);
// the shared dataset query and GraphQL transport live in graphql.go.
// -------------------------------------------------------------------------------

// Package cloudflare is a client for the Cloudflare GraphQL Analytics and REST
// Audit Logs APIs, with query building, response parsing, and retry logic.
package cloudflare

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/retry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	// graphQLEndpoint is the Cloudflare Analytics GraphQL API URL.
	graphQLEndpoint = "https://api.cloudflare.com/client/v4/graphql"

	// maxResponseBytes caps the size of response bodies read from the API to
	// guard against unbounded memory allocation.
	maxResponseBytes = 10 << 20 // 10 MB
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
	retryPolicy   retry.Policy
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
		retryPolicy: retry.Default,
	}
}

// NewTestClient creates a client pointing at a custom endpoint for testing.
func NewTestClient(endpoint, apiToken string) *Client {
	return &Client{
		apiToken:      apiToken,
		endpoint:      endpoint,
		auditEndpoint: endpoint + "/accounts/%s/logs/audit",
		httpClient:    &http.Client{Timeout: 5 * time.Second},
		retryPolicy:   retry.Default,
	}
}

// -------------------------------------------------------------------------
// HTTP TRANSPORT
// -------------------------------------------------------------------------

// doWithRetry sends the request produced by buildReq under the client's retry
// policy (see retry.Policy.Do). The request is rebuilt for every attempt so no
// per-request state leaks across retries. The final HTTP status code is
// recorded on the active span and returned alongside the response body for the
// caller to interpret.
func (c *Client) doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) ([]byte, int, error) {
	res, err := c.retryPolicy.Do(ctx, "Cloudflare API", func() (retry.Result, error) {
		req, err := buildReq()
		if err != nil {
			return retry.Result{}, fmt.Errorf("create request: %w", err)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return retry.Result{}, fmt.Errorf("http request: %w", err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return retry.Result{}, fmt.Errorf("read response: %w", err)
		}

		return retry.Result{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}, nil
	})
	if err != nil {
		return nil, res.StatusCode, err
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.status_code", res.StatusCode))

	return res.Body, res.StatusCode, nil
}
