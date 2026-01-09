package storage

import (
	"context"
	"testing"
	"time"
)

// StoreTestSuite runs a standard test suite against any Store implementation.
func StoreTestSuite(t *testing.T, newStore func() (Store, func())) {
	t.Run("WriteAndQuery", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		now := time.Now()
		entries := LogBatch{
			{
				Timestamp: now,
				Namespace: "default",
				Pod:       "nginx-abc123",
				Container: "nginx",
				Severity:  SeverityInfo,
				Message:   "request completed successfully",
			},
			{
				Timestamp: now.Add(time.Second),
				Namespace: "default",
				Pod:       "nginx-abc123",
				Container: "nginx",
				Severity:  SeverityError,
				Message:   "connection refused",
			},
		}

		n, err := store.Write(context.Background(), entries)
		if err != nil {
			t.Fatalf("Write failed: %v", err)
		}
		if n != 2 {
			t.Errorf("Write returned %d, want 2", n)
		}

		// Force flush for stores with buffering
		if wo, ok := store.(WriteOptimizer); ok {
			if err := wo.Flush(context.Background()); err != nil {
				t.Fatalf("Flush failed: %v", err)
			}
		}

		result, err := store.Query(context.Background(), Query{})
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if len(result.Entries) != 2 {
			t.Errorf("Query returned %d entries, want 2", len(result.Entries))
		}
	})

	t.Run("QueryTimeRange", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
		entries := LogBatch{
			{Timestamp: base, Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "first"},
			{Timestamp: base.Add(time.Hour), Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "second"},
			{Timestamp: base.Add(2 * time.Hour), Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "third"},
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		result, err := store.Query(context.Background(), Query{
			StartTime: base.Add(30 * time.Minute),
			EndTime:   base.Add(90 * time.Minute),
		})
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if len(result.Entries) != 1 {
			t.Errorf("Query returned %d entries, want 1", len(result.Entries))
		}
	})

	t.Run("QueryNamespaceFilter", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		now := time.Now()
		entries := LogBatch{
			{Timestamp: now, Namespace: "production", Pod: "api-1", Container: "app", Severity: SeverityInfo, Message: "prod log"},
			{Timestamp: now, Namespace: "staging", Pod: "api-1", Container: "app", Severity: SeverityInfo, Message: "staging log"},
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		result, err := store.Query(context.Background(), Query{Namespace: "production"})
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if len(result.Entries) != 1 {
			t.Errorf("Query returned %d entries, want 1", len(result.Entries))
		}
		if result.Entries[0].Namespace != "production" {
			t.Errorf("Expected namespace production, got %s", result.Entries[0].Namespace)
		}
	})

	t.Run("QuerySeverityFilter", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		now := time.Now()
		entries := LogBatch{
			{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityDebug, Message: "debug"},
			{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "info"},
			{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityError, Message: "error"},
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		result, err := store.Query(context.Background(), Query{MinSeverity: SeverityWarn})
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if len(result.Entries) != 1 {
			t.Errorf("Query returned %d entries, want 1 (error only)", len(result.Entries))
		}
	})

	t.Run("QueryAttributes", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		now := time.Now()
		entries := LogBatch{
			{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "msg1", Attributes: map[string]string{"user_id": "123"}},
			{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "msg2", Attributes: map[string]string{"user_id": "456"}},
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		result, err := store.Query(context.Background(), Query{Attributes: map[string]string{"user_id": "123"}})
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if len(result.Entries) != 1 {
			t.Errorf("Query returned %d entries, want 1", len(result.Entries))
		}
	})

	t.Run("GetByID", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		entries := LogBatch{
			{Timestamp: time.Now(), Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "test message"},
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		result, _ := store.Query(context.Background(), Query{})
		if len(result.Entries) == 0 {
			t.Fatal("No entries found")
		}

		entry, err := store.GetByID(context.Background(), result.Entries[0].ID)
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if entry.Message != "test message" {
			t.Errorf("Expected 'test message', got %q", entry.Message)
		}
	})

	t.Run("GetByIDNotFound", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		_, err := store.GetByID(context.Background(), 99999)
		if err != ErrNotFound {
			t.Errorf("Expected ErrNotFound, got %v", err)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		entries := LogBatch{
			{Timestamp: base, Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "old"},
			{Timestamp: base.Add(24 * time.Hour), Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "new"},
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		deleted, err := store.Delete(context.Background(), base.Add(12*time.Hour))
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}
		if deleted != 1 {
			t.Errorf("Delete returned %d, want 1", deleted)
		}

		result, _ := store.Query(context.Background(), Query{})
		if len(result.Entries) != 1 {
			t.Errorf("Query returned %d entries after delete, want 1", len(result.Entries))
		}
	})

	t.Run("Stats", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		entries := LogBatch{
			{Timestamp: time.Now(), Namespace: "ns", Pod: "pod", Container: "c", Severity: SeverityInfo, Message: "msg"},
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		stats, err := store.Stats(context.Background())
		if err != nil {
			t.Fatalf("Stats failed: %v", err)
		}
		if stats.TotalEntries != 1 {
			t.Errorf("Stats.TotalEntries = %d, want 1", stats.TotalEntries)
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		store, cleanup := newStore()
		defer cleanup()

		now := time.Now()
		entries := make(LogBatch, 10)
		for i := range entries {
			entries[i] = LogEntry{
				Timestamp: now.Add(time.Duration(i) * time.Second),
				Namespace: "ns",
				Pod:       "pod",
				Container: "c",
				Severity:  SeverityInfo,
				Message:   "msg",
			}
		}

		store.Write(context.Background(), entries)
		if wo, ok := store.(WriteOptimizer); ok {
			wo.Flush(context.Background())
		}

		// First page
		result, err := store.Query(context.Background(), Query{
			Pagination: Pagination{Limit: 3},
		})
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}
		if len(result.Entries) != 3 {
			t.Errorf("First page has %d entries, want 3", len(result.Entries))
		}
		if !result.HasMore {
			t.Error("Expected HasMore=true")
		}

		// Second page
		result2, err := store.Query(context.Background(), Query{
			Pagination: Pagination{Limit: 3, AfterID: result.NextCursor},
		})
		if err != nil {
			t.Fatalf("Query page 2 failed: %v", err)
		}
		if len(result2.Entries) != 3 {
			t.Errorf("Second page has %d entries, want 3", len(result2.Entries))
		}
	})
}
