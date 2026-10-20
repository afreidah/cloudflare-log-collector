// -------------------------------------------------------------------------------
// Cloudflare HTTP Traffic Query
//
// Authors: Alex Freidah, Aaron Florey
//
// Queries the zone-scoped httpRequestsAdaptiveGroups GraphQL dataset for HTTP
// traffic aggregated by method, status, and country.
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
type HTTPRequestDimensions struct {
	Datetime                    string `json:"datetime"`
	ClientRequestHTTPMethodName string `json:"clientRequestHTTPMethodName"`
	EdgeResponseStatus          int    `json:"edgeResponseStatus"`
	ClientCountryName           string `json:"clientCountryName"`
}

// HTTPRequestSum holds the aggregated byte counts for HTTP traffic.
type HTTPRequestSum struct {
	EdgeResponseBytes int64 `json:"edgeResponseBytes"`
}

// httpDataset fetches aggregated HTTP traffic grouped by method, status, and
// country.
var httpDataset = dataset{
	field: "httpRequestsAdaptiveGroups",
	scope: "zones",
	limit: 5000,
	query: `query ($zoneId: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      httpRequestsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until}
        limit: $limit
      ) {
        count
        dimensions {
          datetime
          clientRequestHTTPMethodName
          edgeResponseStatus
          clientCountryName
        }
        sum {
          edgeResponseBytes
        }
      }
    }
  }
}`,
}

// QueryHTTPRequests fetches aggregated HTTP traffic stats for the given zone and time range.
func (c *Client) QueryHTTPRequests(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPRequestGroup, error) {
	return queryDataset[HTTPRequestGroup](ctx, c, httpDataset, zoneID, since, until,
		map[string]any{"zoneId": zoneID})
}
