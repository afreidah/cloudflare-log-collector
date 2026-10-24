// -------------------------------------------------------------------------------
// Poll Templates
//
// Author: Alex Freidah
//
// The poll cycles every collector runs, written once. There are two shapes.
// A cursor-based poll reads the window (cursor, now - ingestion lag], skips
// when that window is empty, and ships the rows to Loki before committing
// them - counting them into Prometheus and moving the cursor. A trailing poll
// reads a fixed window - ending now, or the last complete hour or day - and
// repopulates gauges from it; it is for quantiles and distinct counts, which
// cannot be accumulated, so there is no cursor and re-querying the window
// every poll self-heals late data. Both share the query
// step: a traced span, the timed query, its recorded outcome, and the failure
// path. A collector describes a dataset with a cursorPoll or trailingPoll spec
// and calls pollCursor or pollTrailing; it does not reimplement any of the
// cycle.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// -------------------------------------------------------------------------
// QUERY STEP
// -------------------------------------------------------------------------

// pollQuery is what every poll spec shares: the dataset query and how it is
// reported. dataset labels the poll's metrics and span, span is the span name,
// and title is the subject of the poll's log lines (e.g. "Firewall events").
// countAttr is the span attribute that records how many rows the poll kept.
// query reads the window (since, until]. filter, if set, drops rows before
// they are used (audit uses it to dedupe its cursor boundary).
type pollQuery[T any] struct {
	dataset   Dataset
	span      string
	title     string
	countAttr attribute.Key
	query     func(ctx context.Context, since, until time.Time) ([]T, error)
	filter    func(rows []T) []T
}

// startSpan opens the poll's span, labeled with its dataset and b's scope.
func (p pollQuery[T]) startSpan(ctx context.Context, b *base) (context.Context, trace.Span) {
	return telemetry.StartSpan(ctx, p.span,
		telemetry.AttrDataset.String(p.dataset.String()),
		b.scopeAttr(),
	)
}

// fetch runs the query over (since, until], records its outcome, and applies
// the filter. A failed query is reported on span and returns ok false; the
// caller then stops without touching any state.
func (p pollQuery[T]) fetch(ctx context.Context, b *base, span trace.Span, since, until time.Time) (rows []T, ok bool) {
	start := time.Now()
	rows, err := p.query(ctx, since, until)
	b.recordPoll(p.dataset, start, err)
	if err != nil {
		b.fail(ctx, span, p.title+" poll failed", err)
		return nil, false
	}

	if p.filter != nil {
		rows = p.filter(rows)
	}

	span.SetAttributes(p.countAttr.Int(len(rows)))
	return rows, true
}

// -------------------------------------------------------------------------
// CURSOR-BASED POLL
// -------------------------------------------------------------------------

// cursorPoll describes one cursor-based dataset poll to pollCursor. cursor
// points at the collector's seek cursor and advancer decides where it moves.
// count updates the dataset's counters for shipped rows. advanced, if set,
// runs after the cursor moves, for state that must track the cursor.
type cursorPoll[T any] struct {
	pollQuery[T]
	cursor   *time.Time
	advancer cursorAdvancer[T]
	count    func(rows []T)
	advanced func(rows []T)
}

// pollCursor runs one poll cycle for p within a traced span. A window that is
// not yet ingested is skipped without querying. A failed query or push is
// reported and leaves the cursor in place, so the same window is retried on
// the next poll. Rows are counted and the cursor moved only after every batch
// has shipped, so a window's rows are counted exactly once.
func pollCursor[T any](ctx context.Context, b *base, p cursorPoll[T]) {
	ctx, span := p.startSpan(ctx, b)
	defer span.End()

	until, ok := b.pollWindow(time.Now(), *p.cursor)
	if !ok {
		slog.DebugContext(ctx, p.title+" poll skipped, window not yet ingested", b.scopeKey, b.scope)
		return
	}

	rows, ok := p.fetch(ctx, b, span, *p.cursor, until)
	if !ok {
		return
	}

	if len(rows) == 0 {
		if next, ok := p.advancer.advance(ctx, b, rows, until); ok {
			*p.cursor = next
		}
		slog.DebugContext(ctx, p.title+" up to date", b.scopeKey, b.scope)
		return
	}

	slog.InfoContext(ctx, p.title+" fetched", b.scopeKey, b.scope, "count", len(rows))

	if err := shipThenCommit(ctx, b, rows, func() {
		p.count(rows)
		if next, ok := p.advancer.advance(ctx, b, rows, until); ok {
			*p.cursor = next
			if p.advanced != nil {
				p.advanced(rows)
			}
		}
	}); err != nil {
		b.fail(ctx, span, p.title+" failed to ship to Loki", err)
		return
	}

	slog.InfoContext(ctx, p.title+" pushed to Loki", b.scopeKey, b.scope, "count", len(rows))
}

// -------------------------------------------------------------------------
// TRAILING POLL
// -------------------------------------------------------------------------

// trailingPoll describes one trailing-window dataset poll to pollTrailing.
// window, if set, picks the window to query from now (lastComplete reports a
// whole hour or day); left unset, it is the collector's trailing window ending
// now. gauges are the metrics set repopulates; each is cleared for the
// collector's scope before set runs, so a series absent from the latest window
// does not linger at a stale value.
type trailingPoll[T any] struct {
	pollQuery[T]
	window func(now time.Time) (since, until time.Time)
	gauges []*prometheus.GaugeVec
	set    func(rows []T)
}

// pollTrailing runs one poll cycle for p within a traced span over its window.
// On success the gauges are cleared and repopulated; a failed query leaves
// them at their last values.
func pollTrailing[T any](ctx context.Context, b *base, p trailingPoll[T]) {
	ctx, span := p.startSpan(ctx, b)
	defer span.End()

	now := time.Now().UTC()
	since, until := now.Add(-b.trailingWindow), now
	if p.window != nil {
		since, until = p.window(now)
	}

	rows, ok := p.fetch(ctx, b, span, since, until)
	if !ok {
		return
	}

	for _, g := range p.gauges {
		b.clear(g)
	}
	p.set(rows)

	slog.DebugContext(ctx, p.title+" collected", b.scopeKey, b.scope, "groups", len(rows))
}

// lastComplete is a trailingPoll window: the last whole period - an hour, or a
// UTC day for 24h - that ended at or before now, as [start, end). The period
// still in progress is never reported, so a value is always for a full hour or
// day; re-querying it every poll picks up data Cloudflare adds late.
func lastComplete(period time.Duration) func(now time.Time) (since, until time.Time) {
	return func(now time.Time) (time.Time, time.Time) {
		until := now.UTC().Truncate(period)
		return until.Add(-period), until
	}
}
