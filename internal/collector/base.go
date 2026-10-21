// -------------------------------------------------------------------------------
// Collector Base
//
// Author: Alex Freidah
//
// What every collector shares, whatever it polls. CommonConfig is the settings
// half: the Loki client and batch size it ships with, its poll schedule, and
// its ingestion lag and trailing window, built by main.go and handed to every
// collector. base is the run-time half each collector embeds: those settings
// plus the collector's identity (its display name, the zone/account/site it
// polls, and its Loki stream), and the behavior that follows from them -
// scheduling polls, sizing their windows, recording their outcome, and
// labeling data metrics with the scope. Collectors keep only what is theirs: a
// querier, their IDs, and their cursor state.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/metrics"
	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// -------------------------------------------------------------------------
// COMMON CONFIG
// -------------------------------------------------------------------------

// CommonConfig holds the settings every collector shares. Loki is a
// consumer-declared interface, not a concrete pointer, so the composition root
// (main.go) passes the real *loki.Client while tests substitute fakes.
// IngestionLag holds a cursor-based poll's window end back from now so
// Cloudflare has finished ingesting the window before it is read.
// TrailingWindow is how far back a trailing poll's quantiles reach; only
// collectors with trailing polls (HTTP latency, RUM web vitals) use it. Both
// differ by dataset, so main.go sets them per collector.
type CommonConfig struct {
	Loki           logPusher
	PollInterval   time.Duration
	BackfillWindow time.Duration
	IngestionLag   time.Duration
	TrailingWindow time.Duration
	BatchSize      int
}

// -------------------------------------------------------------------------
// BASE
// -------------------------------------------------------------------------

// identity names a collector and what it polls. It is fixed at construction
// and labels everything the collector emits. name is the display name used in
// log lines (e.g. "Firewall"). stream is the Loki stream's type label; it
// usually matches the dataset name, but not always - HTTP traffic ships as
// "http_traffic". scopeKey says what kind of thing is polled ("zone",
// "account", "site") and scope names it; together they label log lines, spans,
// and the Loki stream, and scope alone labels metrics. record describes one
// shipped record, for marshal-failure warnings.
type identity struct {
	name     string
	stream   string
	scopeKey string
	scope    string
	record   string
}

// base is the state and behavior every collector embeds: its identity, the
// shared settings, and labels, the Loki stream labels built once from the
// identity.
type base struct {
	identity
	loki           logPusher
	batchSize      int
	pollInterval   time.Duration
	backfillWindow time.Duration
	ingestionLag   time.Duration
	trailingWindow time.Duration
	labels         map[string]string
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
		ingestionLag:   cfg.IngestionLag,
		trailingWindow: cfg.TrailingWindow,
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

// pollWindow returns the end of the next poll window for a cursor at since:
// now held back by the ingestion lag, so the window only covers data Cloudflare
// has finished ingesting. ok is false when that end is not after the cursor -
// the window is empty, so the poll is skipped and the cursor stays put.
func (b *base) pollWindow(now, since time.Time) (until time.Time, ok bool) {
	until = now.UTC().Add(-b.ingestionLag)
	return until, until.After(since)
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

	// Initial poll on startup.
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

// -------------------------------------------------------------------------
// DATA METRICS
// -------------------------------------------------------------------------

// Every data metric a collector emits ends in its scope label - zone, account,
// or site, named by scopeKey. These helpers append it, so a call site passes
// only the labels its data carries and cannot misplace or omit the scope.

// add adds n to counter's series for labels plus the scope.
func (b *base) add(counter *prometheus.CounterVec, n float64, labels ...string) {
	counter.WithLabelValues(append(labels, b.scope)...).Add(n)
}

// set sets gauge's series for labels plus the scope to v.
func (b *base) set(gauge *prometheus.GaugeVec, v float64, labels ...string) {
	gauge.WithLabelValues(append(labels, b.scope)...).Set(v)
}

// setDuration sets gauge's series for labels plus the scope to raw, a
// duration Cloudflare reports in unit (e.g. time.Millisecond), converted to
// seconds. A negative raw is Cloudflare's "no data" sentinel (e.g. no
// first-input delay when there was no interaction) and is skipped, so it never
// shows up as a real 0.
func (b *base) setDuration(gauge *prometheus.GaugeVec, raw float64, unit time.Duration, labels ...string) {
	if raw < 0 {
		return
	}
	b.set(gauge, raw/float64(time.Second/unit), labels...)
}

// clear deletes every series of gauge in this collector's scope, so a gauge
// repopulated each poll drops series absent from the latest data rather than
// leaving them at a stale value.
func (b *base) clear(gauge *prometheus.GaugeVec) {
	gauge.DeletePartialMatch(prometheus.Labels{b.scopeKey: b.scope})
}
