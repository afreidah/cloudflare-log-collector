// -------------------------------------------------------------------------------
// Loki Push Client
//
// Author: Alex Freidah
//
// HTTP client for the Loki push API (POST /loki/api/v1/push). Sends log entries
// as JSON streams with configurable labels and tenant ID, retrying under the
// shared retry policy. Used to ship Cloudflare firewall events, HTTP traffic,
// audit logs, and RUM page loads into the cluster's Loki instance.
// -------------------------------------------------------------------------------

// Package loki is a client for the Loki push API, with batching, retry, and
// multi-tenant support.
package loki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/retry"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
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

// Client pushes log entries to the Loki HTTP API.
type Client struct {
	endpoint    string
	tenantID    string
	httpClient  httpDoer
	retryPolicy retry.Policy
}

// NewClient creates a Loki push API client.
func NewClient(endpoint, tenantID string) *Client {
	return &Client{
		endpoint: endpoint,
		tenantID: tenantID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		retryPolicy: retry.Default,
	}
}

// -------------------------------------------------------------------------
// PUSH API TYPES
// -------------------------------------------------------------------------

// pushRequest is the JSON payload for POST /loki/api/v1/push.
type pushRequest struct {
	Streams []stream `json:"streams"`
}

// stream represents a single log stream with labels and entries.
type stream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

// -------------------------------------------------------------------------
// PUSH
// -------------------------------------------------------------------------

// Push sends a batch of log entries to Loki under the given stream labels.
// Each entry is a [timestamp_nanos, json_line] pair.
func (c *Client) Push(ctx context.Context, labels map[string]string, entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}

	ctx, span := telemetry.StartClientSpan(ctx, "loki.push",
		attribute.String("peer.service", "loki"),
		attribute.String("server.address", c.endpoint),
		attribute.Int("loki.entry_count", len(entries)),
	)
	defer span.End()

	start := time.Now()

	// fail is the single failure exit: every failed push is counted and marks
	// the span failed, whatever stage it failed at.
	fail := func(err error) error {
		metrics.LokiPushTotal.WithLabelValues("error").Inc()
		telemetry.FailSpan(span, err)
		return err
	}

	values := make([][]string, len(entries))
	for i, e := range entries {
		values[i] = []string{e.Timestamp, e.Line}
	}

	req := pushRequest{
		Streams: []stream{
			{
				Stream: labels,
				Values: values,
			},
		},
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return fail(fmt.Errorf("marshal push request: %w", err))
	}

	res, err := c.retryPolicy.Do(ctx, "Loki", func() (retry.Result, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.endpoint+"/loki/api/v1/push", bytes.NewReader(payload))
		if err != nil {
			return retry.Result{}, fmt.Errorf("create push request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-Scope-OrgID", c.tenantID)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return retry.Result{}, fmt.Errorf("push request: %w", err)
		}

		// The body is only used to explain a rejected push, so a short read is
		// not itself a failure.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
		_ = resp.Body.Close()

		return retry.Result{StatusCode: resp.StatusCode, Header: resp.Header, Body: body}, nil
	})
	if err != nil {
		return fail(err)
	}

	metrics.LokiPushDuration.Observe(time.Since(start).Seconds())

	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("loki push HTTP %d: %s", res.StatusCode, string(res.Body)))
	}

	metrics.LokiPushTotal.WithLabelValues("success").Inc()
	return nil
}

// -------------------------------------------------------------------------
// ENTRY
// -------------------------------------------------------------------------

// Entry is a single log line with a nanosecond-precision timestamp string.
// Timestamp is nanoseconds since the epoch as a string, and Line is the
// JSON-encoded log line.
type Entry struct {
	Timestamp string
	Line      string
}

// NewEntry creates a log entry from a time and JSON line.
func NewEntry(t time.Time, line string) Entry {
	return Entry{
		Timestamp: fmt.Sprintf("%d", t.UnixNano()),
		Line:      line,
	}
}
