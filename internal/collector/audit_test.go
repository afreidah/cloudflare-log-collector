// -------------------------------------------------------------------------------
// Audit Log Collector Tests
//
// Author: Aaron Florey
//
// Tests for the audit collector's Loki shipping logic. Verifies JSON
// serialization of audit events, batch splitting, stream label assignment,
// and error handling on Loki push failures.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
)

// auditTestConfig returns an AuditCollectorConfig for audit tests with the
// given Loki client and batch size.
func auditTestConfig(lokiClient *loki.Client, batchSize int) AuditCollectorConfig {
	return AuditCollectorConfig{
		Loki:           lokiClient,
		AccountID:      "account1",
		AccountName:    "test-account",
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      batchSize,
	}
}

// -------------------------------------------------------------------------
// SHIP TO LOKI
// -------------------------------------------------------------------------

func TestAuditShipToLoki_SendsJSONEntries(t *testing.T) {
	var received []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		received, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewAuditCollector(auditTestConfig(lokiClient, 100))

	events := []cloudflare.AuditLogEvent{
		{
			ID:        "event-123",
			AccountID: "account1",
			Account: cloudflare.AuditAccount{
				ID:   "account1",
				Name: "test-account",
			},
			Action: cloudflare.AuditAction{
				Description: "Test action",
				Result:      "success",
				Time:        "2026-03-13T10:00:00Z",
				Type:        "settings.modify",
			},
			Actor: cloudflare.AuditActor{
				ID:        "actor-123",
				Email:     "user@example.com",
				IPAddress: "1.2.3.4",
				Type:      "user",
			},
			Resource: cloudflare.AuditResource{
				ID:      "resource-123",
				Product: "zones",
				Type:    "zone",
			},
		},
	}

	err := c.shipToLoki(context.Background(), events)
	if err != nil {
		t.Fatalf("shipToLoki() error = %v", err)
	}

	// --- Verify push request structure ---
	var pushReq struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(received, &pushReq); err != nil {
		t.Fatalf("unmarshal push request: %v", err)
	}

	if len(pushReq.Streams) != 1 {
		t.Fatalf("got %d streams, want 1", len(pushReq.Streams))
	}

	stream := pushReq.Streams[0]
	if stream.Stream["type"] != "audit" {
		t.Errorf("stream type = %q, want %q", stream.Stream["type"], "audit")
	}
	if stream.Stream["job"] != "cloudflare" {
		t.Errorf("stream job = %q, want %q", stream.Stream["job"], "cloudflare")
	}
	if stream.Stream["account"] != "test-account" {
		t.Errorf("stream account = %q, want %q", stream.Stream["account"], "test-account")
	}

	// --- Verify the log line contains the event data ---
	var event cloudflare.AuditLogEvent
	if err := json.Unmarshal([]byte(stream.Values[0][1]), &event); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if event.ID != "event-123" {
		t.Errorf("event ID = %q, want %q", event.ID, "event-123")
	}
	if event.Action.Type != "settings.modify" {
		t.Errorf("action type = %q, want %q", event.Action.Type, "settings.modify")
	}
	if event.Actor.Email != "user@example.com" {
		t.Errorf("actor email = %q, want %q", event.Actor.Email, "user@example.com")
	}
}

func TestAuditShipToLoki_Batching(t *testing.T) {
	var requestCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")

	// --- Batch size of 3 with 7 events should produce 3 requests ---
	c := NewAuditCollector(auditTestConfig(lokiClient, 3))

	events := make([]cloudflare.AuditLogEvent, 7)
	for i := range events {
		events[i] = cloudflare.AuditLogEvent{
			ID:        "event-" + string(rune('0'+i)),
			AccountID: "account1",
			Action: cloudflare.AuditAction{
				Type: "settings.modify",
				Time: "2026-03-13T10:00:00Z",
			},
		}
	}

	err := c.shipToLoki(context.Background(), events)
	if err != nil {
		t.Fatalf("shipToLoki() error = %v", err)
	}

	if requestCount != 3 {
		t.Errorf("got %d Loki requests, want 3 (batches of 3 from 7 events)", requestCount)
	}
}

