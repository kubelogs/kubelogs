package loadgen

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubelogs/kubelogs/api/storagepb"
)

// SenderStats contains statistics about sent logs.
type SenderStats struct {
	TotalLogs    int64
	TotalBatches int64
	Errors       int64
	StartTime    time.Time
}

// Sender batches and sends logs to the gRPC server.
type Sender struct {
	client    storagepb.StorageServiceClient
	batchSize int

	mu        sync.Mutex
	buffer    []*storagepb.LogEntry
	startTime time.Time

	// Metrics
	totalLogs    atomic.Int64
	totalBatches atomic.Int64
	errors       atomic.Int64
}

// NewSender creates a new log sender.
func NewSender(client storagepb.StorageServiceClient, batchSize int) *Sender {
	return &Sender{
		client:    client,
		batchSize: batchSize,
		buffer:    make([]*storagepb.LogEntry, 0, batchSize),
		startTime: time.Now(),
	}
}

// Send adds a log entry to the buffer and flushes if batch is full.
func (s *Sender) Send(ctx context.Context, entry *storagepb.LogEntry) error {
	s.mu.Lock()
	s.buffer = append(s.buffer, entry)
	shouldFlush := len(s.buffer) >= s.batchSize
	s.mu.Unlock()

	if shouldFlush {
		return s.Flush(ctx)
	}
	return nil
}

// Flush sends all buffered logs to the server.
func (s *Sender) Flush(ctx context.Context) error {
	s.mu.Lock()
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		return nil
	}

	batch := s.buffer
	s.buffer = make([]*storagepb.LogEntry, 0, s.batchSize)
	s.mu.Unlock()

	req := &storagepb.WriteRequest{
		Entries: batch,
	}

	resp, err := s.client.Write(ctx, req)
	if err != nil {
		s.errors.Add(1)
		slog.Error("failed to write batch",
			"entries", len(batch),
			"error", err,
		)
		return err
	}

	s.totalLogs.Add(int64(resp.Count))
	s.totalBatches.Add(1)

	slog.Debug("batch sent",
		"entries", resp.Count,
		"total", s.totalLogs.Load(),
	)

	return nil
}

// Stats returns sender statistics.
func (s *Sender) Stats() SenderStats {
	return SenderStats{
		TotalLogs:    s.totalLogs.Load(),
		TotalBatches: s.totalBatches.Load(),
		Errors:       s.errors.Load(),
		StartTime:    s.startTime,
	}
}
