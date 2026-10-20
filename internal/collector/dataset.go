// -------------------------------------------------------------------------------
// Collector Dataset Names
//
// Author: Alex Freidah
//
// Typed constants for the dataset names emitted as Prometheus metric labels,
// OpenTelemetry span attributes, Loki stream labels, and lifecycle service
// registration keys. Centralizing them here keeps every call site spelling the
// same value and lets the compiler catch typos that bare string literals would
// silently scatter across metrics and traces.
// -------------------------------------------------------------------------------

package collector

// Dataset identifies a Cloudflare dataset a collector polls.
type Dataset string

// The datasets the collectors poll: DatasetFirewall is firewallEventsAdaptive,
// DatasetHTTP is httpRequestsAdaptiveGroups, DatasetAudit is the account audit
// logs, DatasetRUMPageload is rumPageloadEventsAdaptiveGroups, and
// DatasetRUMWebVitals is rumWebVitalsEventsAdaptiveGroups. DatasetRUM names the
// RUM collector as a whole (its lifecycle registration key); the two RUM
// datasets carry the metric labels.
const (
	DatasetFirewall     Dataset = "firewall"
	DatasetHTTP         Dataset = "http"
	DatasetAudit        Dataset = "audit"
	DatasetRUM          Dataset = "rum"
	DatasetRUMPageload  Dataset = "rum_pageload"
	DatasetRUMWebVitals Dataset = "rum_web_vitals"
)

// String returns the dataset name for use where a plain string is required,
// such as metric label values and span attributes.
func (d Dataset) String() string {
	return string(d)
}
