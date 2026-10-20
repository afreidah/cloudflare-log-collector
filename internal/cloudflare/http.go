// -------------------------------------------------------------------------------
// Cloudflare HTTP Traffic Query
//
// Authors: Alex Freidah, Aaron Florey
//
// Queries the zone-scoped httpRequestsAdaptiveGroups GraphQL dataset for HTTP
// traffic aggregated per minute by host, method, edge and origin status, cache
// status, request source, and country.
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
	return queryDataset(ctx, c, httpDataset, zoneID, since, until, map[string]any{"zoneId": zoneID})
}
