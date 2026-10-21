// -------------------------------------------------------------------------------
// Cloudflare GraphQL Transport
//
// Authors: Alex Freidah, Aaron Florey
//
// The GraphQL transport every Analytics dataset query runs on (see dataset.go
// for how those queries are built). doQuery posts a query within a client
// span, records failures on it, and returns the response's data field.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// graphQLRequest is the JSON payload sent to the Cloudflare GraphQL API.
type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphQLResponse is the top-level envelope returned by the Cloudflare GraphQL API.
type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors apiErrors       `json:"errors"`
}

// doQuery sends a GraphQL request within a client span and returns the data
// field from the response; any failure marks the span failed. The scope
// argument (a zone or site tag) is recorded on the span only.
func (c *Client) doQuery(ctx context.Context, scope, query string, variables map[string]any) (json.RawMessage, error) {
	ctx, span := startAPISpan(ctx, "cloudflare.graphql",
		attribute.String("cflog.scope", scope),
		attribute.String("cflog.since", fmt.Sprint(variables["since"])),
		attribute.String("cflog.until", fmt.Sprint(variables["until"])),
	)
	defer span.End()

	data, err := c.postQuery(ctx, query, variables)
	if err != nil {
		telemetry.FailSpan(span, err)
	}
	return data, err
}

// postQuery posts one GraphQL request and returns its data field, turning a
// GraphQL-level error in the response into an error.
func (c *Client) postQuery(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	payload, err := json.Marshal(graphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	respBody, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, err
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		return nil, fmt.Errorf("parse graphql response: %w", err)
	}

	if err := gqlResp.Errors.err("graphql"); err != nil {
		return nil, err
	}

	return gqlResp.Data, nil
}
