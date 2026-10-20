// -------------------------------------------------------------------------------
// Cloudflare GraphQL Transport
//
// Authors: Alex Freidah, Aaron Florey
//
// Shared GraphQL machinery that the zone- and account-scoped Analytics queries
// (firewall, http, rum) build on. A dataset describes one query; queryDataset
// runs it over a time window, extracts its rows from the response, and warns
// when a full page suggests truncation. doQuery is the transport underneath:
// it posts a query, records failures on the active span, and returns the data
// field.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// -------------------------------------------------------------------------
// DATASETS
// -------------------------------------------------------------------------

// dataset describes one Analytics dataset query: the document to send, where
// its rows sit in the response, and how many to request.
//
// field is the dataset's field name in both the query and the response (e.g.
// "firewallEventsAdaptive"). scope is the viewer node it lives under: "zones"
// for the zone-scoped datasets, "accounts" for the account-scoped RUM datasets.
// query is the GraphQL document; it must declare $since, $until, and
// $limit: uint64!, plus whatever scope variables its caller passes. limit is
// the most rows requested per query - a full page means the window may hold
// more, so it is logged as possibly truncated.
type dataset struct {
	field string
	scope string
	query string
	limit int
}

// queryDataset runs ds over the (since, until] window and returns its rows
// decoded as T. vars carries the dataset's scope variables (zone ID, or account
// ID and site tag); the window and limit are added here. scopeTag - the zone
// or site tag - labels the span and the truncation warning. A response with no
// zone/account node returns nil rows and no error.
func queryDataset[T any](ctx context.Context, c *Client, ds dataset, scopeTag string, since, until time.Time, vars map[string]any) ([]T, error) {
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

	nodes := resp.Viewer[ds.scope]
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
// TRANSPORT
// -------------------------------------------------------------------------

// graphQLRequest is the JSON payload sent to the Cloudflare GraphQL API.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphQLResponse is the top-level envelope returned by the Cloudflare GraphQL API.
type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// doQuery sends a GraphQL request and returns the data field from the response.
// The scope argument (a zone or site tag) is recorded on the span only.
func (c *Client) doQuery(ctx context.Context, scope, query string, variables map[string]any) (json.RawMessage, error) {
	ctx, span := telemetry.StartClientSpan(ctx, "cloudflare.graphql",
		attribute.String("peer.service", "cloudflare-api"),
		attribute.String("server.address", "api.cloudflare.com"),
		attribute.String("cflog.scope", scope),
		attribute.String("cflog.since", fmt.Sprint(variables["since"])),
		attribute.String("cflog.until", fmt.Sprint(variables["until"])),
	)
	defer span.End()

	payload, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		telemetry.FailSpan(span, err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	respBody, statusCode, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiToken)
		return req, nil
	})
	if err != nil {
		telemetry.FailSpan(span, err)
		return nil, err
	}

	if statusCode != http.StatusOK {
		err := fmt.Errorf("HTTP %d: %s", statusCode, string(respBody))
		telemetry.FailSpan(span, err)
		return nil, err
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		telemetry.FailSpan(span, err)
		return nil, fmt.Errorf("parse graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		err := fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
		telemetry.FailSpan(span, err)
		return nil, err
	}

	return gqlResp.Data, nil
}
