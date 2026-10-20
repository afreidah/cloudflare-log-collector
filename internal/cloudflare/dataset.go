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
// dataset, written once. queryDataset runs a dataset over a time window and
// extracts its rows from the response.
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

// -------------------------------------------------------------------------
// DATASETS
// -------------------------------------------------------------------------

// datasetSpec is what differs between dataset queries. field is the dataset's
// field name in both the query and the response (e.g. "firewallEventsAdaptive")
// and scope the node it lives under. vars names any extra String! variables
// the query declares (e.g. "siteTag"), and filters any extra terms added to the
// window filter (e.g. "siteTag: $siteTag", "bot: 0"). orderBy, if set, is the
// ordering argument (e.g. "[datetime_ASC]"). limit is the most rows requested
// per query - a full page means the window may hold more, so it is logged as
// possibly truncated.
type datasetSpec struct {
	field   string
	scope   queryScope
	vars    []string
	filters []string
	orderBy string
	limit   int
}

// datasetInfo is a built dataset query: its field and node, its row limit, and
// the GraphQL document, which declares $since, $until, $limit, the scope
// variable, and any extra variables.
type datasetInfo struct {
	field string
	node  string
	limit int
	query string
}

// dataset is a built query whose rows decode into T. Binding T here means a
// dataset can only be run for the row type its query was generated from.
type dataset[T any] struct {
	datasetInfo
}

// newDataset builds the query for spec with the selection set generated from
// T, once, so polls reuse the finished document.
func newDataset[T any](spec datasetSpec) dataset[T] {
	return dataset[T]{datasetInfo{
		field: spec.field,
		node:  spec.scope.node,
		limit: spec.limit,
		query: buildQuery(spec, reflect.TypeFor[T]()),
	}}
}

// queryDataset runs ds over the (since, until] window and returns its rows.
// vars carries the dataset's scope variables (zone ID, or account ID and site
// tag); the window and limit are added here. scopeTag - the zone or site tag -
// labels the span and the truncation warning. A response with no zone/account
// node returns nil rows and no error.
func queryDataset[T any](ctx context.Context, c *Client, ds dataset[T], scopeTag string, since, until time.Time, vars map[string]any) ([]T, error) {
	vars["since"] = since.UTC().Format(time.RFC3339)
	vars["until"] = until.UTC().Format(time.RFC3339)
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

	filter := append([]string{"datetime_gt: $since", "datetime_leq: $until"}, spec.filters...)

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
