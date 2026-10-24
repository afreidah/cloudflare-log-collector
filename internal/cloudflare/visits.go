// -------------------------------------------------------------------------------
// Cloudflare HTTP Visits Query
//
// Author: Alex Freidah
//
// Queries the zone-scoped httpRequestsAdaptiveGroups GraphQL dataset for
// visits - Cloudflare's server-side count of page views that start at the
// site - per minute and host. Unlike Web Analytics it needs no browser beacon.
// Requests whose user agent matches an excluded pattern are left out, so
// self-monitoring and self-identified bots do not count as visitors.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"
)

// httpVisitsQueryLimit is the most groups requested per visits query; there is
// one group per host per minute.
const httpVisitsQueryLimit = 5000

// HTTPVisitsGroup is the visits to one host in one minute.
type HTTPVisitsGroup struct {
	Dimensions HTTPVisitsDimensions `json:"dimensions"`
	Sum        HTTPVisitsSum        `json:"sum"`
}

// HTTPVisitsDimensions holds the grouping dimensions for visits.
type HTTPVisitsDimensions struct {
	DatetimeMinute        string `json:"datetimeMinute"`
	ClientRequestHTTPHost string `json:"clientRequestHTTPHost"`
}

// HTTPVisitsSum holds the visit count of a group.
type HTTPVisitsSum struct {
	Visits int64 `json:"visits"`
}

// HTTPVisitsQuery is the visits dataset with a fixed set of excluded user
// agents. Each pattern is sent as its own query variable rather than written
// into the document, so a pattern cannot break or alter the query; excludes
// holds those variables by name.
type HTTPVisitsQuery struct {
	ds       dataset[HTTPVisitsGroup]
	excludes map[string]any
}

// NewHTTPVisitsQuery builds the visits query for real clients, leaving out
// requests whose user agent matches any of excludeUserAgents. Patterns use the
// API's like syntax, where % matches any run of characters.
func NewHTTPVisitsQuery(excludeUserAgents []string) HTTPVisitsQuery {
	spec := datasetSpec{
		field:   "httpRequestsAdaptiveGroups",
		scope:   zoneScope,
		filters: []string{eyeballFilter},
		limit:   httpVisitsQueryLimit,
	}
	excludes := make(map[string]any, len(excludeUserAgents))
	terms := make([]string, 0, len(excludeUserAgents))
	for i, pattern := range excludeUserAgents {
		name := fmt.Sprintf("excludeUserAgent%d", i)
		spec.vars = append(spec.vars, name)
		terms = append(terms, "{userAgent_notlike: $"+name+"}")
		excludes[name] = pattern
	}
	if len(terms) > 0 {
		spec.filters = append(spec.filters, "AND: ["+strings.Join(terms, ", ")+"]")
	}
	return HTTPVisitsQuery{ds: newDataset[HTTPVisitsGroup](spec), excludes: excludes}
}

// QueryHTTPVisits runs q for the given zone and time range.
func (c *Client) QueryHTTPVisits(ctx context.Context, q HTTPVisitsQuery, zoneID string, since, until time.Time) ([]HTTPVisitsGroup, error) {
	vars := map[string]any{zoneScope.variable: zoneID}
	maps.Copy(vars, q.excludes)
	return queryDataset(ctx, c, q.ds, zoneID, since, until, vars)
}
