package collector

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// Batcher accumulates log lines and writes them in batches to storage.
type Batcher struct {
	store         storage.Store
	batchSize     int
	flushInterval time.Duration

	input <-chan LogLine

	mu        sync.Mutex
	buffer    storage.LogBatch
	lastFlush time.Time

	// Metrics
	totalWrites  atomic.Int64
	totalEntries atomic.Int64
	writeErrors  atomic.Int64
}

// BatcherStats contains batcher statistics.
type BatcherStats struct {
	TotalWrites  int64
	TotalEntries int64
	WriteErrors  int64
	BufferSize   int
}

// NewBatcher creates a log batcher.
func NewBatcher(
	store storage.Store,
	input <-chan LogLine,
	batchSize int,
	flushInterval time.Duration,
) *Batcher {
	return &Batcher{
		store:         store,
		input:         input,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		buffer:        make(storage.LogBatch, 0, batchSize),
		lastFlush:     time.Now(),
	}
}

// Run processes log lines until ctx is canceled.
// Performs final flush on shutdown.
func (b *Batcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case line, ok := <-b.input:
			if !ok {
				// Input channel closed, flush remaining
				return b.Flush(context.Background())
			}

			entry := b.convertToEntry(line)

			b.mu.Lock()
			b.buffer = append(b.buffer, entry)
			shouldFlush := len(b.buffer) >= b.batchSize
			b.mu.Unlock()

			if shouldFlush {
				if err := b.flush(ctx); err != nil {
					slog.Error("batch flush failed", "error", err)
				}
			}

		case <-ticker.C:
			b.mu.Lock()
			shouldFlush := len(b.buffer) > 0 && time.Since(b.lastFlush) >= b.flushInterval
			b.mu.Unlock()

			if shouldFlush {
				if err := b.flush(ctx); err != nil {
					slog.Error("periodic flush failed", "error", err)
				}
			}

		case <-ctx.Done():
			// Graceful shutdown - flush remaining with timeout
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := b.Flush(shutdownCtx)
			cancel()
			return err
		}
	}
}

// Flush forces an immediate write of buffered logs.
func (b *Batcher) Flush(ctx context.Context) error {
	return b.flush(ctx)
}

func (b *Batcher) flush(ctx context.Context) error {
	b.mu.Lock()
	if len(b.buffer) == 0 {
		b.mu.Unlock()
		return nil
	}

	batch := b.buffer
	b.buffer = make(storage.LogBatch, 0, b.batchSize)
	b.lastFlush = time.Now()
	b.mu.Unlock()

	n, err := b.store.Write(ctx, batch)
	if err != nil {
		b.writeErrors.Add(1)
		slog.Error("failed to write batch",
			"entries", len(batch),
			"error", err,
		)
		return err
	}

	b.totalWrites.Add(1)
	b.totalEntries.Add(int64(n))

	return nil
}

func (b *Batcher) convertToEntry(line LogLine) storage.LogEntry {
	return storage.LogEntry{
		Timestamp: line.Timestamp,
		Namespace: line.Container.Namespace,
		Pod:       line.Container.PodName,
		Container: line.Container.ContainerName,
		Severity:  line.Severity,
		Message:   line.Message,
		Attributes: map[string]string{
			"pod_uid": line.Container.PodUID,
		},
	}
}

// Stats returns batcher statistics.
func (b *Batcher) Stats() BatcherStats {
	b.mu.Lock()
	bufSize := len(b.buffer)
	b.mu.Unlock()

	return BatcherStats{
		TotalWrites:  b.totalWrites.Load(),
		TotalEntries: b.totalEntries.Load(),
		WriteErrors:  b.writeErrors.Load(),
		BufferSize:   bufSize,
	}
}
