package collector

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// Collector watches pods and streams container logs to storage.
type Collector struct {
	config    Config
	clientset kubernetes.Interface
	store     storage.Store

	discovery     *PodDiscovery
	streamManager *StreamManager
	batcher       *Batcher

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Metrics
	totalLinesRead atomic.Int64
	totalErrors    atomic.Int64
}

// CollectorStats contains collector statistics.
type CollectorStats struct {
	ActiveStreams  int
	TotalLinesRead int64
	TotalErrors    int64
	BatcherStats   BatcherStats
	StreamStats    []StreamStats
}

// New creates a new Collector.
func New(clientset kubernetes.Interface, store storage.Store, cfg Config) (*Collector, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &Collector{
		config:    cfg,
		clientset: clientset,
		store:     store,
	}, nil
}

// Start begins collecting logs. Blocks until ctx is canceled.
func (c *Collector) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	// Create components
	c.streamManager = NewStreamManager(
		c.clientset,
		c.config.MaxConcurrentStreams,
		c.config.StreamBufferSize,
		c.config.SinceTime,
	)
	c.streamManager.Start(c.ctx)

	c.batcher = NewBatcher(
		c.store,
		c.streamManager.Output(),
		c.config.BatchSize,
		c.config.BatchTimeout,
	)

	c.discovery = NewPodDiscovery(c.clientset, c.config.NodeName)

	// Start batcher (must be running before streams produce)
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.batcher.Run(c.ctx); err != nil && err != context.Canceled {
			slog.Error("batcher error", "error", err)
		}
	}()

	// Start pod discovery
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.discovery.Start(c.ctx); err != nil && err != context.Canceled {
			slog.Error("discovery error", "error", err)
		}
	}()

	slog.Info("collector started",
		"node", c.config.NodeName,
		"maxStreams", c.config.MaxConcurrentStreams,
		"batchSize", c.config.BatchSize,
	)

	// Main loop: process pod events
	for {
		select {
		case event := <-c.discovery.Events():
			c.handlePodEvent(event)
		case <-c.ctx.Done():
			return c.shutdown()
		}
	}
}

func (c *Collector) handlePodEvent(event PodEvent) {
	// Check namespace filter
	if !c.config.ShouldCollect(event.Container.Namespace) {
		return
	}

	switch event.Type {
	case ContainerStarted:
		slog.Debug("starting stream",
			"namespace", event.Container.Namespace,
			"pod", event.Container.PodName,
			"container", event.Container.ContainerName,
		)
		if err := c.streamManager.StartStream(event.Container); err != nil {
			slog.Error("failed to start stream",
				"container", event.Container.Key(),
				"error", err,
			)
			c.totalErrors.Add(1)
		}

	case ContainerStopped:
		slog.Debug("stopping stream",
			"namespace", event.Container.Namespace,
			"pod", event.Container.PodName,
			"container", event.Container.ContainerName,
		)
		c.streamManager.StopStream(event.Container)
	}
}

func (c *Collector) shutdown() error {
	slog.Info("collector shutting down")

	// Stop accepting new streams and stop existing ones
	c.streamManager.StopAll()

	// Wait for components with timeout
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		slog.Info("collector shutdown complete")
	case <-time.After(c.config.ShutdownTimeout):
		slog.Warn("collector shutdown timeout, some logs may be lost")
	}

	// Final flush
	if err := c.batcher.Flush(context.Background()); err != nil {
		slog.Error("final flush failed", "error", err)
	}

	return nil
}

// Stop gracefully shuts down the collector.
func (c *Collector) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}

// Stats returns current collector statistics.
func (c *Collector) Stats() CollectorStats {
	var batcherStats BatcherStats
	var streamStats []StreamStats
	activeStreams := 0

	if c.batcher != nil {
		batcherStats = c.batcher.Stats()
	}
	if c.streamManager != nil {
		streamStats = c.streamManager.Stats()
		activeStreams = c.streamManager.ActiveStreams()
	}

	return CollectorStats{
		ActiveStreams:  activeStreams,
		TotalLinesRead: c.totalLinesRead.Load(),
		TotalErrors:    c.totalErrors.Load(),
		BatcherStats:   batcherStats,
		StreamStats:    streamStats,
	}
}
