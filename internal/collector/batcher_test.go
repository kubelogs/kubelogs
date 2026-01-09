package collector

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// mockStore implements storage.Store for testing.
type mockStore struct {
	mu      sync.Mutex
	entries []storage.LogEntry
	closed  bool
}

func (m *mockStore) Write(ctx context.Context, entries storage.LogBatch) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, entries...)
	return len(entries), nil
}

func (m *mockStore) Query(ctx context.Context, q storage.Query) (*storage.QueryResult, error) {
	return &storage.QueryResult{}, nil
}

func (m *mockStore) GetByID(ctx context.Context, id int64) (*storage.LogEntry, error) {
	return nil, storage.ErrNotFound
}

func (m *mockStore) Delete(ctx context.Context, olderThan time.Time) (int64, error) {
	return 0, nil
}

func (m *mockStore) Stats(ctx context.Context) (*storage.Stats, error) {
	return &storage.Stats{}, nil
}

func (m *mockStore) Close() error {
	m.closed = true
	return nil
}

func (m *mockStore) getEntries() []storage.LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]storage.LogEntry, len(m.entries))
	copy(result, m.entries)
	return result
}

func TestBatcher_FlushOnSize(t *testing.T) {
	store := &mockStore{}
	input := make(chan LogLine, 100)
	batcher := NewBatcher(store, input, 3, time.Hour) // High timeout, rely on size

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go batcher.Run(ctx)

	// Send 3 lines (should trigger flush)
	for i := range 3 {
		input <- LogLine{
			Container: ContainerRef{
				Namespace:     "default",
				PodName:       "test-pod",
				ContainerName: "test",
			},
			Timestamp: time.Now(),
			Severity:  storage.SeverityInfo,
			Message:   "test message",
		}
		_ = i
	}

	// Wait for flush
	time.Sleep(100 * time.Millisecond)

	entries := store.getEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(entries))
	}
}

func TestBatcher_FlushOnTimeout(t *testing.T) {
	store := &mockStore{}
	input := make(chan LogLine, 100)
	batcher := NewBatcher(store, input, 100, 50*time.Millisecond) // Small timeout

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go batcher.Run(ctx)

	// Send 1 line (won't hit size threshold)
	input <- LogLine{
		Container: ContainerRef{
			Namespace:     "default",
			PodName:       "test-pod",
			ContainerName: "test",
		},
		Timestamp: time.Now(),
		Severity:  storage.SeverityInfo,
		Message:   "test message",
	}

	// Wait for timeout flush
	time.Sleep(150 * time.Millisecond)

	entries := store.getEntries()
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after timeout, got %d", len(entries))
	}
}

func TestBatcher_GracefulShutdown(t *testing.T) {
	store := &mockStore{}
	input := make(chan LogLine, 100)
	batcher := NewBatcher(store, input, 100, time.Hour) // High threshold and timeout

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		batcher.Run(ctx)
		close(done)
	}()

	// Send 2 lines (won't hit threshold)
	for range 2 {
		input <- LogLine{
			Container: ContainerRef{
				Namespace:     "default",
				PodName:       "test-pod",
				ContainerName: "test",
			},
			Timestamp: time.Now(),
			Severity:  storage.SeverityInfo,
			Message:   "test message",
		}
	}

	// Give batcher time to receive the lines
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	// Wait for batcher to finish
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("batcher did not shut down in time")
	}

	// Verify entries were flushed on shutdown
	entries := store.getEntries()
	if len(entries) != 2 {
		t.Errorf("expected 2 entries after shutdown, got %d", len(entries))
	}
}

func TestBatcher_Stats(t *testing.T) {
	store := &mockStore{}
	input := make(chan LogLine, 100)
	batcher := NewBatcher(store, input, 2, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go batcher.Run(ctx)

	// Send 4 lines (2 flushes of 2)
	for range 4 {
		input <- LogLine{
			Container: ContainerRef{
				Namespace:     "default",
				PodName:       "test-pod",
				ContainerName: "test",
			},
			Timestamp: time.Now(),
			Severity:  storage.SeverityInfo,
			Message:   "test message",
		}
	}

	time.Sleep(100 * time.Millisecond)

	stats := batcher.Stats()
	if stats.TotalWrites != 2 {
		t.Errorf("expected 2 writes, got %d", stats.TotalWrites)
	}
	if stats.TotalEntries != 4 {
		t.Errorf("expected 4 total entries, got %d", stats.TotalEntries)
	}
}
