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

// The datasets the collectors poll, one per poll; each labels its poll's
// metrics and span. DatasetFirewall, DatasetHTTP, DatasetDNS, DatasetAudit,
// and DatasetRUM also name their collectors' lifecycle registrations.
const (
	DatasetFirewall            Dataset = "firewall"
	DatasetHTTP                Dataset = "http"
	DatasetHTTPOriginLatency   Dataset = "http_origin_latency"
	DatasetHTTPEdgeLatency     Dataset = "http_edge_latency"
	DatasetHTTPUniqueIPsHourly Dataset = "http_unique_ips_1h"
	DatasetHTTPUniqueIPsDaily  Dataset = "http_unique_ips_1d"
	DatasetDNS                 Dataset = "dns"
	DatasetDNSProcessing       Dataset = "dns_processing"
	DatasetAudit               Dataset = "audit"
	DatasetRUM                 Dataset = "rum"
	DatasetRUMPageload         Dataset = "rum_pageload"
	DatasetRUMWebVitals        Dataset = "rum_web_vitals"
)

// String returns the dataset name for use where a plain string is required,
// such as metric label values and span attributes.
func (d Dataset) String() string {
	return string(d)
}
