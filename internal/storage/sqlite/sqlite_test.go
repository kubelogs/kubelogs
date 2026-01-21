package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"

	_ "github.com/mattn/go-sqlite3"
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

func TestConcurrentWrites(t *testing.T) {
	// Use file-based DB to properly test locking behavior
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := New(Config{Path: dbPath, WriteBufferSize: 10})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	const numGoroutines = 10
	const writesPerGoroutine = 100

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < writesPerGoroutine; j++ {
				entries := storage.LogBatch{
					{
						Timestamp: time.Now(),
						Namespace: fmt.Sprintf("ns-%d", goroutineID),
						Pod:       fmt.Sprintf("pod-%d-%d", goroutineID, j),
						Container: "container",
						Severity:  storage.SeverityInfo,
						Message:   fmt.Sprintf("message from goroutine %d, write %d", goroutineID, j),
					},
				}
				if _, err := store.Write(ctx, entries); err != nil {
					errCh <- fmt.Errorf("goroutine %d write %d: %w", goroutineID, j, err)
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("Concurrent write error: %v", err)
	}

	// Final flush
	if err := store.Flush(ctx); err != nil {
		t.Fatalf("Final flush failed: %v", err)
	}

	// Verify all entries were written
	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}

	expected := int64(numGoroutines * writesPerGoroutine)
	if stats.TotalEntries != expected {
		t.Errorf("Expected %d entries, got %d", expected, stats.TotalEntries)
	}
}

func TestConcurrentWritesAndReads(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := New(Config{Path: dbPath, WriteBufferSize: 5})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Pre-populate some data
	for i := 0; i < 50; i++ {
		store.Write(ctx, storage.LogBatch{
			{Timestamp: time.Now(), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "init"},
		})
	}
	store.Flush(ctx)

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// Writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, err := store.Write(ctx, storage.LogBatch{
					{Timestamp: time.Now(), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: fmt.Sprintf("w%d-%d", id, j)},
				})
				if err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}

	// Readers (concurrent queries should not cause SQLITE_BUSY)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_, err := store.Query(ctx, storage.Query{Pagination: storage.Pagination{Limit: 10}})
				if err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("Concurrent operation error: %v", err)
	}
}

func TestDeduplication(t *testing.T) {
	store, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	entry := storage.LogEntry{
		Timestamp: now,
		Namespace: "default",
		Pod:       "test-pod",
		Container: "app",
		Severity:  storage.SeverityInfo,
		Message:   "duplicate message",
	}

	// Write same entry twice
	store.Write(context.Background(), storage.LogBatch{entry})
	store.Write(context.Background(), storage.LogBatch{entry})
	store.Flush(context.Background())

	// Should only have one entry due to deduplication
	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.TotalEntries != 1 {
		t.Errorf("Expected 1 entry after dedup, got %d", stats.TotalEntries)
	}
}

func TestDeduplicationDifferentEntries(t *testing.T) {
	store, err := New(Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	now := time.Now()

	// These should all be stored as separate entries
	entries := storage.LogBatch{
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "msg1"},
		{Timestamp: now.Add(time.Nanosecond), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "msg1"}, // different timestamp
		{Timestamp: now, Namespace: "ns2", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "msg1"},                     // different namespace
		{Timestamp: now, Namespace: "ns", Pod: "pod2", Container: "c", Severity: storage.SeverityInfo, Message: "msg1"},                     // different pod
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c2", Severity: storage.SeverityInfo, Message: "msg1"},                     // different container
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "msg2"},                      // different message
	}

	store.Write(context.Background(), entries)
	store.Flush(context.Background())

	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.TotalEntries != 6 {
		t.Errorf("Expected 6 distinct entries, got %d", stats.TotalEntries)
	}
}

func TestDedupHashCollisionResistance(t *testing.T) {
	// Test that similar but different entries get different hashes
	testCases := []struct {
		ts        int64
		namespace string
		pod       string
		container string
		message   string
	}{
		{1000, "ns", "pod", "container", "msg"},
		{1001, "ns", "pod", "container", "msg"},  // Different timestamp
		{1000, "ns2", "pod", "container", "msg"}, // Different namespace
		{1000, "ns", "pod2", "container", "msg"}, // Different pod
		{1000, "ns", "pod", "container2", "msg"}, // Different container
		{1000, "ns", "pod", "container", "msg2"}, // Different message
		// Test separator collision prevention
		{1000, "ab", "c", "d", "msg"}, // namespace="ab", pod="c"
		{1000, "a", "bc", "d", "msg"}, // namespace="a", pod="bc" - should be different hash
		{1000, "a", "b", "cd", "msg"}, // container="cd"
		{1000, "a", "b", "c", "dmsg"}, // message="dmsg"
	}

	hashes := make(map[int64]int)
	for i, tc := range testCases {
		h := computeDedupHash(tc.ts, tc.namespace, tc.pod, tc.container, tc.message)
		if prev, exists := hashes[h]; exists {
			t.Errorf("Hash collision: case %d has same hash as case %d", i, prev)
		}
		hashes[h] = i
	}
}

