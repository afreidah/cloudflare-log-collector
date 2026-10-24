// -------------------------------------------------------------------------------
// Cloudflare Analytics Datasets
//
// Author: Alex Freidah
//
// Every Analytics dataset query is built here from a small spec and the Go type
// its rows decode into. The selection set comes from the row type's JSON tags,
// so the row struct is the single source of truth for what is requested: a
// field added to the struct is always asked for, and the query cannot silently
// drift from the struct. The rest of the document - variables, the zone or
// account node, the window filter, the limit - is the same skeleton for every
// dataset, written once; only which filter keys bound the window differs, for
// the hourly and daily rollups. queryDataset runs a dataset over a time window and
// extracts its rows from the response; queryZone and querySite supply the
// zone or site variables for it.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"
)

// -------------------------------------------------------------------------
// SCOPES
// -------------------------------------------------------------------------

// queryScope is the viewer node a dataset lives under. node is the response
// and query field ("zones" or "accounts"), filterKey the tag it is filtered by,
// and variable the query variable carrying that tag.
type queryScope struct {
	node      string
	filterKey string
	variable  string
}

// zoneScope holds the zone-scoped datasets (firewall, HTTP); accountScope holds
// the account-scoped ones (RUM).
var (
	zoneScope    = queryScope{node: "zones", filterKey: "zoneTag", variable: "zoneId"}
	accountScope = queryScope{node: "accounts", filterKey: "accountTag", variable: "accountId"}
)

// siteTagVar is the variable a Web Analytics dataset is filtered by, and
// siteFilter its filter term; every site dataset declares and applies both.
const (
	siteTagVar = "siteTag"
	siteFilter = "siteTag: $siteTag"
)

// -------------------------------------------------------------------------
// WINDOWS
// -------------------------------------------------------------------------

// windowFilter is how a dataset's time window is filtered: lower and upper are
// the filter keys bounding it by $since and $until, and layout formats those
// times. Adaptive datasets read (since, until] so cursor windows never
// overlap; rollups read [since, until) to select exactly one bucket.
type windowFilter struct {
	lower  string
	upper  string
	layout string
}

// afterUntil is the (since, until] window of the adaptive datasets, and the
// default. hourBucket and dayBucket are the [since, until) windows of the
// hourly and daily rollups; the daily one filters on dates.
var (
	afterUntil = windowFilter{lower: "datetime_gt", upper: "datetime_leq", layout: time.RFC3339}
	hourBucket = windowFilter{lower: "datetime_geq", upper: "datetime_lt", layout: time.RFC3339}
	dayBucket  = windowFilter{lower: "date_geq", upper: "date_lt", layout: time.DateOnly}
)

// -------------------------------------------------------------------------
// DATASETS
// -------------------------------------------------------------------------

// datasetSpec is what differs between dataset queries. field is the dataset's
// field name in both the query and the response (e.g. "firewallEventsAdaptive")
// and scope the node it lives under. window is how its time window is
// filtered; left unset it is afterUntil. vars names any extra String!
// variables the query declares (e.g. "siteTag"), and filters any extra terms
// added to the window filter (e.g. "siteTag: $siteTag", "bot: 0"). orderBy, if
// set, is the ordering argument (e.g. "[datetime_ASC]"). limit is the most rows
// requested per query - a full page means the window may hold more, so it is
// logged as possibly truncated.
type datasetSpec struct {
	field   string
	scope   queryScope
	window  windowFilter
	vars    []string
	filters []string
	orderBy string
	limit   int
}

// datasetInfo is a built dataset query: its field and node, its row limit, the
// layout its window bounds are formatted with, and the GraphQL document, which
// declares $since, $until, $limit, the scope variable, and any extra
// variables.
type datasetInfo struct {
	field  string
	node   string
	limit  int
	layout string
	query  string
}

// dataset is a built query whose rows decode into T. Binding T here means a
// dataset can only be run for the row type its query was generated from.
type dataset[T any] struct {
	datasetInfo
}

// newDataset builds the query for spec with the selection set generated from
// T, once, so polls reuse the finished document.
func newDataset[T any](spec datasetSpec) dataset[T] {
	if spec.window == (windowFilter{}) {
		spec.window = afterUntil
	}
	return dataset[T]{datasetInfo{
		field:  spec.field,
		node:   spec.scope.node,
		limit:  spec.limit,
		layout: spec.window.layout,
		query:  buildQuery(spec, reflect.TypeFor[T]()),
	}}
}

