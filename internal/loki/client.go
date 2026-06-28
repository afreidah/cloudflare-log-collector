// -------------------------------------------------------------------------------
// Loki Push Client
//
// Author: Alex Freidah
//
// HTTP client for the Loki push API (POST /loki/api/v1/push). Batches log
// entries and sends them as JSON streams with configurable labels and tenant ID.
// Used to ship Cloudflare firewall events, HTTP traffic, and audit logs into the
// cluster's Loki instance.
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
	"log/slog"
	"net/http"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/retry"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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
	endpoint   string
	tenantID   string
	httpClient httpDoer
}

// NewClient creates a Loki push API client.
func NewClient(endpoint, tenantID string) *Client {
	return &Client{
		endpoint: endpoint,
		tenantID: tenantID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
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
		metrics.LokiPushTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("marshal push request: %w", err)
	}

	var statusCode int
	var respBody []byte

	for attempt := range retry.MaxRetries + 1 {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
			c.endpoint+"/loki/api/v1/push", bytes.NewReader(payload))
		if err != nil {
			metrics.LokiPushTotal.WithLabelValues("error").Inc()
			return fmt.Errorf("create push request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-Scope-OrgID", c.tenantID)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			metrics.LokiPushTotal.WithLabelValues("error").Inc()
			return fmt.Errorf("push request: %w", err)
		}

		statusCode = resp.StatusCode
		respBody, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
		_ = resp.Body.Close()

		if !retry.IsRetryable(statusCode) || attempt == retry.MaxRetries {
			break
		}

		delay := retry.Delay(resp.Header, attempt)
		slog.WarnContext(ctx, "Loki returned retryable status, backing off",
			"status", statusCode, "attempt", attempt+1, "delay", delay)

		retryTimer := time.NewTimer(delay)
		select {
		case <-retryTimer.C:
		case <-ctx.Done():
			retryTimer.Stop()
			return ctx.Err()
		}
	}

	metrics.LokiPushDuration.Observe(time.Since(start).Seconds())

	if statusCode != http.StatusNoContent && statusCode != http.StatusOK {
		metrics.LokiPushTotal.WithLabelValues("error").Inc()
		err := fmt.Errorf("loki push HTTP %d: %s", statusCode, string(respBody))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	metrics.LokiPushTotal.WithLabelValues("success").Inc()
	return nil
}

// -------------------------------------------------------------------------
// ENTRY
// -------------------------------------------------------------------------

// Entry is a single log line with a nanosecond-precision timestamp string.
type Entry struct {
	Timestamp string // nanoseconds since epoch as a string
	Line      string // JSON-encoded log line
}

// NewEntry creates a log entry from a time and JSON line.
func NewEntry(t time.Time, line string) Entry {
	return Entry{
		Timestamp: fmt.Sprintf("%d", t.UnixNano()),
		Line:      line,
	}
}