func TestMigrationFromOldSchema(t *testing.T) {
	// This test verifies that the fix for the "no such column: dedup_hash" error works.
	// It simulates an existing database created before the dedup_hash feature was added.
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "old.db")

	// Create a database with the old schema (without dedup_hash column)
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	// Old schema without dedup_hash column
	oldSchema := `
		CREATE TABLE logs (
			id          INTEGER PRIMARY KEY,
			timestamp   INTEGER NOT NULL,
			namespace   TEXT NOT NULL,
			pod         TEXT NOT NULL,
			container   TEXT NOT NULL,
			severity    INTEGER NOT NULL,
			message     TEXT NOT NULL,
			attributes  TEXT
		);

		CREATE INDEX idx_logs_k8s ON logs(namespace, pod, container);
		CREATE INDEX idx_logs_timestamp ON logs(timestamp DESC);
		CREATE INDEX idx_logs_severity ON logs(severity);

		CREATE VIRTUAL TABLE logs_fts USING fts5(
			message,
			content='logs',
			content_rowid='id',
			tokenize='porter unicode61 remove_diacritics 1'
		);

		CREATE TRIGGER logs_ai AFTER INSERT ON logs BEGIN
			INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
		END;

		CREATE TRIGGER logs_ad AFTER DELETE ON logs BEGIN
			INSERT INTO logs_fts(logs_fts, rowid, message)
				VALUES('delete', old.id, old.message);
		END;

		CREATE TRIGGER logs_au AFTER UPDATE ON logs BEGIN
			INSERT INTO logs_fts(logs_fts, rowid, message)
				VALUES('delete', old.id, old.message);
			INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
		END;

		CREATE TABLE users (
			id         INTEGER PRIMARY KEY,
			username   TEXT NOT NULL UNIQUE,
			password   TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);

		CREATE TABLE sessions (
			id         TEXT PRIMARY KEY,
			user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		);

		CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
	`

	if _, err := db.Exec(oldSchema); err != nil {
		db.Close()
		t.Fatalf("Failed to create old schema: %v", err)
	}

	// Insert some test data (without dedup_hash)
	_, err = db.Exec(`
		INSERT INTO logs (timestamp, namespace, pod, container, severity, message)
		VALUES (?, 'default', 'test-pod', 'app', 1, 'existing log entry')
	`, time.Now().UnixNano())
	if err != nil {
		db.Close()
		t.Fatalf("Failed to insert test data: %v", err)
	}

	db.Close()

	// Now open the database with the new Store (this would fail with the bug)
	store, err := New(Config{Path: dbPath})
	if err != nil {
		t.Fatalf("Failed to open store with old schema: %v", err)
	}
	defer store.Close()

	// Verify the dedup_hash column exists after migration
	hasColumn, err := columnExists(store.db, "logs", "dedup_hash")
	if err != nil {
		t.Fatalf("Failed to check column: %v", err)
	}
	if !hasColumn {
		t.Error("dedup_hash column should exist after migration")
	}

	// Verify the idx_logs_dedup index exists
	var indexExists bool
	err = store.db.QueryRow(`
		SELECT 1 FROM sqlite_master
		WHERE type='index' AND name='idx_logs_dedup'
	`).Scan(&indexExists)
	if err == sql.ErrNoRows {
		t.Error("idx_logs_dedup index should exist after migration")
	} else if err != nil {
		t.Fatalf("Failed to check index: %v", err)
	}

	// Verify existing data is still accessible
	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.TotalEntries != 1 {
		t.Errorf("Expected 1 existing entry, got %d", stats.TotalEntries)
	}

	// Verify we can write new entries with deduplication
	now := time.Now()
	entry := storage.LogEntry{
		Timestamp: now,
		Namespace: "default",
		Pod:       "new-pod",
		Container: "app",
		Severity:  storage.SeverityInfo,
		Message:   "new log entry",
	}
	store.Write(context.Background(), storage.LogBatch{entry})
	store.Write(context.Background(), storage.LogBatch{entry}) // duplicate
	store.Flush(context.Background())

	stats, err = store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	// Should have 2 entries: 1 existing + 1 new (duplicate is deduped)
	if stats.TotalEntries != 2 {
		t.Errorf("Expected 2 entries after write, got %d", stats.TotalEntries)
	}
}

func TestFreshDatabaseSchema(t *testing.T) {
	// Verify that new databases are created correctly with all schema elements
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "fresh.db")

	store, err := New(Config{Path: dbPath})
	if err != nil {
		t.Fatalf("Failed to create fresh store: %v", err)
	}
	defer store.Close()

	// Verify the dedup_hash column exists
	hasColumn, err := columnExists(store.db, "logs", "dedup_hash")
	if err != nil {
		t.Fatalf("Failed to check column: %v", err)
	}
	if !hasColumn {
		t.Error("dedup_hash column should exist in fresh database")
	}

	// Verify the idx_logs_dedup index exists
	var indexExists bool
	err = store.db.QueryRow(`
		SELECT 1 FROM sqlite_master
		WHERE type='index' AND name='idx_logs_dedup'
	`).Scan(&indexExists)
	if err == sql.ErrNoRows {
		t.Error("idx_logs_dedup index should exist in fresh database")
	} else if err != nil {
		t.Fatalf("Failed to check index: %v", err)
	}

	// Verify deduplication works
	now := time.Now()
	entry := storage.LogEntry{
		Timestamp: now,
		Namespace: "default",
		Pod:       "test-pod",
		Container: "app",
		Severity:  storage.SeverityInfo,
		Message:   "test message",
	}
	store.Write(context.Background(), storage.LogBatch{entry})
	store.Write(context.Background(), storage.LogBatch{entry})
	store.Flush(context.Background())

	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}
	if stats.TotalEntries != 1 {
		t.Errorf("Expected 1 entry after dedup, got %d", stats.TotalEntries)
	}
}
