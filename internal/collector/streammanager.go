package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
)

// StreamManager coordinates multiple log streams with resource limits.
type StreamManager struct {
	clientset   kubernetes.Interface
	output      chan LogLine
	maxStreams  int
	bufferSize  int
	sinceTime   time.Time
	idleTimeout time.Duration
	parser      *Parser

	mu      sync.RWMutex
	streams map[string]*managedStream

	// Semaphore for limiting concurrent streams
	streamSem chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// managedStream wraps a Stream with its cancel function.
type managedStream struct {
	stream *Stream
	cancel context.CancelFunc
}

// NewStreamManager creates a stream coordinator.
func NewStreamManager(
	clientset kubernetes.Interface,
	maxStreams int,
	bufferSize int,
	sinceTime time.Time,
	idleTimeout time.Duration,
) *StreamManager {
	return &StreamManager{
		clientset:   clientset,
		output:      make(chan LogLine, bufferSize*10),
		maxStreams:  maxStreams,
		bufferSize:  bufferSize,
		sinceTime:   sinceTime,
		idleTimeout: idleTimeout,
		parser:      NewParser(),
		streams:     make(map[string]*managedStream),
		streamSem:   make(chan struct{}, maxStreams),
	}
}

// Output returns the channel where all log lines are sent.
func (m *StreamManager) Output() <-chan LogLine {
	return m.output
}

// Start initializes the stream manager.
func (m *StreamManager) Start(ctx context.Context) {
	m.ctx, m.cancel = context.WithCancel(ctx)
}

// StartStream begins streaming logs for a container.
// Returns immediately; stream runs in background.
// Blocks if at max capacity until a slot is available.
func (m *StreamManager) StartStream(ref ContainerRef) error {
	key := ref.Key()

	m.mu.Lock()
	if _, exists := m.streams[key]; exists {
		m.mu.Unlock()
		return nil // Already streaming
	}
	m.mu.Unlock()

	// Acquire semaphore slot (may block)
	select {
	case m.streamSem <- struct{}{}:
	case <-m.ctx.Done():
		return m.ctx.Err()
	}

	// Create stream-specific context
	streamCtx, streamCancel := context.WithCancel(m.ctx)

	stream := NewStream(m.clientset, ref, m.output, m.parser, m.sinceTime, m.idleTimeout)

	m.mu.Lock()
	// Double-check after acquiring semaphore
	if _, exists := m.streams[key]; exists {
		m.mu.Unlock()
		streamCancel()
		<-m.streamSem // Release slot
		return nil
	}
	m.streams[key] = &managedStream{
		stream: stream,
		cancel: streamCancel,
	}
	m.mu.Unlock()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.streams, key)
			m.mu.Unlock()
			<-m.streamSem // Release slot
		}()

		err := stream.Start(streamCtx)
		if err != nil && err != context.Canceled {
			slog.Warn("stream ended with error",
				"container", key,
				"error", err,
			)
		} else if err == nil {
			slog.Info("stream ended normally",
				"container", key,
				"linesRead", stream.Stats().LinesRead,
			)
		}
	}()

	return nil
}

// StopStream stops the stream for a container.
func (m *StreamManager) StopStream(ref ContainerRef) {
	key := ref.Key()

	m.mu.Lock()
	managed, exists := m.streams[key]
	m.mu.Unlock()

	if exists {
		managed.cancel()
	}
}

// StopAll stops all streams and waits for completion.
func (m *StreamManager) StopAll() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	// Close output channel to signal batcher that no more logs are coming
	close(m.output)
}

// ActiveStreams returns the number of active streams.
func (m *StreamManager) ActiveStreams() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.streams)
}

// Stats returns statistics for all active streams.
func (m *StreamManager) Stats() []StreamStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make([]StreamStats, 0, len(m.streams))
	for _, managed := range m.streams {
		stats = append(stats, managed.stream.Stats())
	}
	return stats
}