func TestAuditShipToLoki_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewAuditCollector(auditTestConfig(lokiClient, 100))

	events := []cloudflare.AuditLogEvent{
		{
			ID:        "event-123",
			AccountID: "account1",
			Action: cloudflare.AuditAction{
				Type: "settings.modify",
				Time: "2026-03-13T10:00:00Z",
			},
		},
	}

	err := c.shipToLoki(context.Background(), events)
	if err == nil {
		t.Error("expected error for Loki HTTP 500")
	}
}

func TestAuditShipToLoki_InvalidTimestamp(t *testing.T) {
	var received []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		received, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewAuditCollector(auditTestConfig(lokiClient, 100))

	// --- Event with unparseable timestamp should still be shipped ---
	events := []cloudflare.AuditLogEvent{
		{
			ID:        "event-123",
			AccountID: "account1",
			Action: cloudflare.AuditAction{
				Type: "settings.modify",
				Time: "not-a-timestamp",
			},
		},
	}

	err := c.shipToLoki(context.Background(), events)
	if err != nil {
		t.Fatalf("shipToLoki() error = %v", err)
	}

	if len(received) == 0 {
		t.Error("expected Loki push request even with invalid timestamp")
	}
}

// auditCursorServer serves a different page of audit events per poll. The first
// poll returns e1 and e2; the second returns e2 (the inclusive `since`
// boundary) and e3, simulating Cloudflare re-returning the boundary event.
func auditCursorServer(t *testing.T) *httptest.Server {
	t.Helper()
	var pollCount int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCount++
		w.Header().Set("Content-Type", "application/json")
		body := `{"success":true,"result":[
			{"id":"e2","action":{"type":"settings.modify","time":"2026-03-13T10:00:05Z"}},
			{"id":"e3","action":{"type":"settings.modify","time":"2026-03-13T10:00:09Z"}}
		],"result_info":{"count":2,"cursor":""}}`
		if pollCount == 1 {
			body = `{"success":true,"result":[
				{"id":"e1","action":{"type":"settings.modify","time":"2026-03-13T10:00:00Z"}},
				{"id":"e2","action":{"type":"settings.modify","time":"2026-03-13T10:00:05Z"}}
			],"result_info":{"count":2,"cursor":""}}`
		}
		_, _ = w.Write([]byte(body))
	}))
}

func TestAuditPoll_DedupesCursorBoundary(t *testing.T) {
	cfServer := auditCursorServer(t)
	t.Cleanup(cfServer.Close)

	var shipped []string
	lokiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var push struct {
			Streams []struct {
				Values [][]string `json:"values"`
			} `json:"streams"`
		}
		_ = json.Unmarshal(body, &push)
		for _, s := range push.Streams {
			for _, v := range s.Values {
				var ev cloudflare.AuditLogEvent
				if json.Unmarshal([]byte(v[1]), &ev) == nil {
					shipped = append(shipped, ev.ID)
				}
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(lokiServer.Close)

	cfg := auditTestConfig(loki.NewClient(lokiServer.URL, "fake"), 100)
	cfg.CF = cloudflare.NewTestClient(cfServer.URL, "test-token")
	c := NewAuditCollector(cfg)

	c.poll(context.Background())
	c.poll(context.Background())

	// e2 sits on the cursor boundary and is returned by both polls; it must
	// ship exactly once.
	want := []string{"e1", "e2", "e3"}
	if !slices.Equal(shipped, want) {
		t.Errorf("shipped = %v, want %v (boundary event e2 must not ship twice)", shipped, want)
	}
}

func TestNewAuditCollector_BackfillWindow(t *testing.T) {
	lokiClient := loki.NewClient("http://localhost:3100", "fake")

	backfillWindow := 2 * time.Hour
	cfg := AuditCollectorConfig{
		Loki:           lokiClient,
		AccountID:      "account1",
		AccountName:    "test-account",
		PollInterval:   time.Minute,
		BackfillWindow: backfillWindow,
		BatchSize:      100,
	}

	before := time.Now().UTC()
	c := NewAuditCollector(cfg)
	after := time.Now().UTC()

	// --- lastSeen should be approximately now - backfillWindow ---
	expectedMin := before.Add(-backfillWindow)
	expectedMax := after.Add(-backfillWindow)

	if c.lastSeen.Before(expectedMin) || c.lastSeen.After(expectedMax) {
		t.Errorf("lastSeen = %v, want between %v and %v", c.lastSeen, expectedMin, expectedMax)
	}
}
