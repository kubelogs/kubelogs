package collector

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// errStreamClosedUnexpectedly indicates the stream closed but the container is still running.
var errStreamClosedUnexpectedly = errors.New("stream closed unexpectedly, container still running")

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
	Container  ContainerRef
	Timestamp  time.Time
	Severity   storage.Severity
	Message    string
	Attributes map[string]string // Extracted structured fields (nil if none)
}

// Stream reads logs from a single container.
type Stream struct {
	ref         ContainerRef
	clientset   kubernetes.Interface
	output      chan<- LogLine
	parser      *Parser
	sinceTime   time.Time
	idleTimeout time.Duration

	mu           sync.Mutex
	running      bool
	linesRead    int64
	errors       int
	lastError    error
	startedAt    time.Time
	lastSentTime time.Time // Cursor: timestamp of last successfully sent log
}

// StreamStats contains stream statistics.
type StreamStats struct {
	Container    ContainerRef
	Running      bool
	LinesRead    int64
	Errors       int
	LastError    error
	StartedAt    time.Time
	LastSentTime time.Time // Cursor position for debugging
}

// NewStream creates a stream for the given container.
func NewStream(
	clientset kubernetes.Interface,
	ref ContainerRef,
	output chan<- LogLine,
	parser *Parser,
	sinceTime time.Time,
	idleTimeout time.Duration,
) *Stream {
	return &Stream{
		ref:         ref,
		clientset:   clientset,
		output:      output,
		parser:      parser,
		sinceTime:   sinceTime,
		idleTimeout: idleTimeout,
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
		// Update sinceTime from cursor before each run attempt
		s.mu.Lock()
		if !s.lastSentTime.IsZero() {
			// Add 1ns to exclude the last sent log (SinceTime is inclusive)
			s.sinceTime = s.lastSentTime.Add(time.Nanosecond)
		}
		s.mu.Unlock()

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

	// Channel for scanner results - allows timeout detection
	type scanResult struct {
		hasNext bool
		line    string
	}
	scanCh := make(chan scanResult, 1)

	// scanNext runs scanner.Scan() in a goroutine with timeout detection
	scanNext := func() {
		hasNext := scanner.Scan()
		select {
		case scanCh <- scanResult{hasNext: hasNext, line: scanner.Text()}:
		case <-ctx.Done():
			// Context cancelled, goroutine will exit when scanCh is GC'd
		}
	}

	// Start first scan
	go scanNext()

	for {
		select {
		case result := <-scanCh:
			if !result.hasNext {
				// Scanner finished - check for errors
				if err := scanner.Err(); err != nil {
					return fmt.Errorf("read log stream: %w", err)
				}
				// Connection closed cleanly - check if container is still running
				// This distinguishes "pod terminated" from "connection dropped"
				if s.isContainerRunning(ctx) {
					slog.Debug("stream closed but container still running, will reconnect",
						"container", s.ref.Key(),
					)
					return errStreamClosedUnexpectedly
				}
				return nil // Pod actually terminated
			}

			// Parse and send the log line
			parsed := s.parser.Parse(result.line)
			logLine := LogLine{
				Container:  s.ref,
				Timestamp:  parsed.Timestamp,
				Severity:   parsed.Severity,
				Message:    parsed.Message,
				Attributes: parsed.Attributes,
			}

			select {
			case s.output <- logLine:
				s.mu.Lock()
				s.linesRead++
				if logLine.Timestamp.After(s.lastSentTime) {
					s.lastSentTime = logLine.Timestamp
				}
				s.mu.Unlock()
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
				// Output channel is full - log warning and continue
				slog.Warn("output channel full, dropping log line",
					"container", s.ref.Key(),
				)
				// Still update cursor to avoid re-sending dropped logs on reconnect
				s.mu.Lock()
				if logLine.Timestamp.After(s.lastSentTime) {
					s.lastSentTime = logLine.Timestamp
				}
				s.mu.Unlock()
			}

			// Start next scan
			go scanNext()

		case <-time.After(s.idleTimeout):
			// No log line received within idle timeout - connection may be stale
			slog.Warn("stream idle timeout, reconnecting",
				"container", s.ref.Key(),
				"idleTimeout", s.idleTimeout,
			)
			return fmt.Errorf("stream idle timeout after %v", s.idleTimeout)

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Stats returns stream statistics.
func (s *Stream) Stats() StreamStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	return StreamStats{
		Container:    s.ref,
		Running:      s.running,
		LinesRead:    s.linesRead,
		Errors:       s.errors,
		LastError:    s.lastError,
		StartedAt:    s.startedAt,
		LastSentTime: s.lastSentTime,
	}
}

// isContainerRunning checks if the container is still running in the cluster.
// Used to distinguish between "pod terminated" and "connection dropped".
func (s *Stream) isContainerRunning(ctx context.Context) bool {
	pod, err := s.clientset.CoreV1().Pods(s.ref.Namespace).Get(ctx, s.ref.PodName, metav1.GetOptions{})
	if err != nil {
		// Can't reach API server or pod doesn't exist - assume not running
		return false
	}

	// Verify the pod UID matches to handle pod name reuse
	if string(pod.UID) != s.ref.PodUID {
		return false
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == s.ref.ContainerName && cs.State.Running != nil {
			return true
		}
	}
	return false
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