// queryDataset runs ds over its window from since to until and returns its
// rows. vars carries the dataset's scope variables (zone ID, or account ID and
// site tag); the window, formatted for the dataset, and limit are added here.
// scopeTag - the zone or site tag - labels the span and the truncation
// warning. A response with no zone/account node returns nil rows and no error.
func queryDataset[T any](ctx context.Context, c *Client, ds dataset[T], scopeTag string, since, until time.Time, vars map[string]any) ([]T, error) {
	vars["since"] = since.UTC().Format(ds.layout)
	vars["until"] = until.UTC().Format(ds.layout)
	vars["limit"] = ds.limit

	body, err := c.doQuery(ctx, scopeTag, ds.query, vars)
	if err != nil {
		return nil, fmt.Errorf("%s query: %w", ds.field, err)
	}

	var resp struct {
		Viewer map[string][]map[string]json.RawMessage `json:"viewer"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%s response parse: %w", ds.field, err)
	}

	nodes := resp.Viewer[ds.node]
	if len(nodes) == 0 {
		return nil, nil
	}

	var rows []T
	if raw, ok := nodes[0][ds.field]; ok {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("%s response parse: %w", ds.field, err)
		}
	}

	if len(rows) >= ds.limit {
		slog.WarnContext(ctx, "Cloudflare query hit limit, results may be truncated",
			"dataset", ds.field, "scope", scopeTag, "limit", ds.limit, "count", len(rows))
	}

	return rows, nil
}

// queryZone runs a zone-scoped dataset for one zone over its window from
// since to until.
func queryZone[T any](ctx context.Context, c *Client, ds dataset[T], zoneID string, since, until time.Time) ([]T, error) {
	return queryDataset(ctx, c, ds, zoneID, since, until, map[string]any{zoneScope.variable: zoneID})
}

// querySite runs an account-scoped Web Analytics dataset for one site over its
// window from since to until. The dataset's spec must declare and filter on
// $siteTag.
func querySite[T any](ctx context.Context, c *Client, ds dataset[T], accountID, siteTag string, since, until time.Time) ([]T, error) {
	return queryDataset(ctx, c, ds, siteTag, since, until,
		map[string]any{accountScope.variable: accountID, siteTagVar: siteTag})
}

// -------------------------------------------------------------------------
// QUERY BUILDING
// -------------------------------------------------------------------------

// buildQuery writes the GraphQL document for spec, selecting the fields of
// rowType.
func buildQuery(spec datasetSpec, rowType reflect.Type) string {
	decls := []string{"$" + spec.scope.variable + ": String!"}
	for _, v := range spec.vars {
		decls = append(decls, "$"+v+": String!")
	}
	decls = append(decls, "$since: String!", "$until: String!", "$limit: uint64!")

	filter := append([]string{spec.window.lower + ": $since", spec.window.upper + ": $until"}, spec.filters...)

	var b strings.Builder
	fmt.Fprintf(&b, "query (%s) {\n", strings.Join(decls, ", "))
	b.WriteString("  viewer {\n")
	fmt.Fprintf(&b, "    %s(filter: {%s: $%s}) {\n", spec.scope.node, spec.scope.filterKey, spec.scope.variable)
	fmt.Fprintf(&b, "      %s(\n", spec.field)
	fmt.Fprintf(&b, "        filter: {%s}\n", strings.Join(filter, ", "))
	b.WriteString("        limit: $limit\n")
	if spec.orderBy != "" {
		fmt.Fprintf(&b, "        orderBy: %s\n", spec.orderBy)
	}
	b.WriteString("      ) {\n")
	writeSelection(&b, rowType, "        ")
	b.WriteString("      }\n")
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("}")
	return b.String()
}

// writeSelection writes one line per field of t, named by its JSON tag; a field
// whose type is a struct becomes a nested selection. Fields without a JSON name
// (untagged, "-", or unexported) are not part of the response and are skipped.
func writeSelection(b *strings.Builder, t reflect.Type, indent string) {
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "" || name == "-" {
			continue
		}

		ft := f.Type
		if ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			fmt.Fprintf(b, "%s%s {\n", indent, name)
			writeSelection(b, ft, indent+"  ")
			fmt.Fprintf(b, "%s}\n", indent)
			continue
		}
		fmt.Fprintf(b, "%s%s\n", indent, name)
	}
}
