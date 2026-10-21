// -------------------------------------------------------------------------------
// Cloudflare Analytics Dataset Query Tests
//
// Author: Alex Freidah
//
// Golden tests pinning the exact GraphQL document generated for every dataset,
// so a change to a row struct or the query skeleton shows up as a reviewable
// diff here. Also covers the selection builder's rules: fields named by JSON
// tag, nested structs as sub-selections, and untagged, "-", and unexported
// fields skipped; and that each latency quantile set reads back as the right
// percentiles.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"reflect"
	"strings"
	"testing"
)

func TestDatasetQueries_Golden(t *testing.T) {
	tests := map[string]struct {
		got  string
		want string
	}{
		"firewall": {firewallDataset.query, `query ($zoneId: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      firewallEventsAdaptive(
        filter: {datetime_gt: $since, datetime_leq: $until}
        limit: $limit
        orderBy: [datetime_ASC]
      ) {
        action
        clientIP
        clientRequestHTTPHost
        clientRequestHTTPMethodName
        clientRequestPath
        clientRequestQuery
        datetime
        rayName
        ruleId
        source
        userAgent
        clientCountryName
      }
    }
  }
}`},
		"http": {httpDataset.query, `query ($zoneId: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      httpRequestsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until}
        limit: $limit
      ) {
        count
        dimensions {
          datetimeMinute
          clientRequestHTTPHost
          clientRequestHTTPMethodName
          edgeResponseStatus
          originResponseStatus
          cacheStatus
          requestSource
          clientCountryName
        }
        sum {
          edgeResponseBytes
        }
      }
    }
  }
}`},
		"http origin latency": {httpOriginLatencyDataset.query, `query ($zoneId: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      httpRequestsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, requestSource: "eyeball", originResponseStatus_gt: 0}
        limit: $limit
      ) {
        count
        dimensions {
          clientRequestHTTPHost
        }
        quantiles {
          originResponseDurationMsP50
          originResponseDurationMsP95
          originResponseDurationMsP99
        }
      }
    }
  }
}`},
		"http edge latency": {httpEdgeLatencyDataset.query, `query ($zoneId: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      httpRequestsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, requestSource: "eyeball"}
        limit: $limit
      ) {
        count
        dimensions {
          clientRequestHTTPHost
        }
        quantiles {
          edgeTimeToFirstByteMsP50
          edgeTimeToFirstByteMsP95
          edgeTimeToFirstByteMsP99
        }
      }
    }
  }
}`},
		"rum pageloads": {rumPageloadDataset.query, `query ($accountId: String!, $siteTag: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    accounts(filter: {accountTag: $accountId}) {
      rumPageloadEventsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, siteTag: $siteTag}
        limit: $limit
      ) {
        count
        dimensions {
          datetimeMinute
          requestHost
          requestPath
          countryName
          deviceType
          refererHost
          bot
        }
        sum {
          visits
        }
      }
    }
  }
}`},
		"rum web vitals": {rumWebVitalsDataset.query, `query ($accountId: String!, $siteTag: String!, $since: String!, $until: String!, $limit: uint64!) {
  viewer {
    accounts(filter: {accountTag: $accountId}) {
      rumWebVitalsEventsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until, siteTag: $siteTag, bot: 0}
        limit: $limit
      ) {
        count
        dimensions {
          requestHost
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
}`},
	}

	for name, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s query:\n--- got ---\n%s\n--- want ---\n%s", name, tt.got, tt.want)
		}
	}
}

func TestLatencyQuantiles_Percentiles(t *testing.T) {
	want := Percentiles{P50: 1, P95: 2, P99: 3}
	for name, q := range map[string]LatencyQuantiles{
		"origin": OriginLatencyQuantiles{OriginResponseDurationMsP50: 1, OriginResponseDurationMsP95: 2, OriginResponseDurationMsP99: 3},
		"edge":   EdgeLatencyQuantiles{EdgeTimeToFirstByteMsP50: 1, EdgeTimeToFirstByteMsP95: 2, EdgeTimeToFirstByteMsP99: 3},
	} {
		if got := q.Percentiles(); got != want {
			t.Errorf("%s Percentiles() = %+v, want %+v", name, got, want)
		}
	}
}

// selectionRow exercises every selection rule: a tagged leaf, a tag with
// options, a nested struct, a pointer to a struct, an untagged field, a "-"
// field, and an unexported field.
type selectionRow struct {
	Count    int              `json:"count"`
	Name     string           `json:"name,omitempty"`
	Nested   selectionNested  `json:"nested"`
	Pointer  *selectionNested `json:"pointer"`
	Untagged string
	Skipped  string `json:"-"`
	hidden   string //nolint:unused // proves unexported fields are skipped
}

// selectionNested is a nested selection with one leaf.
type selectionNested struct {
	Leaf string `json:"leaf"`
}

func TestWriteSelection_Rules(t *testing.T) {
	var b strings.Builder
	writeSelection(&b, reflect.TypeFor[selectionRow](), "")

	want := "count\nname\nnested {\n  leaf\n}\npointer {\n  leaf\n}\n"
	if b.String() != want {
		t.Errorf("selection:\n%s\nwant:\n%s", b.String(), want)
	}
}
