// -------------------------------------------------------------------------------
// Cloudflare GraphQL Client
//
// Author: Alex Freidah
//
// HTTP client for the Cloudflare GraphQL Analytics API. Queries firewall events
// and HTTP traffic statistics. Handles rate limiting, response parsing, and
// seek-based pagination via datetime filters.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	// graphQLEndpoint is the Cloudflare Analytics GraphQL API URL.
	graphQLEndpoint = "https://api.cloudflare.com/client/v4/graphql"

	// auditLogsEndpoint is the Cloudflare Audit Logs REST API URL template.
	auditLogsEndpoint = "https://api.cloudflare.com/client/v4/accounts/%s/logs/audit"

	// firewallQueryLimit is the maximum number of firewall events returned per query.
	firewallQueryLimit = 10000

	// httpQueryLimit is the maximum number of HTTP request groups returned per query.
	httpQueryLimit = 5000

	// auditQueryLimit is the maximum number of audit log entries requested per page.
	auditQueryLimit = 1000

	// maxResponseBytes caps the size of response bodies read from the API to
	// guard against unbounded memory allocation.
	maxResponseBytes = 10 << 20 // 10 MB

	// maxRetries is the number of additional attempts after the initial request
	// for retryable HTTP status codes (429, 502, 503, 504).
	maxRetries = 3

	// retryBaseDelay is the initial backoff duration before the first retry.
	retryBaseDelay = 1 * time.Second
)

// -------------------------------------------------------------------------
// CLIENT
// -------------------------------------------------------------------------

// Client talks to the Cloudflare GraphQL Analytics API.
type Client struct {
	apiToken      string
	endpoint      string
	auditEndpoint string
	httpClient    *http.Client
}

