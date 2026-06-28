// -------------------------------------------------------------------------------
// Collector Dataset Tests
//
// Author: Alex Freidah
//
// Verifies the typed dataset constants serialize to the exact wire strings used
// as Prometheus labels, span attributes, Loki stream labels, and lifecycle
// registration keys. Pinning the values here guards against an accidental rename
// silently splitting a metric or stream into a new series.
// -------------------------------------------------------------------------------

package collector

import "testing"

func TestDatasetString(t *testing.T) {
	tests := []struct {
		name    string
		dataset Dataset
		want    string
	}{
		{"firewall", DatasetFirewall, "firewall"},
		{"http", DatasetHTTP, "http"},
		{"audit", DatasetAudit, "audit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.dataset.String(); got != tt.want {
				t.Errorf("Dataset.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDatasetStringMatchesConversion guards against String() drifting away from
// a plain string conversion of the underlying value.
func TestDatasetStringMatchesConversion(t *testing.T) {
	for _, d := range []Dataset{DatasetFirewall, DatasetHTTP, DatasetAudit} {
		if d.String() != string(d) {
			t.Errorf("Dataset.String() = %q, want %q", d.String(), string(d))
		}
	}
}
