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
	"time"
)

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

// firewallDataset fetches individual firewall/WAF events ordered by time, so
// the collector's seek cursor can advance to the last event returned.
var firewallDataset = newDataset[FirewallEvent](datasetSpec{
	field:   "firewallEventsAdaptive",
	scope:   zoneScope,
	orderBy: "[datetime_ASC]",
	limit:   10000,
})

// QueryFirewallEvents fetches firewall events for the given zone and time range.
func (c *Client) QueryFirewallEvents(ctx context.Context, zoneID string, since, until time.Time) ([]FirewallEvent, error) {
	return queryDataset(ctx, c, firewallDataset, zoneID, since, until, map[string]any{"zoneId": zoneID})
}
