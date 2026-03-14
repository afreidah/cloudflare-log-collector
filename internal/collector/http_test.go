// -------------------------------------------------------------------------------
// HTTP Traffic Collector Tests
//
// Author: Alex Freidah
//
// Tests for the HTTP traffic collector's metric update and Loki shipping logic.
// Uses mock Cloudflare response data to verify Prometheus gauge population with
// country labels and structured JSON log entry generation for Loki.
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
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	io_prometheus "github.com/prometheus/client_model/go"
)

// httpTestConfig returns a CollectorConfig for HTTP collector tests with the
// given Loki client and batch size.
func httpTestConfig(lokiClient *loki.Client, batchSize int) CollectorConfig {
	return CollectorConfig{
		Loki:           lokiClient,
		ZoneID:         "zone1",
		ZoneName:       "example.com",
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      batchSize,
	}
}

// -------------------------------------------------------------------------
// UPDATE METRICS
// -------------------------------------------------------------------------

func TestUpdateMetrics_CountryLabel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewHTTPCollector(httpTestConfig(lokiClient, 100))

	groups := []cloudflare.HTTPRequestGroup{
		{
			Count: 10,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    "2026-03-13T10:00:00Z",
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
				ClientCountryName:           "US",
			},
			Sum: cloudflare.HTTPRequestSum{EdgeResponseBytes: 1024},
		},
		{
			Count: 5,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    "2026-03-13T10:00:00Z",
				ClientRequestHTTPMethodName: "POST",
				EdgeResponseStatus:          201,
				ClientCountryName:           "DE",
			},
			Sum: cloudflare.HTTPRequestSum{EdgeResponseBytes: 512},
		},
	}

	c.updateMetrics(groups)

	// --- Verify country label on HTTP requests gauge ---
	usGauge := metrics.HTTPRequests.WithLabelValues("GET", "200", "US", "example.com")
	assertGaugeValue(t, usGauge, 10)

	deGauge := metrics.HTTPRequests.WithLabelValues("POST", "201", "DE", "example.com")
	assertGaugeValue(t, deGauge, 5)

	// --- Verify edge bytes ---
	edgeGauge := metrics.HTTPBytes.WithLabelValues("edge", "example.com")
	assertGaugeValue(t, edgeGauge, 1536)
}

func TestUpdateMetrics_ResetsOnEachCall(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewHTTPCollector(httpTestConfig(lokiClient, 100))

	// --- First call ---
	c.updateMetrics([]cloudflare.HTTPRequestGroup{
		{
			Count: 100,
			Dimensions: cloudflare.HTTPRequestDimensions{
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
				ClientCountryName:           "US",
			},
			Sum: cloudflare.HTTPRequestSum{EdgeResponseBytes: 5000},
		},
	})

	// --- Second call with different data should reset ---
	c.updateMetrics([]cloudflare.HTTPRequestGroup{
		{
			Count: 3,
			Dimensions: cloudflare.HTTPRequestDimensions{
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
				ClientCountryName:           "US",
			},
			Sum: cloudflare.HTTPRequestSum{EdgeResponseBytes: 100},
		},
	})

	gauge := metrics.HTTPRequests.WithLabelValues("GET", "200", "US", "example.com")
	assertGaugeValue(t, gauge, 3)

	edgeGauge := metrics.HTTPBytes.WithLabelValues("edge", "example.com")
	assertGaugeValue(t, edgeGauge, 100)
}

// -------------------------------------------------------------------------
// SHIP TO LOKI
// -------------------------------------------------------------------------

