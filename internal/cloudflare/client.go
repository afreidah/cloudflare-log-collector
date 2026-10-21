// -------------------------------------------------------------------------------
// Cloudflare API Client
//
// Authors: Alex Freidah, Aaron Florey
//
// Core client for the Cloudflare APIs: connection config, the client span and
// authorization every request carries, and the single retrying HTTP executor
// that every query builds on. Rate limiting is handled by the shared retry
// policy (exponential backoff that honors Retry-After), the final HTTP status
// is recorded on the active trace span, and any status other than 200 is an
// error. The per-dataset queries live in firewall.go, http.go, rum.go (GraphQL)
// and audit.go (REST); dataset queries are built in dataset.go and sent by the
// GraphQL transport in graphql.go.
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
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// graphQLEndpoint is the Cloudflare Analytics GraphQL API URL. maxResponseBytes
// caps the size of response bodies read from the API (10 MB) to guard against
// unbounded memory allocation. apiPeerService and apiHost name the API on
// every client span.
const (
	graphQLEndpoint  = "https://api.cloudflare.com/client/v4/graphql"
	maxResponseBytes = 10 << 20
	apiPeerService   = "cloudflare-api"
	apiHost          = "api.cloudflare.com"
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

// apiErrors is the errors array both Cloudflare APIs return in a 200 response
// that nonetheless failed.
type apiErrors []struct {
	Message string `json:"message"`
}

// err returns the first error, prefixed with which API reported it (e.g.
// "graphql error: ..."), or nil when there are none.
func (e apiErrors) err(api string) error {
	if len(e) == 0 {
		return nil
	}
	return fmt.Errorf("%s error: %s", api, e[0].Message)
}

// startAPISpan starts the client span for one Cloudflare API call, naming the
// API as its peer.
func startAPISpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return telemetry.StartClientSpan(ctx, name, apiPeerService, apiHost, attrs...)
}

// doWithRetry sends the request produced by buildReq, authorized with the API
// token, under the client's retry policy (see retry.Policy.Do). The request is
// rebuilt for every attempt so no per-request state leaks across retries. The
// final HTTP status code is recorded on the active span; any status other than
// 200 is returned as an error carrying the response body.
func (c *Client) doWithRetry(ctx context.Context, buildReq func() (*http.Request, error)) ([]byte, error) {
	res, err := c.retryPolicy.Do(ctx, "Cloudflare API", func() (retry.Result, error) {
		req, err := buildReq()
		if err != nil {
			return retry.Result{}, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiToken)

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
		return nil, err
	}

	trace.SpanFromContext(ctx).SetAttributes(attribute.Int("http.status_code", res.StatusCode))

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", res.StatusCode, string(res.Body))
	}
	return res.Body, nil
}
