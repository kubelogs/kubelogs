package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// Common errors returned by storage implementations.
var (
	ErrNotFound      = errors.New("storage: entry not found")
	ErrStorageClosed = errors.New("storage: storage is closed")
)

// Store defines the interface for log storage backends.
// Implementations must be safe for concurrent use.
type Store interface {
	// Write persists a batch of log entries.
	// Returns the number of entries written.
	Write(ctx context.Context, entries LogBatch) (int, error)

	// Query searches for log entries matching the given criteria.
	Query(ctx context.Context, q Query) (*QueryResult, error)

	// GetByID retrieves a single entry by its ID.
	// Returns ErrNotFound if the entry doesn't exist.
	GetByID(ctx context.Context, id int64) (*LogEntry, error)

	// Delete removes entries older than the given timestamp.
	// Returns the number of entries deleted.
	Delete(ctx context.Context, olderThan time.Time) (int64, error)

	// Stats returns storage statistics.
	Stats(ctx context.Context) (*Stats, error)

	// Close releases resources.
	io.Closer
}

// Stats contains storage statistics.
type Stats struct {
	TotalEntries  int64
	DiskSizeBytes int64
	OldestEntry   time.Time
	NewestEntry   time.Time
}

// WriteOptimizer is an optional interface for write-heavy workloads.
type WriteOptimizer interface {
	// Flush forces any buffered writes to persistent storage.
	Flush(ctx context.Context) error

	// SetWriteBuffer configures the write buffer size.
	SetWriteBuffer(entries int)
}
