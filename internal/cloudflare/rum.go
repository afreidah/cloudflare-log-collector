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
type RUMPageloadDimensions struct {
	RequestPath string `json:"requestPath"`
	CountryName string `json:"countryName"`
	DeviceType  string `json:"deviceType"`
	RefererHost string `json:"refererHost"`
}

// RUMPageloadSum holds the aggregated sums for RUM page loads. Visits counts
// sessions (entry page loads), which is a subset of Count (total page views).
type RUMPageloadSum struct {
	Visits int64 `json:"visits"`
}

// RUMWebVitalsGroup represents an aggregated Core Web Vitals data point from
// Cloudflare Web Analytics (RUM), grouped by device type.
type RUMWebVitalsGroup struct {
	Count      int                    `json:"count"`
	Dimensions RUMWebVitalsDimensions `json:"dimensions"`
	Quantiles  RUMWebVitalsQuantiles  `json:"quantiles"`
}

// RUMWebVitalsDimensions holds the grouping dimensions for RUM web vitals.
type RUMWebVitalsDimensions struct {
	DeviceType string `json:"deviceType"`
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
var rumPageloadDataset = dataset{
	field: "rumPageloadEventsAdaptiveGroups",
	scope: "accounts",
	limit: rumQueryLimit,
	query: `query ($accountId: String!, $siteTag: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    accounts(filter: {accountTag: $accountId}) {
      rumPageloadEventsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, siteTag: $siteTag}
        limit: $limit
      ) {
        count
        dimensions {
          requestPath
          countryName
          deviceType
          refererHost
        }
        sum {
          visits
        }
      }
    }
  }
}`,
}

// rumWebVitalsDataset fetches Core Web Vitals p75 quantiles grouped by device
// for one Web Analytics site.
var rumWebVitalsDataset = dataset{
	field: "rumWebVitalsEventsAdaptiveGroups",
	scope: "accounts",
	limit: rumQueryLimit,
	query: `query ($accountId: String!, $siteTag: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    accounts(filter: {accountTag: $accountId}) {
      rumWebVitalsEventsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, siteTag: $siteTag}
        limit: $limit
      ) {
        count
        dimensions {
          deviceType
        }
        quantiles {
          largestContentfulPaintP75
          interactionToNextPaintP75
          firstInputDelayP75
          firstContentfulPaintP75
          timeToFirstByteP75
          cumulativeLayoutShiftP75
        }
      }
    }
  }
}`,
}

// QueryRUMPageloads fetches browser-side page-load groups for the given Web
// Analytics site and time range.
func (c *Client) QueryRUMPageloads(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]RUMPageloadGroup, error) {
	return queryDataset[RUMPageloadGroup](ctx, c, rumPageloadDataset, siteTag, since, until,
		map[string]any{"accountId": accountID, "siteTag": siteTag})
}

// QueryRUMWebVitals fetches Core Web Vitals quantiles for the given Web
// Analytics site and time range.
func (c *Client) QueryRUMWebVitals(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]RUMWebVitalsGroup, error) {
	return queryDataset[RUMWebVitalsGroup](ctx, c, rumWebVitalsDataset, siteTag, since, until,
		map[string]any{"accountId": accountID, "siteTag": siteTag})
}
