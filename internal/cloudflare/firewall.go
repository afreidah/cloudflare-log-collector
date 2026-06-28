// -------------------------------------------------------------------------------
// Cloudflare Firewall Events Query
//
// Authors: Alex Freidah, Aaron Florey
//
// Queries the zone-scoped firewallEventsAdaptive GraphQL dataset for individual
// WAF/firewall events, ordered by time for seek pagination.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// firewallQueryLimit is the maximum number of firewall events returned per query.
const firewallQueryLimit = 10000

// FirewallEvent represents a single firewall/WAF event from Cloudflare.
type FirewallEvent struct {
	Action                      string `json:"action"`
	ClientIP                    string `json:"clientIP"`
	ClientRequestHTTPHost       string `json:"clientRequestHTTPHost"`
	ClientRequestHTTPMethodName string `json:"clientRequestHTTPMethodName"`
	ClientRequestPath           string `json:"clientRequestPath"`
	ClientRequestQuery          string `json:"clientRequestQuery"`
	Datetime                    string `json:"datetime"`
	RayName                     string `json:"rayName"`
	RuleID                      string `json:"ruleId"`
	Source                      string `json:"source"`
	UserAgent                   string `json:"userAgent"`
	ClientCountryName           string `json:"clientCountryName"`
}

// firewallResponse maps the GraphQL response for firewallEventsAdaptive queries.
type firewallResponse struct {
	Viewer struct {
		Zones []struct {
			FirewallEventsAdaptive []FirewallEvent `json:"firewallEventsAdaptive"`
		} `json:"zones"`
	} `json:"viewer"`
}

// firewallQuery fetches individual firewall/WAF events ordered by time.
const firewallQuery = `query ($zoneId: String!, $since: String!, $until: String!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      firewallEventsAdaptive(
        filter: {datetime_gt: $since, datetime_leq: $until}
        limit: 10000
        orderBy: [datetime_ASC]
      ) {
        action clientIP clientRequestHTTPHost clientRequestHTTPMethodName
        clientRequestPath clientRequestQuery datetime rayName ruleId
        source userAgent clientCountryName
      }
    }
  }
}`

// QueryFirewallEvents fetches firewall events for the given zone and time range.
func (c *Client) QueryFirewallEvents(ctx context.Context, zoneID string, since, until time.Time) ([]FirewallEvent, error) {
	vars := map[string]any{
		"zoneId": zoneID,
		"since":  since.UTC().Format(time.RFC3339),
		"until":  until.UTC().Format(time.RFC3339),
	}

	body, err := c.doQuery(ctx, zoneID, firewallQuery, vars)
	if err != nil {
		return nil, fmt.Errorf("firewall query: %w", err)
	}

	var resp firewallResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("firewall response parse: %w", err)
	}

	if len(resp.Viewer.Zones) == 0 {
		return nil, nil
	}

	events := resp.Viewer.Zones[0].FirewallEventsAdaptive
	if len(events) >= firewallQueryLimit {
		slog.WarnContext(ctx, "Firewall query hit limit, events may be truncated",
			"zone_id", zoneID, "limit", firewallQueryLimit, "count", len(events))
	}

	return events, nil
}
