// -------------------------------------------------------------------------------
// Cloudflare HTTP Traffic Query
//
// Authors: Alex Freidah, Aaron Florey
//
// Queries the zone-scoped httpRequestsAdaptiveGroups GraphQL dataset for HTTP
// traffic aggregated per minute by host, method, edge and origin status, cache
// status, request source, and country, and for per-host latency quantiles:
// origin response time and edge time to first byte. Also queries the hourly
// and daily rollups (httpRequests1hGroups, httpRequests1dGroups) for distinct
// client IPs.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"time"
)

// HTTPRequestGroup represents an aggregated HTTP traffic data point.
type HTTPRequestGroup struct {
	Count      int                   `json:"count"`
	Dimensions HTTPRequestDimensions `json:"dimensions"`
	Sum        HTTPRequestSum        `json:"sum"`
}

// HTTPRequestDimensions holds the grouping dimensions for HTTP traffic.
// DatetimeMinute is the minute the group falls in. OriginResponseStatus is the
// status the origin returned to Cloudflare, or 0 when no origin answered
// (served from cache, generated at the edge, or the origin was unreachable).
// RequestSource says who made the request: "eyeball" for real clients, or a
// Cloudflare-internal source such as "edgeWorkerFetch" for a Worker's own
// subrequests.
type HTTPRequestDimensions struct {
	DatetimeMinute              string `json:"datetimeMinute"`
	ClientRequestHTTPHost       string `json:"clientRequestHTTPHost"`
	ClientRequestHTTPMethodName string `json:"clientRequestHTTPMethodName"`
	EdgeResponseStatus          int    `json:"edgeResponseStatus"`
	OriginResponseStatus        int    `json:"originResponseStatus"`
	CacheStatus                 string `json:"cacheStatus"`
	RequestSource               string `json:"requestSource"`
	ClientCountryName           string `json:"clientCountryName"`
}

// HTTPRequestSum holds the aggregated byte counts for HTTP traffic.
type HTTPRequestSum struct {
	EdgeResponseBytes int64 `json:"edgeResponseBytes"`
}

// httpDataset fetches aggregated HTTP traffic grouped by minute, host, method,
// edge and origin status, cache status, request source, and country. Grouping
// by minute rather than second keeps a poll window well under the group limit
// once the per-host dimensions multiply the groups.
var httpDataset = newDataset[HTTPRequestGroup](datasetSpec{
	field: "httpRequestsAdaptiveGroups",
	scope: zoneScope,
	limit: 5000,
})

// QueryHTTPRequests fetches aggregated HTTP traffic stats for the given zone and time range.
func (c *Client) QueryHTTPRequests(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPRequestGroup, error) {
	return queryZone(ctx, c, httpDataset, zoneID, since, until)
}

// -------------------------------------------------------------------------
// LATENCY
// -------------------------------------------------------------------------

// HTTPLatencyGroup is one host's latency quantiles over a window. Q is the
// quantile set selected - origin response time or edge time to first byte -
// so each latency dataset gets its own query from one row shape.
type HTTPLatencyGroup[Q LatencyQuantiles] struct {
	Count      int                   `json:"count"`
	Dimensions HTTPLatencyDimensions `json:"dimensions"`
	Quantiles  Q                     `json:"quantiles"`
}

// HTTPLatencyDimensions holds the grouping dimension for latency: the host.
type HTTPLatencyDimensions struct {
	ClientRequestHTTPHost string `json:"clientRequestHTTPHost"`
}

// OriginLatencyQuantiles is how long the origin took to respond to Cloudflare,
// in milliseconds.
type OriginLatencyQuantiles struct {
	OriginResponseDurationMsP50 float64 `json:"originResponseDurationMsP50"`
	OriginResponseDurationMsP95 float64 `json:"originResponseDurationMsP95"`
	OriginResponseDurationMsP99 float64 `json:"originResponseDurationMsP99"`
}

// Percentiles implements LatencyQuantiles.
func (q OriginLatencyQuantiles) Percentiles() Percentiles {
	return Percentiles{P50: q.OriginResponseDurationMsP50, P95: q.OriginResponseDurationMsP95, P99: q.OriginResponseDurationMsP99}
}

// EdgeLatencyQuantiles is the time from Cloudflare receiving a request to
// sending the first response byte, in milliseconds.
type EdgeLatencyQuantiles struct {
	EdgeTimeToFirstByteMsP50 float64 `json:"edgeTimeToFirstByteMsP50"`
	EdgeTimeToFirstByteMsP95 float64 `json:"edgeTimeToFirstByteMsP95"`
	EdgeTimeToFirstByteMsP99 float64 `json:"edgeTimeToFirstByteMsP99"`
}

