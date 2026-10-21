// -------------------------------------------------------------------------------
// Cloudflare RUM / Web Analytics Queries
//
// Author: Alex Freidah
//
// Queries the account-scoped rumPageloadEventsAdaptiveGroups and
// rumWebVitalsEventsAdaptiveGroups GraphQL datasets for browser-side Real User
// Monitoring data. Unlike the zone-scoped firewall and HTTP datasets, these live
// under viewer.accounts and are filtered by the Web Analytics siteTag.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"time"
)

// rumQueryLimit is the most groups requested per RUM query, shared by both RUM
// datasets.
const rumQueryLimit = 5000

// RUMPageloadGroup represents an aggregated browser-side page-load data point
// from Cloudflare Web Analytics (RUM).
type RUMPageloadGroup struct {
	Count      int                   `json:"count"`
	Dimensions RUMPageloadDimensions `json:"dimensions"`
	Sum        RUMPageloadSum        `json:"sum"`
}

// RUMPageloadDimensions holds the grouping dimensions for RUM page loads.
// DatetimeMinute is the minute the views fall in. RequestHost is the website
// viewed, which tells apart sites sharing one Web Analytics site tag (a site
// covers every host in its zone). Bot is Cloudflare's known-bot flag: 0 for a
// real browser, non-zero for a detected bot.
type RUMPageloadDimensions struct {
	DatetimeMinute string `json:"datetimeMinute"`
	RequestHost    string `json:"requestHost"`
	RequestPath    string `json:"requestPath"`
	CountryName    string `json:"countryName"`
	DeviceType     string `json:"deviceType"`
	RefererHost    string `json:"refererHost"`
	Bot            int    `json:"bot"`
}

// RUMPageloadSum holds the aggregated sums for RUM page loads. Visits counts
// sessions (entry page loads), which is a subset of Count (total page views).
type RUMPageloadSum struct {
	Visits int64 `json:"visits"`
}

// RUMWebVitalsGroup represents an aggregated Core Web Vitals data point from
// Cloudflare Web Analytics (RUM), grouped by host and device type.
type RUMWebVitalsGroup struct {
	Count      int                    `json:"count"`
	Dimensions RUMWebVitalsDimensions `json:"dimensions"`
	Quantiles  RUMWebVitalsQuantiles  `json:"quantiles"`
}

// RUMWebVitalsDimensions holds the grouping dimensions for RUM web vitals.
type RUMWebVitalsDimensions struct {
	RequestHost string `json:"requestHost"`
	DeviceType  string `json:"deviceType"`
}

// RUMWebVitalsQuantiles holds Core Web Vitals at the p75 quantile. The
// time-based vitals are in microseconds; CumulativeLayoutShiftP75 is a unitless
// layout-shift score. A value of -1 is the API's "no data" sentinel (e.g. no
// measured first-input delay when there was no user interaction).
type RUMWebVitalsQuantiles struct {
	LargestContentfulPaintP75 float64 `json:"largestContentfulPaintP75"`
	InteractionToNextPaintP75 float64 `json:"interactionToNextPaintP75"`
	FirstInputDelayP75        float64 `json:"firstInputDelayP75"`
	FirstContentfulPaintP75   float64 `json:"firstContentfulPaintP75"`
	TimeToFirstByteP75        float64 `json:"timeToFirstByteP75"`
	CumulativeLayoutShiftP75  float64 `json:"cumulativeLayoutShiftP75"`
}

// rumPageloadDataset fetches browser-side page-load groups for one Web
// Analytics site. RUM datasets are account-scoped (under viewer.accounts),
// unlike the zone-scoped firewall and HTTP datasets, and filtered by siteTag.
// Known-bot rows are kept so they reach Loki; the collector leaves them out of
// its counters.
var rumPageloadDataset = newDataset[RUMPageloadGroup](datasetSpec{
	field:   "rumPageloadEventsAdaptiveGroups",
	scope:   accountScope,
	vars:    []string{siteTagVar},
	filters: []string{siteFilter},
	limit:   rumQueryLimit,
})

// rumWebVitalsDataset fetches Core Web Vitals p75 quantiles grouped by host and
// device for one Web Analytics site. Known bots are filtered out in the query
// rather than afterwards, because a quantile cannot be split once computed.
var rumWebVitalsDataset = newDataset[RUMWebVitalsGroup](datasetSpec{
	field:   "rumWebVitalsEventsAdaptiveGroups",
	scope:   accountScope,
	vars:    []string{siteTagVar},
	filters: []string{siteFilter, "bot: 0"},
	limit:   rumQueryLimit,
})

// QueryRUMPageloads fetches browser-side page-load groups for the given Web
// Analytics site and time range.
func (c *Client) QueryRUMPageloads(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]RUMPageloadGroup, error) {
	return querySite(ctx, c, rumPageloadDataset, accountID, siteTag, since, until)
}

// QueryRUMWebVitals fetches Core Web Vitals quantiles for the given Web
// Analytics site and time range.
func (c *Client) QueryRUMWebVitals(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]RUMWebVitalsGroup, error) {
	return querySite(ctx, c, rumWebVitalsDataset, accountID, siteTag, since, until)
}
