// -------------------------------------------------------------------------------
// Metric Label Helper Tests
//
// Author: Alex Freidah
//
// Covers host normalization (case, any port, IPv6 literals, bare hosts) and
// status label formatting, including 0 for "no status".
// -------------------------------------------------------------------------------

package collector

import "testing"

func TestNormalizeHost(t *testing.T) {
	tests := map[string]string{
		"example.com":         "example.com",
		"Example.COM":         "example.com",
		"example.com:443":     "example.com",
		"g3.munchbox.cc:8080": "g3.munchbox.cc",
		"[2001:db8::1]:443":   "2001:db8::1",
		"":                    "",
	}
	for in, want := range tests {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusLabel(t *testing.T) {
	for code, want := range map[int]string{0: "0", 200: "200", 504: "504"} {
		if got := statusLabel(code); got != want {
			t.Errorf("statusLabel(%d) = %q, want %q", code, got, want)
		}
	}
}
