// -------------------------------------------------------------------------------
// Collector Base
//
// Author: Alex Freidah
//
// What every collector shares, whatever it polls. CommonConfig is the settings
// half: the Loki client and batch size it ships with and its poll schedule,
// built once by main.go and handed to every collector. base is the run-time
// half each collector embeds: those settings plus the collector's identity (its
// display name, the zone/account/site it polls, and its Loki stream), and the
// behavior that follows from them - scheduling polls and recording their
// outcome. Collectors keep only what is theirs: a querier, their IDs, and their
// cursor state.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// -------------------------------------------------------------------------
// COMMON CONFIG
// -------------------------------------------------------------------------

// CommonConfig holds the settings every collector shares. Loki is a
// consumer-declared interface, not a concrete pointer, so the composition root
// (main.go) passes the real *loki.Client while tests substitute fakes.
type CommonConfig struct {
	Loki           logPusher
	PollInterval   time.Duration
	BackfillWindow time.Duration
	BatchSize      int
}

// -------------------------------------------------------------------------
// BASE
// -------------------------------------------------------------------------

// identity names a collector and what it polls. It is fixed at construction
// and labels everything the collector emits.
type identity struct {
	// name is the display name used in log lines, e.g. "Firewall".
	name string

	// stream is the Loki stream's type label. It usually matches the dataset
	// name, but not always: HTTP traffic ships as "http_traffic".
	stream string

	// scopeKey says what kind of thing is polled ("zone", "account", "site")
	// and scope names it; together they label log lines, spans, and the Loki
	// stream, and scope alone labels metrics.
	scopeKey string
	scope    string

	// record describes one shipped record, for marshal-failure warnings.
	record string
}

// base is the state and behavior every collector embeds.
type base struct {
	identity

	loki           logPusher
	batchSize      int
	pollInterval   time.Duration
	backfillWindow time.Duration

	// labels are the Loki stream labels, built once from the identity.
	labels map[string]string
}

// newBase builds a collector base from the shared settings and the collector's
// identity.
func newBase(cfg CommonConfig, id identity) base {
	return base{
		identity:       id,
		loki:           cfg.Loki,
		batchSize:      cfg.BatchSize,
		pollInterval:   cfg.PollInterval,
		backfillWindow: cfg.BackfillWindow,
		labels: map[string]string{
			"job":       "cloudflare",
			"type":      id.stream,
			id.scopeKey: id.scope,
		},
	}
}

// cursorStart is where a seek cursor begins on startup: one backfill window
// before now, so a restart re-collects the recent past it may have missed.
func (b *base) cursorStart() time.Time {
	return time.Now().UTC().Add(-b.backfillWindow)
}

// scopeAttr is the span attribute naming what this collector polls, e.g.
// cflog.zone=example.com.
func (b *base) scopeAttr() attribute.KeyValue {
	return attribute.String("cflog."+b.scopeKey, b.scope)
}

// fail reports a failed poll step: msg is logged at error level with the
// collector's scope and err, and span is marked failed. Every collector error
// path goes through here so its log line and trace always agree.
func (b *base) fail(ctx context.Context, span trace.Span, msg string, err error) {
	slog.ErrorContext(ctx, msg, b.scopeKey, b.scope, "error", err)
	telemetry.FailSpan(span, err)
}

// -------------------------------------------------------------------------
// POLL LOOP
// -------------------------------------------------------------------------

// run polls once immediately, then every poll interval, and blocks until ctx
// is cancelled. backfillFrom is where the first poll's cursor starts, logged so
// a restart's backfill is visible.
func (b *base) run(ctx context.Context, backfillFrom time.Time, poll func(context.Context)) error {
	slog.Info(b.name+" collector started",
		b.scopeKey, b.scope,
		"poll_interval", b.pollInterval,
		"backfill_from", backfillFrom.Format(time.RFC3339),
	)

	// --- Initial poll on startup ---
	poll(ctx)

	ticker := time.NewTicker(b.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info(b.name+" collector stopped", b.scopeKey, b.scope)
			return nil
		case <-ticker.C:
			poll(ctx)
		}
	}
}

// recordPoll records the outcome of one Cloudflare query: the attempt counter
// by status, the latency histogram, and - on success only - the timestamp of
// the last successful poll. Call it once, straight after the query, with the
// query's error. dataset is explicit because one collector can poll several
// (RUM polls page loads and web vitals).
func (b *base) recordPoll(dataset Dataset, start time.Time, err error) {
	status := "success"
	if err != nil {
		status = "error"
	}

	metrics.PollTotal.WithLabelValues(dataset.String(), b.scope, status).Inc()
	metrics.PollDuration.WithLabelValues(dataset.String(), b.scope).Observe(time.Since(start).Seconds())

	if err == nil {
		metrics.LastPollTimestamp.WithLabelValues(dataset.String(), b.scope).Set(float64(time.Now().Unix()))
	}
}
