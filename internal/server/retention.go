package server

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// RetentionWorker periodically deletes old log entries.
type RetentionWorker struct {
	store  storage.Store
	config Config

	totalRuns    atomic.Int64
	totalDeleted atomic.Int64
	lastRunTime  atomic.Pointer[time.Time]
	lastRunError atomic.Pointer[error]
}

// RetentionStats contains retention worker statistics.
type RetentionStats struct {
	TotalRuns    int64
	TotalDeleted int64
	LastRunTime  time.Time
	LastRunError error
}

// NewRetentionWorker creates a new retention worker.
func NewRetentionWorker(store storage.Store, config Config) *RetentionWorker {
	return &RetentionWorker{
		store:  store,
		config: config,
	}
}

// Run starts the retention worker. Blocks until ctx is canceled.
func (w *RetentionWorker) Run(ctx context.Context) {
	if !w.config.RetentionEnabled() {
		slog.Info("retention disabled, worker not starting")
		return
	}

	slog.Info("retention worker starting",
		"retention_days", w.config.RetentionDays,
		"interval", w.config.RetentionInterval,
	)

	// Run immediately on startup
	w.runOnce(ctx)

	ticker := time.NewTicker(w.config.RetentionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.runOnce(ctx)
		case <-ctx.Done():
			slog.Info("retention worker stopping")
			return
		}
	}
}

// runOnce executes a single retention cycle.
func (w *RetentionWorker) runOnce(ctx context.Context) {
	cutoff := w.config.RetentionCutoff()

	slog.Debug("retention cleanup starting",
		"cutoff", cutoff.Format(time.RFC3339),
	)

	deleted, err := w.store.Delete(ctx, cutoff)

	w.totalRuns.Add(1)
	now := time.Now()
	w.lastRunTime.Store(&now)

	if err != nil {
		w.lastRunError.Store(&err)
		slog.Error("retention cleanup failed",
			"cutoff", cutoff.Format(time.RFC3339),
			"error", err,
		)
		return
	}

	w.lastRunError.Store(nil)
	w.totalDeleted.Add(deleted)

	if deleted > 0 {
		slog.Info("retention cleanup completed",
			"deleted", deleted,
			"cutoff", cutoff.Format(time.RFC3339),
		)
	} else {
		slog.Debug("retention cleanup completed, no logs to delete",
			"cutoff", cutoff.Format(time.RFC3339),
		)
	}
}

// Stats returns retention worker statistics.
func (w *RetentionWorker) Stats() RetentionStats {
	var lastErr error
	if errPtr := w.lastRunError.Load(); errPtr != nil {
		lastErr = *errPtr
	}

	var lastTime time.Time
	if timePtr := w.lastRunTime.Load(); timePtr != nil {
		lastTime = *timePtr
	}

	return RetentionStats{
		TotalRuns:    w.totalRuns.Load(),
		TotalDeleted: w.totalDeleted.Load(),
		LastRunTime:  lastTime,
		LastRunError: lastErr,
	}
}