// NewClient creates a Cloudflare GraphQL client.
func NewClient(apiToken string) *Client {
	return &Client{
		apiToken:      apiToken,
		endpoint:      graphQLEndpoint,
		auditEndpoint: auditLogsEndpoint,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// NewTestClient creates a client pointing at a custom endpoint for testing.
func NewTestClient(endpoint, apiToken string) *Client {
	return &Client{
		apiToken:      apiToken,
		endpoint:      endpoint,
		auditEndpoint: endpoint + "/accounts/%s/logs/audit",
		httpClient:    &http.Client{Timeout: 5 * time.Second},
	}
}

// -------------------------------------------------------------------------
// RESPONSE TYPES
// -------------------------------------------------------------------------

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

// HTTPRequestGroup represents an aggregated HTTP traffic data point.
type HTTPRequestGroup struct {
	Count      int                   `json:"count"`
	Dimensions HTTPRequestDimensions `json:"dimensions"`
	Sum        HTTPRequestSum        `json:"sum"`
}

// HTTPRequestDimensions holds the grouping dimensions for HTTP traffic.
type HTTPRequestDimensions struct {
	Datetime                    string `json:"datetime"`
	ClientRequestHTTPMethodName string `json:"clientRequestHTTPMethodName"`
	EdgeResponseStatus          int    `json:"edgeResponseStatus"`
	ClientCountryName           string `json:"clientCountryName"`
}

// HTTPRequestSum holds the aggregated byte counts for HTTP traffic.
type HTTPRequestSum struct {
	EdgeResponseBytes int64 `json:"edgeResponseBytes"`
}

// AuditLogEvent represents a single account audit log entry from Cloudflare.
type AuditLogEvent struct {
	ID        string        `json:"id"`
	Account   AuditAccount  `json:"account"`
	Action    AuditAction   `json:"action"`
	Actor     AuditActor    `json:"actor"`
	Raw       AuditRaw      `json:"raw"`
	Resource  AuditResource `json:"resource"`
	Zone      *AuditZone    `json:"zone,omitempty"`
	AccountID string        `json:"account_id,omitempty"`
}

// AuditAccount contains account information for an audit log entry.
type AuditAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AuditAction describes the action performed in an audit log entry.
type AuditAction struct {
	Description string `json:"description"`
	Result      string `json:"result"`
	Time        string `json:"time"`
	Type        string `json:"type"`
}

// AuditActor describes who performed the action in an audit log entry.
type AuditActor struct {
	ID        string `json:"id"`
	Context   string `json:"context"`
	Email     string `json:"email"`
	IPAddress string `json:"ip_address"`
	TokenID   string `json:"token_id,omitempty"`
	TokenName string `json:"token_name,omitempty"`
	Type      string `json:"type"`
}

// AuditRaw contains raw request/response details for an audit log entry.
type AuditRaw struct {
	CFRayID    string `json:"cf_ray_id"`
	Method     string `json:"method"`
	StatusCode int    `json:"status_code"`
	URI        string `json:"uri"`
	UserAgent  string `json:"user_agent"`
}

// AuditResource describes the resource affected by an audit log action.
type AuditResource struct {
	ID      string `json:"id"`
	Product string `json:"product"`
	Type    string `json:"type"`
}

// AuditZone contains zone information when an audit log entry affects a zone.
type AuditZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// -------------------------------------------------------------------------
// GRAPHQL REQUEST / RESPONSE
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

// firewallResponse maps the GraphQL response for firewallEventsAdaptive queries.
type firewallResponse struct {
	Viewer struct {
		Zones []struct {
			FirewallEventsAdaptive []FirewallEvent `json:"firewallEventsAdaptive"`
		} `json:"zones"`
	} `json:"viewer"`
}

// httpRequestResponse maps the GraphQL response for httpRequestsAdaptiveGroups queries.
type httpRequestResponse struct {
	Viewer struct {
		Zones []struct {
			HTTPRequestsAdaptiveGroups []HTTPRequestGroup `json:"httpRequestsAdaptiveGroups"`
		} `json:"zones"`
	} `json:"viewer"`
}

// auditLogsResponse maps the REST API response for audit logs queries.
type auditLogsResponse struct {
	Success    bool            `json:"success"`
	Result     []AuditLogEvent `json:"result"`
	ResultInfo struct {
		Count  int    `json:"count"`
		Cursor string `json:"cursor"`
	} `json:"result_info"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// -------------------------------------------------------------------------
// QUERIES
// -------------------------------------------------------------------------

// firewallQuery fetches individual firewall/WAF events ordered by time.
const firewallQuery = `query ($zoneId: String!, $since: String!, $until: String!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      firewallEventsAdaptive(
        filter: {datetime_gt: $since, datetime_leq: $until}
        limit: 10000
        orderBy: [datetime_ASC]
      ) {
        action clientIP clientRequestHTTPHost clientRequestHTTPMethodName
        clientRequestPath clientRequestQuery datetime rayName ruleId
        source userAgent clientCountryName
      }
    }
  }
}`

// httpRequestQuery fetches aggregated HTTP traffic grouped by method, status, and country.
const httpRequestQuery = `query ($zoneId: String!, $since: String!, $until: String!) {
  viewer {
    zones(filter: {zoneTag: $zoneId}) {
      httpRequestsAdaptiveGroups(
        filter: {datetime_gt: $since, datetime_leq: $until}
        limit: 5000
      ) {
        count
        dimensions {
          datetime
          clientRequestHTTPMethodName
          edgeResponseStatus
          clientCountryName
        }
        sum {
          edgeResponseBytes
        }
      }
    }
  }
}`

// -------------------------------------------------------------------------
// API METHODS
// -------------------------------------------------------------------------

// QueryFirewallEvents fetches firewall events for the given zone and time range.
func (c *Client) QueryFirewallEvents(ctx context.Context, zoneID string, since, until time.Time) ([]FirewallEvent, error) {
	vars := map[string]any{
		"zoneId": zoneID,
		"since":  since.UTC().Format(time.RFC3339),
		"until":  until.UTC().Format(time.RFC3339),
	}

	body, err := c.doQuery(ctx, zoneID, firewallQuery, vars)
	if err != nil {
		return nil, fmt.Errorf("firewall query: %w", err)
	}

	var resp firewallResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("firewall response parse: %w", err)
	}

	if len(resp.Viewer.Zones) == 0 {
		return nil, nil
	}

	events := resp.Viewer.Zones[0].FirewallEventsAdaptive
	if len(events) >= firewallQueryLimit {
		slog.WarnContext(ctx, "Firewall query hit limit, events may be truncated",
			"zone_id", zoneID, "limit", firewallQueryLimit, "count", len(events))
	}

	return events, nil
}

// QueryHTTPRequests fetches aggregated HTTP traffic stats for the given zone and time range.
func (c *Client) QueryHTTPRequests(ctx context.Context, zoneID string, since, until time.Time) ([]HTTPRequestGroup, error) {
	vars := map[string]any{
		"zoneId": zoneID,
		"since":  since.UTC().Format(time.RFC3339),
		"until":  until.UTC().Format(time.RFC3339),
	}

	body, err := c.doQuery(ctx, zoneID, httpRequestQuery, vars)
	if err != nil {
		return nil, fmt.Errorf("http request query: %w", err)
	}

	var resp httpRequestResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("http request response parse: %w", err)
	}

	if len(resp.Viewer.Zones) == 0 {
		return nil, nil
	}

	groups := resp.Viewer.Zones[0].HTTPRequestsAdaptiveGroups
	if len(groups) >= httpQueryLimit {
		slog.WarnContext(ctx, "HTTP request query hit limit, groups may be truncated",
			"zone_id", zoneID, "limit", httpQueryLimit, "count", len(groups))
	}

	return groups, nil
}

// QueryAuditLogs fetches account audit logs for the given account and time range.
// Paginates through all available results using cursor-based pagination.
func (c *Client) QueryAuditLogs(ctx context.Context, accountID string, since, before time.Time) ([]AuditLogEvent, error) {
	ctx, span := telemetry.StartClientSpan(ctx, "cloudflare.audit_logs",
		attribute.String("peer.service", "cloudflare-api"),
		attribute.String("server.address", "api.cloudflare.com"),
		attribute.String("cflog.account_id", accountID),
		attribute.String("cflog.since", since.UTC().Format(time.RFC3339)),
		attribute.String("cflog.before", before.UTC().Format(time.RFC3339)),
	)
	defer span.End()

	endpoint := fmt.Sprintf(c.auditEndpoint, accountID)
	var allEvents []AuditLogEvent
	cursor := ""

	for {
		events, nextCursor, err := c.doAuditQuery(ctx, endpoint, since, before, cursor)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return nil, fmt.Errorf("audit logs query: %w", err)
		}

		// --- Inject account ID into each event for downstream use ---
		for i := range events {
			events[i].AccountID = accountID
		}

		allEvents = append(allEvents, events...)

		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	span.SetAttributes(attribute.Int("cflog.event_count", len(allEvents)))

	return allEvents, nil
}

// doAuditQuery executes a single page request to the audit logs REST API.
func (c *Client) doAuditQuery(ctx context.Context, endpoint string, since, before time.Time, cursor string) ([]AuditLogEvent, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}

	q := req.URL.Query()
	q.Set("since", since.UTC().Format(time.RFC3339))
	q.Set("before", before.UTC().Format(time.RFC3339))
	q.Set("limit", fmt.Sprintf("%d", auditQueryLimit))
	q.Set("direction", "asc")
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	var respBody []byte
	var statusCode int

	for attempt := range maxRetries + 1 {
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("http request: %w", err)
		}

		respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, "", fmt.Errorf("read response: %w", err)
		}

		statusCode = resp.StatusCode

		if !isRetryable(statusCode) || attempt == maxRetries {
			break
		}

		delay := retryDelay(resp.Header, attempt)
		slog.WarnContext(ctx, "Cloudflare API returned retryable status, backing off",
			"status", statusCode, "attempt", attempt+1, "delay", delay)

		retryTimer := time.NewTimer(delay)
		select {
		case <-retryTimer.C:
		case <-ctx.Done():
			retryTimer.Stop()
			return nil, "", ctx.Err()
		}
	}

	if statusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d: %s", statusCode, string(respBody))
	}

	var auditResp auditLogsResponse
	if err := json.Unmarshal(respBody, &auditResp); err != nil {
		return nil, "", fmt.Errorf("parse audit logs response: %w", err)
	}

	if len(auditResp.Errors) > 0 {
		return nil, "", fmt.Errorf("audit logs error: %s", auditResp.Errors[0].Message)
	}

	return auditResp.Result, auditResp.ResultInfo.Cursor, nil
}

// -------------------------------------------------------------------------
// INTERNAL
// -------------------------------------------------------------------------

// doQuery sends a GraphQL request and returns the data field from the response.
func (c *Client) doQuery(ctx context.Context, zoneID, query string, variables map[string]any) (json.RawMessage, error) {
	ctx, span := telemetry.StartClientSpan(ctx, "cloudflare.graphql",
		attribute.String("peer.service", "cloudflare-api"),
		attribute.String("server.address", "api.cloudflare.com"),
		attribute.String("cflog.zone_id", zoneID),
		attribute.String("cflog.since", fmt.Sprint(variables["since"])),
		attribute.String("cflog.until", fmt.Sprint(variables["until"])),
	)
	defer span.End()

	reqBody := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var respBody []byte
	var statusCode int

	for attempt := range maxRetries + 1 {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiToken)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http request: %w", err)
		}

		respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		statusCode = resp.StatusCode

		if !isRetryable(statusCode) || attempt == maxRetries {
			break
		}

		delay := retryDelay(resp.Header, attempt)
		slog.WarnContext(ctx, "Cloudflare API returned retryable status, backing off",
			"status", statusCode, "attempt", attempt+1, "delay", delay)

		retryTimer := time.NewTimer(delay)
		select {
		case <-retryTimer.C:
		case <-ctx.Done():
			retryTimer.Stop()
			return nil, ctx.Err()
		}
	}

	span.SetAttributes(attribute.Int("http.status_code", statusCode))

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

// isRetryable returns true for HTTP status codes that warrant a retry.
func isRetryable(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// retryDelay computes the backoff duration for the given attempt, honoring
// the Retry-After header if present.
func retryDelay(header http.Header, attempt int) time.Duration {
	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return retryBaseDelay * (1 << attempt)
}
