// -------------------------------------------------------------------------------
// Collector Cursor Helpers
//
// Author: Alex Freidah
//
// The seek cursors collectors use to avoid re-ingesting data across poll
// cycles. A cursor-based poll reads the window (cursor, until]; once the window
// is handled, the cursor advancer decides where the cursor moves. Aggregated
// datasets report every bucket in a window, so a handled window is complete and
// the cursor moves to its end (windowEnd). Event datasets return a capped page
// of events, so the cursor moves only as far as the last event actually
// shipped (lastEvent). Timestamp parsing is centralized here so every dataset
// accepts the same RFC3339 layouts and handles unparseable values identically.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"log/slog"
	"time"
)

// cursorAdvancer decides where a collector's seek cursor moves once the rows of
// a poll window (possibly none) have been handled, given the window's end. ok
// is false when the cursor should stay where it is.
type cursorAdvancer[T any] interface {
	advance(ctx context.Context, b *base, rows []T, until time.Time) (cursor time.Time, ok bool)
}

// windowEnd moves the cursor to the end of every handled window, empty or not.
// It is the advancer for aggregated datasets (HTTP traffic, RUM page loads),
// whose groups cover the whole window.
type windowEnd[T any] struct{}

func (windowEnd[T]) advance(_ context.Context, _ *base, _ []T, until time.Time) (time.Time, bool) {
	return until, true
}

// lastEvent moves the cursor to the timestamp of the last row shipped, read
// with timestamp. It is the advancer for event datasets (firewall, audit): a page
// is capped, so a full page may leave later events in the window, and the
// cursor must not skip them by jumping to the window's end. An empty window
// leaves the cursor in place. An unparseable timestamp is logged and leaves the
// cursor in place rather than stalling silently.
type lastEvent[T any] struct {
	timestamp func(T) string
}

func (k lastEvent[T]) advance(ctx context.Context, b *base, rows []T, _ time.Time) (time.Time, bool) {
	if len(rows) == 0 {
		return time.Time{}, false
	}

	raw := k.timestamp(rows[len(rows)-1])
	t, ok := parseEventTime(raw)
	if !ok {
		slog.WarnContext(ctx, "Unparseable "+b.record+" timestamp, cursor not advanced",
			b.scopeKey, b.scope, "time", raw)
	}
	return t, ok
}

// parseEventTime parses an RFC3339 event timestamp as returned by the
// Cloudflare APIs, with or without fractional seconds - parsing with
// RFC3339Nano accepts both. The boolean result is false for anything else,
// letting callers log and skip cursor advancement rather than silently
// stalling on an unparseable timestamp.
func parseEventTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}
