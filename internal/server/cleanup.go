package server

import (
	"context"
	"log/slog"
	"time"
)

// DefaultRetention is the default job retention period.
const DefaultRetention = 7 * 24 * time.Hour

// RunCleanup periodically deletes jobs older than retention.
// It blocks until ctx is cancelled.
func RunCleanup(ctx context.Context, store JobStore, retention time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().UTC().Add(-retention)
			n, err := store.DeleteBefore(ctx, cutoff)
			if err != nil {
				logger.Error("cleanup failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("cleaned up old jobs", "deleted", n, "retention", retention)
			}
		}
	}
}
