// -------------------------------------------------------------------------------
// Loki Shipping Helper
//
// Author: Alex Freidah
//
// Shared helper that every collector uses to ship a batch of records to Loki:
// marshal each record to a JSON log line, then push the lines under the given
// stream labels in batches. Each collector differs only in its record type and
// stream labels, so the marshal-and-batch loop lives here once.
// -------------------------------------------------------------------------------

package collector

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/afreidah/cloudflare-log-collector/internal/loki"
)

// shipJSON marshals each item to a JSON log line and pushes the lines to Loki
// under labels, in batches of batchSize. Items that fail to marshal are logged
// with warnMsg (plus any logFields key/value pairs) and skipped.
//
// Entries are stamped with the current time rather than the record's own
// timestamp to avoid rejection by Loki's reject_old_samples_max_age; the
// original timestamp is preserved in the JSON body for querying.
func shipJSON[T any](ctx context.Context, push logPusher, batchSize int, labels map[string]string, items []T, warnMsg string, logFields ...any) error {
	now := time.Now().UTC()
	entries := make([]loki.Entry, 0, len(items))
	for i := range items {
		line, err := json.Marshal(&items[i])
		if err != nil {
			slog.WarnContext(ctx, warnMsg, append(append([]any{}, logFields...), "error", err)...)
			continue
		}
		entries = append(entries, loki.NewEntry(now, string(line)))
	}

	for i := 0; i < len(entries); i += batchSize {
		end := min(i+batchSize, len(entries))
		if err := push.Push(ctx, labels, entries[i:end]); err != nil {
			return err
		}
	}

	return nil
}