// Percentiles implements LatencyQuantiles.
func (q EdgeLatencyQuantiles) Percentiles() Percentiles {
	return Percentiles{P50: q.EdgeTimeToFirstByteMsP50, P95: q.EdgeTimeToFirstByteMsP95, P99: q.EdgeTimeToFirstByteMsP99}
}

// eyeballFilter limits the latency datasets to real clients, so a Worker's
// own subrequests do not skew a host's distribution; latencyQueryLimit bounds
// their groups, one per host.
const (
	eyeballFilter     = `requestSource: "eyeball"`
	latencyQueryLimit = 1000
)

// httpOriginLatencyDataset fetches origin response time quantiles per host.
// Requests no origin answered (cache hits, edge-generated responses, an
// unreachable origin) have no origin time, so they are filtered out rather
// than pulling the quantiles toward zero. Quantiles cannot be split once
// computed, which is why origin and edge latency are separate queries: edge
// latency must keep the cache hits this one drops.
var httpOriginLatencyDataset = newDataset[HTTPLatencyGroup[OriginLatencyQuantiles]](datasetSpec{
	field:   "httpRequestsAdaptiveGroups",
	scope:   zoneScope,
	filters: []string{eyeballFilter, "originResponseStatus_gt: 0"},
	limit:   latencyQueryLimit,
})

// httpEdgeLatencyDataset fetches edge time-to-first-byte quantiles per host,
// over every client request, cache hits included.
var httpEdgeLatencyDataset = newDataset[HTTPLatencyGroup[EdgeLatencyQuantiles]](datasetSpec{
	field:   "httpRequestsAdaptiveGroups",
	scope:   zoneScope,
	filters: []string{eyeballFilter},
	limit:   latencyQueryLimit,
})

// QueryHTTPOriginLatency fetches per-host origin response time quantiles for
// the given zone and time range.
func (c *Client) QueryHTTPOriginLatency(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPLatencyGroup[OriginLatencyQuantiles], error) {
	return queryZone(ctx, c, httpOriginLatencyDataset, zoneID, since, until)
}

// QueryHTTPEdgeLatency fetches per-host edge time-to-first-byte quantiles for
// the given zone and time range.
func (c *Client) QueryHTTPEdgeLatency(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPLatencyGroup[EdgeLatencyQuantiles], error) {
	return queryZone(ctx, c, httpEdgeLatencyDataset, zoneID, since, until)
}

// -------------------------------------------------------------------------
// UNIQUE IPS
// -------------------------------------------------------------------------

// HTTPUniqueIPsGroup is the number of distinct client IPs across one or more
// hourly or daily rollup buckets. It counts IPs, not people: bots, scanners,
// and monitors are included, and NAT or mobile carriers merge or split real
// visitors.
type HTTPUniqueIPsGroup struct {
	Uniq HTTPUniq `json:"uniq"`
}

// HTTPUniq holds a rollup's distinct-value counts.
type HTTPUniq struct {
	Uniques int64 `json:"uniques"`
}

// uniqueIPsQueryLimit bounds the unique-IP rollup queries; with no dimensions
// they return a single row for the window.
const uniqueIPsQueryLimit = 10

// httpUniqueIPsHourlyDataset and httpUniqueIPsDailyDataset fetch the distinct
// client IPs in the hourly and daily rollup buckets that start within
// [since, until). Distinct counts cannot be added up, so a caller asks for
// exactly the one bucket it reports.
var (
	httpUniqueIPsHourlyDataset = newDataset[HTTPUniqueIPsGroup](datasetSpec{
		field:  "httpRequests1hGroups",
		scope:  zoneScope,
		window: hourBucket,
		limit:  uniqueIPsQueryLimit,
	})
	httpUniqueIPsDailyDataset = newDataset[HTTPUniqueIPsGroup](datasetSpec{
		field:  "httpRequests1dGroups",
		scope:  zoneScope,
		window: dayBucket,
		limit:  uniqueIPsQueryLimit,
	})
)

// QueryHTTPUniqueIPsHourly fetches the distinct client IPs in the hourly
// buckets starting in [since, until) for the given zone.
func (c *Client) QueryHTTPUniqueIPsHourly(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPUniqueIPsGroup, error) {
	return queryZone(ctx, c, httpUniqueIPsHourlyDataset, zoneID, since, until)
}

// QueryHTTPUniqueIPsDaily fetches the distinct client IPs in the daily buckets
// (UTC dates) starting in [since, until) for the given zone.
func (c *Client) QueryHTTPUniqueIPsDaily(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPUniqueIPsGroup, error) {
	return queryZone(ctx, c, httpUniqueIPsDailyDataset, zoneID, since, until)
}
