package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/kubelogs/kubelogs/api/storagepb"
	"github.com/kubelogs/kubelogs/internal/loadgen"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func main() {
	// Parse configuration from flags
	cfg := loadgen.ParseFlags()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		flag.Usage()
		os.Exit(1)
	}

	// Initialize logger
	level := slog.LevelInfo
	if cfg.Verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})))

	slog.Info("kubelogs-loadgen starting",
		"version", Version,
		"addr", cfg.Addr,
		"rate", cfg.Rate,
		"duration", cfg.Duration,
		"batch_size", cfg.BatchSize,
	)

	// Create gRPC connection (following remote/client.go pattern)
	conn, err := grpc.NewClient(cfg.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		slog.Error("failed to connect", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := storagepb.NewStorageServiceClient(conn)

	// Setup context with cancellation and deadline
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutdown signal received")
		cancel()
	}()

	// Create generator and sender
	gen := loadgen.NewGenerator(cfg)
	sender := loadgen.NewSender(client, cfg.BatchSize)

	// Run the load generator
	if err := run(ctx, gen, sender, cfg); err != nil && err != context.DeadlineExceeded && err != context.Canceled {
		slog.Error("load generator error", "error", err)
		os.Exit(1)
	}

	// Print final statistics
	stats := sender.Stats()
	slog.Info("load generation complete",
		"total_logs", stats.TotalLogs,
		"total_batches", stats.TotalBatches,
		"errors", stats.Errors,
		"duration", time.Since(stats.StartTime).Round(time.Millisecond),
	)
}

func run(ctx context.Context, gen *loadgen.Generator, sender *loadgen.Sender, cfg loadgen.Config) error {
	// Calculate interval between logs
	interval := time.Second / time.Duration(cfg.Rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Flush any remaining logs
			return sender.Flush(context.Background())
		case <-ticker.C:
			entry := gen.Next()
			if err := sender.Send(ctx, entry); err != nil {
				slog.Warn("send failed", "error", err)
			}
		}
	}
}
