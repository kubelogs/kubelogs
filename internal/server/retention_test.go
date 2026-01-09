package server

import (
	"context"
	"testing"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
	"github.com/kubelogs/kubelogs/internal/storage/sqlite"
)

func TestRetentionWorker_DeletesOldLogs(t *testing.T) {
	store, err := sqlite.New(sqlite.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()
	oldTime := now.Add(-48 * time.Hour) // 2 days ago

	entries := storage.LogBatch{
		{Timestamp: oldTime, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "old"},
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "new"},
	}
	store.Write(ctx, entries)
	store.Flush(ctx)

	// Configure 1-day retention
	cfg := Config{
		RetentionDays:     1,
		RetentionInterval: time.Hour,
	}

	worker := NewRetentionWorker(store, cfg)
	worker.runOnce(ctx)

	// Verify old log deleted, new log remains
	stats := worker.Stats()
	if stats.TotalDeleted != 1 {
		t.Errorf("Expected 1 deleted, got %d", stats.TotalDeleted)
	}

	result, err := store.Query(ctx, storage.Query{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Errorf("Expected 1 entry remaining, got %d", len(result.Entries))
	}
	if result.Entries[0].Message != "new" {
		t.Errorf("Expected 'new' message, got %q", result.Entries[0].Message)
	}
}

func TestRetentionWorker_DisabledWhenZeroDays(t *testing.T) {
	cfg := Config{
		RetentionDays:     0,
		RetentionInterval: time.Millisecond,
	}

	if cfg.RetentionEnabled() {
		t.Error("RetentionEnabled should return false when days=0")
	}
}

func TestRetentionWorker_GracefulShutdown(t *testing.T) {
	store, err := sqlite.New(sqlite.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	cfg := Config{
		RetentionDays:     7,
		RetentionInterval: 10 * time.Millisecond,
	}

	worker := NewRetentionWorker(store, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(done)
	}()

	// Let it run a few cycles
	time.Sleep(50 * time.Millisecond)

	// Cancel and verify clean shutdown
	cancel()

	select {
	case <-done:
		// Success - worker stopped
	case <-time.After(time.Second):
		t.Error("Worker did not stop within timeout")
	}
}

func TestRetentionWorker_StatsTracking(t *testing.T) {
	store, err := sqlite.New(sqlite.Config{Path: ":memory:"})
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Insert logs at different ages
	entries := storage.LogBatch{
		{Timestamp: now.Add(-72 * time.Hour), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "very old"},
		{Timestamp: now.Add(-48 * time.Hour), Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "old"},
		{Timestamp: now, Namespace: "ns", Pod: "pod", Container: "c", Severity: storage.SeverityInfo, Message: "new"},
	}
	store.Write(ctx, entries)
	store.Flush(ctx)

	cfg := Config{
		RetentionDays:     1,
		RetentionInterval: time.Hour,
	}

	worker := NewRetentionWorker(store, cfg)

	// Run twice to accumulate stats
	worker.runOnce(ctx)
	worker.runOnce(ctx)

	stats := worker.Stats()
	if stats.TotalRuns != 2 {
		t.Errorf("Expected 2 runs, got %d", stats.TotalRuns)
	}
	if stats.TotalDeleted != 2 {
		t.Errorf("Expected 2 total deleted, got %d", stats.TotalDeleted)
	}
	if stats.LastRunTime.IsZero() {
		t.Error("LastRunTime should be set")
	}
	if stats.LastRunError != nil {
		t.Errorf("LastRunError should be nil, got %v", stats.LastRunError)
	}
}

func TestConfigFromEnv(t *testing.T) {
	// Test defaults
	cfg := DefaultConfig()
	if cfg.RetentionDays != 0 {
		t.Errorf("Default retention days should be 0, got %d", cfg.RetentionDays)
	}
	if cfg.ListenAddr != ":50051" {
		t.Errorf("Default listen addr should be :50051, got %s", cfg.ListenAddr)
	}
	if cfg.DBPath != "kubelogs.db" {
		t.Errorf("Default DB path should be kubelogs.db, got %s", cfg.DBPath)
	}
	if cfg.RetentionInterval != time.Hour {
		t.Errorf("Default retention interval should be 1h, got %v", cfg.RetentionInterval)
	}

	// Test with env var
	t.Setenv("KUBELOGS_RETENTION_DAYS", "30")
	cfg = ConfigFromEnv()
	if cfg.RetentionDays != 30 {
		t.Errorf("Expected retention days 30, got %d", cfg.RetentionDays)
	}

	// Test invalid value (should use default)
	t.Setenv("KUBELOGS_RETENTION_DAYS", "-5")
	cfg = ConfigFromEnv()
	if cfg.RetentionDays != 0 {
		t.Errorf("Invalid retention days should default to 0, got %d", cfg.RetentionDays)
	}

	// Test non-numeric value (should use default)
	t.Setenv("KUBELOGS_RETENTION_DAYS", "abc")
	cfg = ConfigFromEnv()
	if cfg.RetentionDays != 0 {
		t.Errorf("Non-numeric retention days should default to 0, got %d", cfg.RetentionDays)
	}
}

func TestRetentionCutoff(t *testing.T) {
	cfg := Config{
		RetentionDays: 7,
	}

	before := time.Now()
	cutoff := cfg.RetentionCutoff()
	after := time.Now()

	expectedBefore := before.Add(-7 * 24 * time.Hour)
	expectedAfter := after.Add(-7 * 24 * time.Hour)

	if cutoff.Before(expectedBefore) || cutoff.After(expectedAfter) {
		t.Errorf("Cutoff %v not in expected range [%v, %v]", cutoff, expectedBefore, expectedAfter)
	}
}
