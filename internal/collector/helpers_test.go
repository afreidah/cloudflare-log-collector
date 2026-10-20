// -------------------------------------------------------------------------------
// Collector Test Helpers
//
// Author: Alex Freidah
//
// Fixtures and assertions shared by the collector tests: the common settings
// most tests build collectors with, and readers for Prometheus counter and
// gauge values.
// -------------------------------------------------------------------------------

package collector

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus "github.com/prometheus/client_model/go"
)

// -------------------------------------------------------------------------
// FIXTURES
// -------------------------------------------------------------------------

// testCommon returns the shared collector settings most tests use: a one-minute
// poll interval, a one-hour backfill window, and batches of 100, shipping to
// push. Tests that need other values adjust the returned CommonConfig.
func testCommon(push logPusher) CommonConfig {
	return CommonConfig{
		Loki:           push,
		PollInterval:   time.Minute,
		BackfillWindow: time.Hour,
		BatchSize:      100,
	}
}

// -------------------------------------------------------------------------
// ASSERTIONS
// -------------------------------------------------------------------------

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
