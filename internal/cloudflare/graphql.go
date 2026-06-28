// -------------------------------------------------------------------------------
// Cloudflare GraphQL Transport
//
// Authors: Alex Freidah, Aaron Florey
//
// Shared GraphQL request/response envelope and the executor that the zone- and
// account-scoped Analytics queries (firewall, http, rum) build on. Posts a
// query, records the HTTP status on the active span, and returns the data field.
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
	"go.opentelemetry.io/otel/codes"
)

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
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	if statusCode != http.StatusOK {
		err := fmt.Errorf("HTTP %d: %s", statusCode, string(respBody))
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(respBody, &gqlResp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("parse graphql response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		err := fmt.Errorf("graphql error: %s", gqlResp.Errors[0].Message)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	return gqlResp.Data, nil
}
