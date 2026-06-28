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

const (
	// DatasetFirewall is the firewallEventsAdaptive dataset.
	DatasetFirewall Dataset = "firewall"

	// DatasetHTTP is the httpRequestsAdaptiveGroups dataset.
	DatasetHTTP Dataset = "http"

	// DatasetAudit is the account audit logs dataset.
	DatasetAudit Dataset = "audit"
)

// String returns the dataset name for use where a plain string is required,
// such as metric label values and span attributes.
func (d Dataset) String() string {
	return string(d)
}
