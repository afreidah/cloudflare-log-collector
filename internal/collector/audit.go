// -------------------------------------------------------------------------------
// Audit Log Collector
//
// Author: Alex Freidah
//
// Polls the Cloudflare Account Audit Logs REST API on a configurable interval
// and ships events to Loki as structured JSON log lines. Tracks the last-seen
// event timestamp for seek-based pagination to avoid duplicates across polls.
// Each poll cycle is wrapped in an OpenTelemetry span for trace correlation.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/cloudflare"
	"github.com/afreidah/cloudflare-log-collector/internal/loki"
	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// -------------------------------------------------------------------------
// AUDIT COLLECTOR CONFIG
// -------------------------------------------------------------------------

// AuditCollectorConfig holds the parameters for constructing an audit collector.
type AuditCollectorConfig struct {
	CF             *cloudflare.Client
	Loki           *loki.Client
	AccountID      string
	AccountName    string
	PollInterval   time.Duration
	BackfillWindow time.Duration
	BatchSize      int
}

// -------------------------------------------------------------------------
// AUDIT COLLECTOR
// -------------------------------------------------------------------------

// AuditCollector polls Cloudflare for account audit logs and ships them to Loki.
type AuditCollector struct {
	cf           *cloudflare.Client
	loki         *loki.Client
	accountID    string
	accountName  string
	pollInterval time.Duration
	lastSeen     time.Time
	batchSize    int
}

// NewAuditCollector creates an audit log collector for the given account
// with the backfill window applied to the initial poll.
func NewAuditCollector(cfg AuditCollectorConfig) *AuditCollector {
	return &AuditCollector{
		cf:           cfg.CF,
		loki:         cfg.Loki,
		accountID:    cfg.AccountID,
		accountName:  cfg.AccountName,
		pollInterval: cfg.PollInterval,
		lastSeen:     time.Now().UTC().Add(-cfg.BackfillWindow),
		batchSize:    cfg.BatchSize,
	}
}

// Run starts the polling loop and blocks until ctx is cancelled. Implements
// the lifecycle.Service interface.
func (c *AuditCollector) Run(ctx context.Context) error {
	slog.Info("Audit collector started",
		"account", c.accountName,
		"poll_interval", c.pollInterval,
		"backfill_from", c.lastSeen.Format(time.RFC3339),
	)

	// --- Initial poll on startup ---
	c.poll(ctx)

	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Audit collector stopped", "account", c.accountName)
			return nil
		case <-ticker.C:
			c.poll(ctx)
		}
	}
}

// poll executes a single audit log collection cycle within a traced span.
func (c *AuditCollector) poll(ctx context.Context) {
	ctx, span := telemetry.StartSpan(ctx, "audit.poll",
		telemetry.AttrDataset.String("audit"),
		attribute.String("cflog.account", c.accountName),
	)
	defer span.End()

	start := time.Now()
	before := time.Now().UTC()

	events, err := c.cf.QueryAuditLogs(ctx, c.accountID, c.lastSeen, before)
	if err != nil {
		slog.ErrorContext(ctx, "Audit poll failed", "account", c.accountName, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		metrics.PollTotal.WithLabelValues("audit", c.accountName, "error").Inc()
		metrics.PollDuration.WithLabelValues("audit", c.accountName).Observe(time.Since(start).Seconds())
		return
	}

	metrics.PollTotal.WithLabelValues("audit", c.accountName, "success").Inc()
	metrics.PollDuration.WithLabelValues("audit", c.accountName).Observe(time.Since(start).Seconds())
	metrics.LastPollTimestamp.WithLabelValues("audit", c.accountName).Set(float64(time.Now().Unix()))

	span.SetAttributes(attribute.Int("cflog.event_count", len(events)))

	if len(events) == 0 {
		slog.DebugContext(ctx, "No new audit events", "account", c.accountName)
		return
	}

	slog.InfoContext(ctx, "Audit events fetched", "account", c.accountName, "count", len(events))

	// --- Ship events to Loki in batches ---
	if err := c.shipToLoki(ctx, events); err != nil {
		slog.ErrorContext(ctx, "Failed to ship audit events to Loki", "account", c.accountName, "error", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}

	slog.InfoContext(ctx, "Audit events pushed to Loki", "account", c.accountName, "count", len(events))

	// --- Update metrics ---
	for i := range events {
		metrics.AuditEventsTotal.WithLabelValues(events[i].Action.Type, c.accountName).Inc()
	}

	// --- Advance cursor to the last event's timestamp ---
	lastEvent := events[len(events)-1]
	if t, err := time.Parse(time.RFC3339Nano, lastEvent.Action.Time); err == nil {
		c.lastSeen = t
	} else if t, err := time.Parse(time.RFC3339, lastEvent.Action.Time); err == nil {
		c.lastSeen = t
	}
}

// shipToLoki sends audit events to Loki as JSON log lines in batches.
func (c *AuditCollector) shipToLoki(ctx context.Context, events []cloudflare.AuditLogEvent) error {
	labels := map[string]string{
		"job":     "cloudflare",
		"type":    "audit",
		"account": c.accountName,
	}

	// --- Use current time for Loki entry timestamps to avoid rejection by
	// Loki's reject_old_samples_max_age. The original event timestamp is
	// preserved in the JSON log line body for querying. ---
	now := time.Now().UTC()
	entries := make([]loki.Entry, 0, len(events))
	for i := range events {
		line, err := json.Marshal(&events[i])
		if err != nil {
			slog.WarnContext(ctx, "Failed to marshal audit event", "account", c.accountName, "error", err)
			continue
		}

		entries = append(entries, loki.NewEntry(now, string(line)))
	}

	// --- Send in batches ---
	for i := 0; i < len(entries); i += c.batchSize {
		end := min(i+c.batchSize, len(entries))

		if err := c.loki.Push(ctx, labels, entries[i:end]); err != nil {
			return err
		}
	}

	return nil
}
