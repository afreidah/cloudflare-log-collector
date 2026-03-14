// -------------------------------------------------------------------------------
// Firewall Event Collector Tests
//
// Author: Alex Freidah
//
// Tests for the firewall collector's Loki shipping logic. Verifies JSON
// serialization of firewall events, batch splitting, stream label assignment,
// and error handling on Loki push failures.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
)

// -------------------------------------------------------------------------
// SHIP TO LOKI
// -------------------------------------------------------------------------

func TestFirewallShipToLoki_SendsJSONEntries(t *testing.T) {
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
	c := NewFirewallCollector(nil, lokiClient, "zone1", "example.com", time.Minute, time.Hour, 100)

	events := []cloudflare.FirewallEvent{
		{
			Action:            "block",
			ClientIP:          "1.2.3.4",
			Datetime:          "2026-03-13T10:00:00Z",
			RayName:           "abc123",
			ClientCountryName: "US",
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
	if stream.Stream["type"] != "firewall" {
		t.Errorf("stream type = %q, want %q", stream.Stream["type"], "firewall")
	}
	if stream.Stream["job"] != "cloudflare" {
		t.Errorf("stream job = %q, want %q", stream.Stream["job"], "cloudflare")
	}
	if stream.Stream["zone"] != "example.com" {
		t.Errorf("stream zone = %q, want %q", stream.Stream["zone"], "example.com")
	}

	// --- Verify the log line contains the event data ---
	var event cloudflare.FirewallEvent
	if err := json.Unmarshal([]byte(stream.Values[0][1]), &event); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if event.Action != "block" {
		t.Errorf("action = %q, want %q", event.Action, "block")
	}
	if event.ClientIP != "1.2.3.4" {
		t.Errorf("clientIP = %q, want %q", event.ClientIP, "1.2.3.4")
	}
}

func TestFirewallShipToLoki_Batching(t *testing.T) {
	var requestCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")

	// --- Batch size of 3 with 7 events should produce 3 requests ---
	c := NewFirewallCollector(nil, lokiClient, "zone1", "example.com", time.Minute, time.Hour, 3)

	events := make([]cloudflare.FirewallEvent, 7)
	for i := range events {
		events[i] = cloudflare.FirewallEvent{
			Action:   "block",
			ClientIP: "1.2.3.4",
			Datetime: "2026-03-13T10:00:00Z",
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

func TestFirewallShipToLoki_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewFirewallCollector(nil, lokiClient, "zone1", "example.com", time.Minute, time.Hour, 100)

	events := []cloudflare.FirewallEvent{
		{
			Action:   "block",
			ClientIP: "1.2.3.4",
			Datetime: "2026-03-13T10:00:00Z",
		},
	}

	err := c.shipToLoki(context.Background(), events)
	if err == nil {
		t.Error("expected error for Loki HTTP 500")
	}
}

func TestFirewallShipToLoki_InvalidTimestamp(t *testing.T) {
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
	c := NewFirewallCollector(nil, lokiClient, "zone1", "example.com", time.Minute, time.Hour, 100)

	// --- Event with unparseable timestamp should still be shipped ---
	events := []cloudflare.FirewallEvent{
		{
			Action:   "block",
			ClientIP: "1.2.3.4",
			Datetime: "not-a-timestamp",
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
