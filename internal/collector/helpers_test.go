// -------------------------------------------------------------------------------
// Collector Test Helpers
//
// Author: Alex Freidah
//
// Fixtures and assertions shared by the collector tests: the common settings
// and per-collector configs tests build collectors with, a decoder for shipped
// Loki lines, and readers for Prometheus counter and gauge values.
// -------------------------------------------------------------------------------

package collector

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus "github.com/prometheus/client_model/go"
)

// -------------------------------------------------------------------------
// FIXTURES
// -------------------------------------------------------------------------

// testCommon returns the shared collector settings most tests use: a one-minute
// poll interval, a one-hour backfill window, no ingestion lag, a 15-minute
// trailing window, and batches of 100, shipping to push. Tests that need other
// values adjust the returned CommonConfig.
func testCommon(push logPusher) CommonConfig {
	return CommonConfig{
		Loki:           push,
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		TrailingWindow: 15 * time.Minute,
		BatchSize:      100,
	}
}

// zoneTestConfig, auditTestConfig, and rumTestConfig build each collector's
// config around testCommon(push), for zone "zone1" (example.com), account
// "acct1" (acme), and site tag "site1" (example.com).
func zoneTestConfig(cf cloudflareQuerier, push logPusher) ZoneCollectorConfig {
	return ZoneCollectorConfig{CommonConfig: testCommon(push), CF: cf, ZoneID: "zone1", ZoneName: "example.com"}
}

func auditTestConfig(cf auditQuerier, push logPusher) AuditCollectorConfig {
	return AuditCollectorConfig{CommonConfig: testCommon(push), CF: cf, AccountID: "acct1", AccountName: "acme"}
}

func rumTestConfig(cf rumQuerier, push logPusher) RUMCollectorConfig {
	return RUMCollectorConfig{CommonConfig: testCommon(push), CF: cf, AccountID: "acct1", SiteTag: "site1", SiteName: "example.com"}
}

// -------------------------------------------------------------------------
// ASSERTIONS
// -------------------------------------------------------------------------

// decodeLine unmarshals one shipped Loki log line into a T, failing the test
// if it is not valid JSON for T.
func decodeLine[T any](t *testing.T, line string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatalf("unmarshal log line %q: %v", line, err)
	}
	return v
}

// assertCounterValue reads the current value of a Prometheus counter and asserts
// it matches the expected value.
func assertCounterValue(t *testing.T, counter prometheus.Counter, expected float64) {
	t.Helper()

	var m io_prometheus.Metric
	if err := counter.Write(&m); err != nil {
		t.Fatalf("failed to read counter: %v", err)
	}

	if m.Counter.GetValue() != expected {
		t.Errorf("counter value = %v, want %v", m.Counter.GetValue(), expected)
	}
}

// assertGaugeValue reads the current value of a Prometheus gauge and asserts it
// matches the expected value.
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
