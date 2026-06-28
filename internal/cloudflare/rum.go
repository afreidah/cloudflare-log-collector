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
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// rumQueryLimit is the maximum number of RUM groups returned per query.
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

// rumPageloadResponse maps the GraphQL response for rumPageloadEventsAdaptiveGroups
// queries. The RUM datasets are account-scoped (under viewer.accounts), unlike
// the zone-scoped firewall and HTTP datasets.
type rumPageloadResponse struct {
	Viewer struct {
		Accounts []struct {
			RUMPageloadEventsAdaptiveGroups []RUMPageloadGroup `json:"rumPageloadEventsAdaptiveGroups"`
		} `json:"accounts"`
	} `json:"viewer"`
}

// rumWebVitalsResponse maps the GraphQL response for rumWebVitalsEventsAdaptiveGroups queries.
type rumWebVitalsResponse struct {
	Viewer struct {
		Accounts []struct {
			RUMWebVitalsEventsAdaptiveGroups []RUMWebVitalsGroup `json:"rumWebVitalsEventsAdaptiveGroups"`
		} `json:"accounts"`
	} `json:"viewer"`
}

// rumPageloadQuery fetches browser-side page-load groups for one Web Analytics
// site. RUM datasets are account-scoped and filtered by siteTag.
const rumPageloadQuery = `query ($accountId: String!, $siteTag: String!, $since: String!, $until: String!) {
  viewer {
    accounts(filter: {accountTag: $accountId}) {
      rumPageloadEventsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, siteTag: $siteTag}
        limit: 5000
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
}`

// rumWebVitalsQuery fetches Core Web Vitals p75 quantiles grouped by device for
// one Web Analytics site.
const rumWebVitalsQuery = `query ($accountId: String!, $siteTag: String!, $since: String!, $until: String!) {
  viewer {
    accounts(filter: {accountTag: $accountId}) {
      rumWebVitalsEventsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, siteTag: $siteTag}
        limit: 5000
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
}`

// QueryRUMPageloads fetches browser-side page-load groups for the given Web
// Analytics site and time range.
func (c *Client) QueryRUMPageloads(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]RUMPageloadGroup, error) {
	vars := map[string]any{
		"accountId": accountID,
		"siteTag":   siteTag,
		"since":     since.UTC().Format(time.RFC3339),
		"until":     until.UTC().Format(time.RFC3339),
	}

	body, err := c.doQuery(ctx, siteTag, rumPageloadQuery, vars)
	if err != nil {
		return nil, fmt.Errorf("rum pageload query: %w", err)
	}

	var resp rumPageloadResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("rum pageload response parse: %w", err)
	}

	if len(resp.Viewer.Accounts) == 0 {
		return nil, nil
	}

	groups := resp.Viewer.Accounts[0].RUMPageloadEventsAdaptiveGroups
	if len(groups) >= rumQueryLimit {
		slog.WarnContext(ctx, "RUM pageload query hit limit, groups may be truncated",
			"site_tag", siteTag, "limit", rumQueryLimit, "count", len(groups))
	}

	return groups, nil
}

// QueryRUMWebVitals fetches Core Web Vitals quantiles for the given Web
// Analytics site and time range.
func (c *Client) QueryRUMWebVitals(ctx context.Context, accountID, siteTag string, since, until time.Time) ([]RUMWebVitalsGroup, error) {
	vars := map[string]any{
		"accountId": accountID,
		"siteTag":   siteTag,
		"since":     since.UTC().Format(time.RFC3339),
		"until":     until.UTC().Format(time.RFC3339),
	}

	body, err := c.doQuery(ctx, siteTag, rumWebVitalsQuery, vars)
	if err != nil {
		return nil, fmt.Errorf("rum web vitals query: %w", err)
	}

	var resp rumWebVitalsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("rum web vitals response parse: %w", err)
	}

	if len(resp.Viewer.Accounts) == 0 {
		return nil, nil
	}

	groups := resp.Viewer.Accounts[0].RUMWebVitalsEventsAdaptiveGroups
	if len(groups) >= rumQueryLimit {
		slog.WarnContext(ctx, "RUM web vitals query hit limit, groups may be truncated",
			"site_tag", siteTag, "limit", rumQueryLimit, "count", len(groups))
	}

	return groups, nil
}
