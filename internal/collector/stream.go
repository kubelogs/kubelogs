package collector

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// ContainerRef uniquely identifies a container.
type ContainerRef struct {
	Namespace     string
	PodName       string
	PodUID        string // Distinguish restarted pods with same name
	ContainerName string
}

// Key returns a unique string key for map lookups.
func (c ContainerRef) Key() string {
	return fmt.Sprintf("%s/%s/%s/%s", c.Namespace, c.PodName, c.PodUID, c.ContainerName)
}

// LogLine represents a parsed log line from a container.
type LogLine struct {
	Container ContainerRef
	Timestamp time.Time
	Severity  storage.Severity
	Message   string
}

// Stream reads logs from a single container.
type Stream struct {
	ref       ContainerRef
	clientset kubernetes.Interface
	output    chan<- LogLine
	parser    *Parser
	sinceTime time.Time

	mu        sync.Mutex
	running   bool
	linesRead int64
	errors    int
	lastError error
	startedAt time.Time
}

// StreamStats contains stream statistics.
type StreamStats struct {
	Container ContainerRef
	Running   bool
	LinesRead int64
	Errors    int
	LastError error
	StartedAt time.Time
}

// NewStream creates a stream for the given container.
func NewStream(
	clientset kubernetes.Interface,
	ref ContainerRef,
	output chan<- LogLine,
	parser *Parser,
	sinceTime time.Time,
) *Stream {
	return &Stream{
		ref:       ref,
		clientset: clientset,
		output:    output,
		parser:    parser,
		sinceTime: sinceTime,
	}
}

// Start begins streaming logs. Blocks until stream ends or ctx is canceled.
// Implements automatic retry with exponential backoff.
func (s *Stream) Start(ctx context.Context) error {
	s.mu.Lock()
	s.running = true
	s.startedAt = time.Now()
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		err := s.run(ctx)
		if err == nil {
			return nil // Normal termination (pod finished)
		}

		if ctx.Err() != nil {
			return ctx.Err() // Shutdown requested
		}

		if !isRetryable(err) {
			s.mu.Lock()
			s.lastError = err
			s.mu.Unlock()
			return err
		}

		s.mu.Lock()
		s.errors++
		s.lastError = err
		s.mu.Unlock()

		select {
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *Stream) run(ctx context.Context) error {
	opts := &corev1.PodLogOptions{
		Container:  s.ref.ContainerName,
		Follow:     true,
		Timestamps: true,
	}

	if !s.sinceTime.IsZero() {
		sinceTime := metav1.NewTime(s.sinceTime)
		opts.SinceTime = &sinceTime
	}

	req := s.clientset.CoreV1().Pods(s.ref.Namespace).GetLogs(s.ref.PodName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("open log stream: %w", err)
	}
	defer stream.Close()

	scanner := bufio.NewScanner(stream)
	// Increase buffer size for long log lines
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		timestamp, severity, message := s.parser.Parse(line)
		logLine := LogLine{
			Container: s.ref,
			Timestamp: timestamp,
			Severity:  severity,
			Message:   message,
		}

		select {
		case s.output <- logLine:
			s.mu.Lock()
			s.linesRead++
			s.mu.Unlock()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read log stream: %w", err)
	}

	return nil // Stream ended normally (pod terminated)
}

// Stats returns stream statistics.
func (s *Stream) Stats() StreamStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	return StreamStats{
		Container: s.ref,
		Running:   s.running,
		LinesRead: s.linesRead,
		Errors:    s.errors,
		LastError: s.lastError,
		StartedAt: s.startedAt,
	}
}

// isRetryable returns true if the error is worth retrying.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation is not retryable
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// EOF means stream ended normally
	if errors.Is(err, io.EOF) {
		return false
	}

	// Connection errors are generally retryable
	// TODO: Add more specific error checks for k8s API errors
	return true
}
