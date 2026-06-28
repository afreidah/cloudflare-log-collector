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
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// httpQueryLimit is the maximum number of HTTP request groups returned per query.
const httpQueryLimit = 5000

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

// httpRequestResponse maps the GraphQL response for httpRequestsAdaptiveGroups queries.
type httpRequestResponse struct {
	Viewer struct {
		Zones []struct {
			HTTPRequestsAdaptiveGroups []HTTPRequestGroup `json:"httpRequestsAdaptiveGroups"`
		} `json:"zones"`
	} `json:"viewer"`
}

// httpRequestQuery fetches aggregated HTTP traffic grouped by method, status, and country.
const httpRequestQuery = `query ($zoneId: String!, $since: String!, $until: String!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      httpRequestsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until}
        limit: 5000
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
}`

// QueryHTTPRequests fetches aggregated HTTP traffic stats for the given zone and time range.
func (c *Client) QueryHTTPRequests(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPRequestGroup, error) {
	vars := map[string]any{
		"zoneId": zoneID,
		"since":  since.UTC().Format(time.RFC3339),
		"until":  until.UTC().Format(time.RFC3339),
	}

	body, err := c.doQuery(ctx, zoneID, httpRequestQuery, vars)
	if err != nil {
		return nil, fmt.Errorf("http request query: %w", err)
	}

	var resp httpRequestResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("http request response parse: %w", err)
	}

	if len(resp.Viewer.Zones) == 0 {
		return nil, nil
	}

	groups := resp.Viewer.Zones[0].HTTPRequestsAdaptiveGroups
	if len(groups) >= httpQueryLimit {
		slog.WarnContext(ctx, "HTTP request query hit limit, groups may be truncated",
			"zone_id", zoneID, "limit", httpQueryLimit, "count", len(groups))
	}

	return groups, nil
}
