// -------------------------------------------------------------------------------
// Cloudflare Account Audit Logs Query
//
// Author: Aaron Florey
//
// Queries the account Audit Logs REST API (not GraphQL), paginating through all
// results with cursor-based pagination and injecting the account ID into each
// returned event.
// -------------------------------------------------------------------------------

package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const (
	// auditLogsEndpoint is the Cloudflare Audit Logs REST API URL template.
	auditLogsEndpoint = "https://api.cloudflare.com/client/v4/accounts/%s/logs/audit"

	// auditQueryLimit is the maximum number of audit log entries requested per page.
	auditQueryLimit = 1000
)

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

// doAuditQuery executes a single page request to the audit logs REST API. The
// request is rebuilt for every attempt by the shared retrying executor, so no
// per-request state leaks across retries.
func (c *Client) doAuditQuery(ctx context.Context, endpoint string, since, before time.Time, cursor string) ([]AuditLogEvent, string, error) {
	respBody, statusCode, err := c.doWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
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
		return req, nil
	})
	if err != nil {
		return nil, "", err
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
