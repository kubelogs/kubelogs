package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

func TestStore(t *testing.T) {
	storage.StoreTestSuite(t, func() (storage.Store, func()) {
		store, err := New(Config{Path: ":memory:"})
		if err != nil {
			t.Fatalf("Failed to create store: %v", err)
		}
		return store, func() { store.Close() }
	})
}

func TestFTS5Search(t *testing.T) {
	store, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	entries := storage.LogBatch{
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "connection established successfully"},
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityError, Message: "connection refused by server"},
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "request completed in 50ms"},
	}

	store.Write(context.Background(), entries)
	store.Flush(context.Background())

	tests := []struct {
		name   string
		search string
		want   int
	}{
		{"single word", "connection", 2},
		{"phrase", `"connection refused"`, 1},
		{"boolean AND", "connection AND server", 1},
		{"boolean OR", "established OR refused", 2},
		{"prefix", "connect*", 2},
		{"no match", "database", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := store.Query(context.Background(), storage.Query{Search: tt.search})
			if err != nil {
				t.Fatalf("Query failed: %v", err)
			}
			if len(result.Entries) != tt.want {
				t.Errorf("Search %q returned %d entries, want %d", tt.search, len(result.Entries), tt.want)
			}
		})
	}
}

func TestOrderAsc(t *testing.T) {
	store, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	entries := storage.LogBatch{
		{Timestamp: base, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "first"},
		{Timestamp: base.Add(time.Hour), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "second"},
		{Timestamp: base.Add(2 * time.Hour), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "third"},
	}

	store.Write(context.Background(), entries)
	store.Flush(context.Background())

	// Default order is DESC (newest first)
	result, _ := store.Query(context.Background(), storage.Query{})
	if result.Entries[0].Message != "third" {
		t.Errorf("Default order expected 'third' first, got %q", result.Entries[0].Message)
	}

	// ASC order (oldest first)
	result, _ = store.Query(context.Background(), storage.Query{
		Pagination: storage.Pagination{Order: storage.OrderAsc},
	})
	if result.Entries[0].Message != "first" {
		t.Errorf("ASC order expected 'first' first, got %q", result.Entries[0].Message)
	}
}

func TestWriteBuffer(t *testing.T) {
	store, err := New(Config{Path: ":memory:", WriteBufferSize: 5})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	now := time.Now()

	// Write 3 entries (below buffer threshold)
	for i := 0; i < 3; i++ {
		store.Write(context.Background(), storage.LogBatch{
			{Timestamp: now.Add(time.Duration(i) * time.Second), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "msg"},
		})
	}

	// Query without flush - should still see entries because Query flushes first
	result, _ := store.Query(context.Background(), storage.Query{})
	if len(result.Entries) != 3 {
		t.Errorf("Expected 3 entries after query flush, got %d", len(result.Entries))
	}
}

func TestCombinedFilters(t *testing.T) {
	store, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	entries := storage.LogBatch{
		{Timestamp: now, Namespace: "prod", Pod: "api-1", Container: "app", Severity: storage.SeverityError, Message: "database connection failed"},
		{Timestamp: now, Namespace: "prod", Pod: "api-1", Container: "app", Severity: storage.SeverityInfo, Message: "request handled"},
		{Timestamp: now, Namespace: "staging", Pod: "api-1", Container: "app", Severity: storage.SeverityError, Message: "database connection failed"},
	}

	store.Write(context.Background(), entries)
	store.Flush(context.Background())

	// Combine namespace + severity + search
	result, err := store.Query(context.Background(), storage.Query{
		Namespace:   "prod",
		MinSeverity: storage.SeverityError,
		Search:      "database",
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Errorf("Combined filter returned %d entries, want 1", len(result.Entries))
	}
}
