// -------------------------------------------------------------------------------
// Cursor-Based Poll
//
// Author: Alex Freidah
//
// The poll cycle every cursor-based collector runs, written once. Each cycle
// reads the window (cursor, now - ingestion lag], skips when that window is
// empty, queries it, records the outcome, and ships the rows to Loki before
// committing them - counting them into Prometheus and moving the cursor. A
// collector describes its dataset with a cursorPoll spec and calls pollCursor;
// it does not reimplement any of the cycle.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// cursorPoll describes one cursor-based dataset poll to pollCursor.
//
// dataset labels the poll's metrics and span, span is the span name, and title
// is the subject of the poll's log lines (e.g. "Firewall events"). countAttr is
// the span attribute that records how many rows the poll returned. cursor
// points at the collector's seek cursor and advancer decides where it moves. query
// reads the window (since, until]. filter, if set, drops rows before they are
// counted or shipped (audit uses it to dedupe its cursor boundary). count
// updates the dataset's counters for shipped rows. advanced, if set, runs after
// the cursor moves, for state that must track the cursor.
type cursorPoll[T any] struct {
	dataset   Dataset
	span      string
	title     string
	countAttr attribute.Key
	cursor    *time.Time
	advancer  cursorAdvancer[T]
	query     func(ctx context.Context, since, until time.Time) ([]T, error)
	filter    func(rows []T) []T
	count     func(rows []T)
	advanced  func(rows []T)
}

// pollCursor runs one poll cycle for p within a traced span. A window that is
// not yet ingested is skipped without querying. A failed query or push is
// reported and leaves the cursor in place, so the same window is retried on
// the next poll. Rows are counted and the cursor moved only after every batch
// has shipped, so a window's rows are counted exactly once.
func pollCursor[T any](ctx context.Context, b *base, p cursorPoll[T]) {
	ctx, span := telemetry.StartSpan(ctx, p.span,
		telemetry.AttrDataset.String(p.dataset.String()),
		b.scopeAttr(),
	)
	defer span.End()

	start := time.Now()
	until, ok := b.pollWindow(start, *p.cursor)
	if !ok {
		slog.DebugContext(ctx, p.title+" poll skipped, window not yet ingested", b.scopeKey, b.scope)
		return
	}

	rows, err := p.query(ctx, *p.cursor, until)
	b.recordPoll(p.dataset, start, err)
	if err != nil {
		b.fail(ctx, span, p.title+" poll failed", err)
		return
	}

	if p.filter != nil {
		rows = p.filter(rows)
	}

	span.SetAttributes(p.countAttr.Int(len(rows)))

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
