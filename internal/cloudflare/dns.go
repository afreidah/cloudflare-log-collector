// -------------------------------------------------------------------------------
// Cloudflare DNS Analytics Queries
//
// Author: Alex Freidah
//
// Queries the zone-scoped dnsAnalyticsAdaptiveGroups GraphQL dataset for the
// zone's authoritative DNS: queries grouped per minute by name, type, response
// code, cache state, and answering colo, and the zone's processing time
// quantiles.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"time"
)

// dnsQueryLimit is the most groups requested per DNS query.
const dnsQueryLimit = 10000

// DNSQueryGroup represents the DNS queries for one name, type, and outcome in
// one minute.
type DNSQueryGroup struct {
	Count      int                `json:"count"`
	Dimensions DNSQueryDimensions `json:"dimensions"`
}

// DNSQueryDimensions holds the grouping dimensions for DNS queries.
// ResponseCode is the DNS response code name (e.g. "NOERROR", "NXDOMAIN").
// ResponseCached is 1 when Cloudflare answered from cache. ColoName is the
// Cloudflare data center that answered.
type DNSQueryDimensions struct {
	DatetimeMinute string `json:"datetimeMinute"`
	QueryName      string `json:"queryName"`
	QueryType      string `json:"queryType"`
	ResponseCode   string `json:"responseCode"`
	ResponseCached int    `json:"responseCached"`
	ColoName       string `json:"coloName"`
}

// DNSProcessingGroup is the zone's DNS processing time quantiles over a window.
type DNSProcessingGroup struct {
	Count     int                    `json:"count"`
	Quantiles DNSProcessingQuantiles `json:"quantiles"`
}

// DNSProcessingQuantiles is how long Cloudflare took to answer a query, in
// microseconds.
type DNSProcessingQuantiles struct {
	ProcessingTimeUsP50 float64 `json:"processingTimeUsP50"`
	ProcessingTimeUsP95 float64 `json:"processingTimeUsP95"`
	ProcessingTimeUsP99 float64 `json:"processingTimeUsP99"`
}

// Percentiles implements LatencyQuantiles.
func (q DNSProcessingQuantiles) Percentiles() Percentiles {
	return Percentiles{P50: q.ProcessingTimeUsP50, P95: q.ProcessingTimeUsP95, P99: q.ProcessingTimeUsP99}
}

// dnsQueryDataset fetches the zone's DNS queries grouped per minute by name,
// type, response code, cache state, and colo.
var dnsQueryDataset = newDataset[DNSQueryGroup](datasetSpec{
	field: "dnsAnalyticsAdaptiveGroups",
	scope: zoneScope,
	limit: dnsQueryLimit,
})

// dnsProcessingDataset fetches the zone's DNS processing time quantiles; with
// no dimensions it returns one row for the window.
var dnsProcessingDataset = newDataset[DNSProcessingGroup](datasetSpec{
	field: "dnsAnalyticsAdaptiveGroups",
	scope: zoneScope,
	limit: dnsQueryLimit,
})

// QueryDNSQueries fetches DNS query groups for the given zone and time range.
func (c *Client) QueryDNSQueries(ctx context.Context, zoneID string, since, until time.Time) ([]DNSQueryGroup, error) {
	return queryZone(ctx, c, dnsQueryDataset, zoneID, since, until)
}

// QueryDNSProcessingTime fetches the zone's DNS processing time quantiles for
// the given time range.
func (c *Client) QueryDNSProcessingTime(ctx context.Context, zoneID string, since, until time.Time) ([]DNSProcessingGroup, error) {
	return queryZone(ctx, c, dnsProcessingDataset, zoneID, since, until)
}
