// -------------------------------------------------------------------------------
// Loki Push Client
//
// Author: Alex Freidah
//
// HTTP client for the Loki push API (POST /loki/api/v1/push). Batches log
// entries and sends them as JSON streams with configurable labels and tenant ID.
// Used to ship Cloudflare firewall events into the cluster's Loki instance.
// -------------------------------------------------------------------------------

package loki

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	// maxRetries is the number of additional attempts after the initial request
	// for retryable HTTP status codes (429, 502, 503, 504).
	maxRetries = 3

	// retryBaseDelay is the initial backoff duration before the first retry.
	retryBaseDelay = 1 * time.Second
)

// -------------------------------------------------------------------------
// CLIENT
// -------------------------------------------------------------------------

// Client pushes log entries to the Loki HTTP API.
type Client struct {
	endpoint   string
	tenantID   string
	httpClient *http.Client
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

	ctx, span := telemetry.StartSpan(ctx, "loki.push",
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

	for attempt := range maxRetries + 1 {
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

		if !isRetryable(statusCode) || attempt == maxRetries {
			break
		}

		delay := retryDelay(resp.Header, attempt)
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
