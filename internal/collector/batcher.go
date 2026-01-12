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

	// Retry queue for failed batches
	retryMu    sync.Mutex
	retryQueue []storage.LogBatch
	backoff    time.Duration

	// Circuit breaker
	consecutiveFailures int
	circuitOpen         bool
	circuitOpenUntil    time.Time

	// Metrics
	totalWrites  atomic.Int64
	totalEntries atomic.Int64
	writeErrors  atomic.Int64
	retriedBatches atomic.Int64
}

// BatcherStats contains batcher statistics.
type BatcherStats struct {
	TotalWrites    int64
	TotalEntries   int64
	WriteErrors    int64
	BufferSize     int
	RetryQueueSize int
	RetriedBatches int64
	CircuitOpen    bool
}

const (
	minBackoff       = time.Second
	maxBackoff       = 30 * time.Second
	maxRetryQueue    = 100 // Maximum number of batches to queue for retry
	circuitThreshold = 5   // Consecutive failures before opening circuit
	circuitTimeout   = 30 * time.Second
)

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
		retryQueue:    make([]storage.LogBatch, 0),
		backoff:       minBackoff,
	}
}

// Run processes log lines until ctx is canceled.
// Performs final flush on shutdown.
func (b *Batcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	retryTicker := time.NewTicker(b.backoff)
	defer retryTicker.Stop()

	healthTicker := time.NewTicker(60 * time.Second)
	defer healthTicker.Stop()

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

		case <-retryTicker.C:
			// Process retry queue if not empty and circuit is not open
			if !b.isCircuitOpen() {
				b.processRetryQueue(ctx)
			}
			// Adjust ticker to current backoff
			b.retryMu.Lock()
			currentBackoff := b.backoff
			b.retryMu.Unlock()
			retryTicker.Reset(currentBackoff)

		case <-healthTicker.C:
			// Periodic health check - log warning if circuit is open or retry queue has items
			stats := b.Stats()
			if stats.CircuitOpen || stats.RetryQueueSize > 0 {
				slog.Warn("batcher health check",
					"circuitOpen", stats.CircuitOpen,
					"retryQueueSize", stats.RetryQueueSize,
					"writeErrors", stats.WriteErrors,
					"totalWrites", stats.TotalWrites,
				)
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

	// Check circuit breaker before attempting write
	if b.isCircuitOpen() {
		b.addToRetryQueue(batch)
		return nil // Don't return error, batch is queued
	}

	n, err := b.store.Write(ctx, batch)
	if err != nil {
		b.writeErrors.Add(1)
		b.recordFailure()
		b.addToRetryQueue(batch)
		slog.Warn("batch write failed, queued for retry",
			"entries", len(batch),
			"retry_queue_size", len(b.retryQueue),
			"error", err,
		)
		return nil // Don't return error, batch is queued
	}

	b.recordSuccess()
	b.totalWrites.Add(1)
	b.totalEntries.Add(int64(n))

	return nil
}

func (b *Batcher) isCircuitOpen() bool {
	b.retryMu.Lock()
	defer b.retryMu.Unlock()

	if b.circuitOpen && time.Now().After(b.circuitOpenUntil) {
		b.circuitOpen = false
		b.consecutiveFailures = 0
		slog.Info("circuit breaker closed, resuming writes")
	}
	return b.circuitOpen
}

func (b *Batcher) recordFailure() {
	b.retryMu.Lock()
	defer b.retryMu.Unlock()

	b.consecutiveFailures++
	if b.consecutiveFailures >= circuitThreshold {
		b.circuitOpen = true
		b.circuitOpenUntil = time.Now().Add(circuitTimeout)
		slog.Warn("circuit breaker opened",
			"failures", b.consecutiveFailures,
			"reopen_at", b.circuitOpenUntil,
		)
	}
}

func (b *Batcher) recordSuccess() {
	b.retryMu.Lock()
	defer b.retryMu.Unlock()

	b.consecutiveFailures = 0
	b.backoff = minBackoff
}

func (b *Batcher) addToRetryQueue(batch storage.LogBatch) {
	b.retryMu.Lock()
	defer b.retryMu.Unlock()

	if len(b.retryQueue) >= maxRetryQueue {
		slog.Warn("retry queue full, dropping oldest batch",
			"queue_size", len(b.retryQueue),
			"dropped_entries", len(b.retryQueue[0]),
		)
		b.retryQueue = b.retryQueue[1:] // Drop oldest
	}

	b.retryQueue = append(b.retryQueue, batch)
}

func (b *Batcher) processRetryQueue(ctx context.Context) {
	b.retryMu.Lock()
	if len(b.retryQueue) == 0 {
		b.retryMu.Unlock()
		return
	}

	// Take first batch from queue
	batch := b.retryQueue[0]
	b.retryMu.Unlock()

	n, err := b.store.Write(ctx, batch)
	if err != nil {
		b.recordFailure()
		slog.Warn("retry failed, will try again",
			"entries", len(batch),
			"backoff", b.backoff,
			"error", err,
		)
		// Exponential backoff
		b.retryMu.Lock()
		b.backoff = min(b.backoff*2, maxBackoff)
		b.retryMu.Unlock()
		return
	}

	// Success - remove from queue
	b.retryMu.Lock()
	b.retryQueue = b.retryQueue[1:]
	b.retryMu.Unlock()

	b.recordSuccess()
	b.retriedBatches.Add(1)
	b.totalWrites.Add(1)
	b.totalEntries.Add(int64(n))
	slog.Info("retry succeeded", "entries", n)
}

func (b *Batcher) convertToEntry(line LogLine) storage.LogEntry {
	// Start with extracted attributes from parsed log (may be nil)
	attrs := line.Attributes
	if attrs == nil {
		attrs = make(map[string]string, 1)
	}
	// Always add pod_uid
	attrs["pod_uid"] = line.Container.PodUID

	return storage.LogEntry{
		Timestamp:  line.Timestamp,
		Namespace:  line.Container.Namespace,
		Pod:        line.Container.PodName,
		Container:  line.Container.ContainerName,
		Severity:   line.Severity,
		Message:    line.Message,
		Attributes: attrs,
	}
}

// Stats returns batcher statistics.
func (b *Batcher) Stats() BatcherStats {
	b.mu.Lock()
	bufSize := len(b.buffer)
	b.mu.Unlock()

	b.retryMu.Lock()
	retrySize := len(b.retryQueue)
	circuitOpen := b.circuitOpen
	b.retryMu.Unlock()

	return BatcherStats{
		TotalWrites:    b.totalWrites.Load(),
		TotalEntries:   b.totalEntries.Load(),
		WriteErrors:    b.writeErrors.Load(),
		BufferSize:     bufSize,
		RetryQueueSize: retrySize,
		RetriedBatches: b.retriedBatches.Load(),
		CircuitOpen:    circuitOpen,
	}
}