func TestShipToLoki_SendsJSONEntries(t *testing.T) {
	var received [][]byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		received = append(received, body)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewHTTPCollector(httpTestConfig(lokiClient, 100))

	groups := []cloudflare.HTTPRequestGroup{
		{
			Count: 42,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    "2026-03-13T10:00:00Z",
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
				ClientCountryName:           "US",
			},
			Sum: cloudflare.HTTPRequestSum{EdgeResponseBytes: 1024},
		},
	}

	err := c.shipToLoki(context.Background(), groups)
	if err != nil {
		t.Fatalf("shipToLoki() error = %v", err)
	}

	if len(received) != 1 {
		t.Fatalf("got %d requests, want 1", len(received))
	}

	// --- Verify the push request structure ---
	var pushReq struct {
		Streams []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(received[0], &pushReq); err != nil {
		t.Fatalf("unmarshal push request: %v", err)
	}

	if len(pushReq.Streams) != 1 {
		t.Fatalf("got %d streams, want 1", len(pushReq.Streams))
	}

	stream := pushReq.Streams[0]
	if stream.Stream["type"] != "http_traffic" {
		t.Errorf("stream type = %q, want %q", stream.Stream["type"], "http_traffic")
	}
	if stream.Stream["job"] != "cloudflare" {
		t.Errorf("stream job = %q, want %q", stream.Stream["job"], "cloudflare")
	}
	if stream.Stream["zone"] != "example.com" {
		t.Errorf("stream zone = %q, want %q", stream.Stream["zone"], "example.com")
	}

	if len(stream.Values) != 1 {
		t.Fatalf("got %d values, want 1", len(stream.Values))
	}

	// --- Verify the log line contains the group data ---
	var group cloudflare.HTTPRequestGroup
	if err := json.Unmarshal([]byte(stream.Values[0][1]), &group); err != nil {
		t.Fatalf("unmarshal log line: %v", err)
	}
	if group.Count != 42 {
		t.Errorf("count = %d, want 42", group.Count)
	}
	if group.Dimensions.ClientCountryName != "US" {
		t.Errorf("country = %q, want %q", group.Dimensions.ClientCountryName, "US")
	}
}

func TestShipToLoki_Batching(t *testing.T) {
	var requestCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")

	// --- Batch size of 2 with 5 groups should produce 3 requests ---
	c := NewHTTPCollector(httpTestConfig(lokiClient, 2))

	groups := make([]cloudflare.HTTPRequestGroup, 5)
	for i := range groups {
		groups[i] = cloudflare.HTTPRequestGroup{
			Count: 1,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    "2026-03-13T10:00:00Z",
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
				ClientCountryName:           "US",
			},
		}
	}

	err := c.shipToLoki(context.Background(), groups)
	if err != nil {
		t.Fatalf("shipToLoki() error = %v", err)
	}

	if requestCount != 3 {
		t.Errorf("got %d Loki requests, want 3 (batches of 2 from 5 entries)", requestCount)
	}
}

func TestShipToLoki_EmptyGroups(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not make HTTP request for empty groups")
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewHTTPCollector(httpTestConfig(lokiClient, 100))

	err := c.shipToLoki(context.Background(), nil)
	if err != nil {
		t.Fatalf("shipToLoki() with empty groups should not error, got: %v", err)
	}
}

func TestShipToLoki_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	t.Cleanup(ts.Close)

	lokiClient := loki.NewClient(ts.URL, "fake")
	c := NewHTTPCollector(httpTestConfig(lokiClient, 100))

	groups := []cloudflare.HTTPRequestGroup{
		{
			Count: 1,
			Dimensions: cloudflare.HTTPRequestDimensions{
				Datetime:                    "2026-03-13T10:00:00Z",
				ClientRequestHTTPMethodName: "GET",
				EdgeResponseStatus:          200,
				ClientCountryName:           "US",
			},
		},
	}

	err := c.shipToLoki(context.Background(), groups)
	if err == nil {
		t.Error("expected error for Loki HTTP 500")
	}
}

// -------------------------------------------------------------------------
// HELPERS
// -------------------------------------------------------------------------

// assertGaugeValue reads the current value of a Prometheus gauge and asserts
// it matches the expected value.
func assertGaugeValue(t *testing.T, gauge prometheus.Gauge, expected float64) {
	t.Helper()

	var m io_prometheus.Metric
	if err := gauge.Write(&m); err != nil {
		t.Fatalf("failed to read gauge: %v", err)
	}

	if m.Gauge.GetValue() != expected {
		t.Errorf("gauge value = %v, want %v", m.Gauge.GetValue(), expected)
	}
}
