// -------------------------------------------------------------------------------
// Collector Cursor Helpers
//
// Author: Alex Freidah
//
// Shared helpers for advancing the seek cursor that collectors use to avoid
// re-ingesting events across poll cycles. Timestamp parsing is centralized here
// so every dataset accepts the same RFC3339 layouts and handles unparseable
// values identically.
// -------------------------------------------------------------------------------

package collector

import "time"

// parseEventTime parses an event timestamp, accepting both the RFC3339Nano and
// RFC3339 layouts returned by the Cloudflare APIs. The boolean result is false
// when neither layout matches, letting callers log and skip cursor advancement
// rather than silently stalling on an unparseable timestamp.
func parseEventTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}
