// -------------------------------------------------------------------------------
// Cloudflare Latency Quantiles
//
// Author: Alex Freidah
//
// The shared shape of every latency distribution the datasets report - HTTP
// origin and edge time, DNS processing time. Each dataset selects its own
// quantile fields, named and unit-suffixed its own way, and reads them back as
// Percentiles, so collectors handle every kind of latency the same way.
// -------------------------------------------------------------------------------

package cloudflare

// Percentiles is a latency distribution in the unit its dataset reports: its
// p50, p95, and p99.
type Percentiles struct {
	P50 float64
	P95 float64
	P99 float64
}

// LatencyQuantiles is a quantile set a latency dataset selects, read back as
// Percentiles so every latency kind is handled the same way.
type LatencyQuantiles interface {
	Percentiles() Percentiles
}
