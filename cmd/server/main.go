package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	"github.com/kubelogs/kubelogs/api/storagepb"
	"github.com/kubelogs/kubelogs/internal/server"
	"github.com/kubelogs/kubelogs/internal/storage/sqlite"
)

func main() {
	// Load configuration from environment
	cfg := server.ConfigFromEnv()

	// Initialize logger
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Open SQLite store
	store, err := sqlite.New(sqlite.Config{Path: cfg.DBPath})
	if err != nil {
		slog.Error("failed to open database", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	defer store.Close()

	slog.Info("database opened", "path", cfg.DBPath)

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start retention worker (if enabled)
	if cfg.RetentionEnabled() {
		retentionWorker := server.NewRetentionWorker(store, cfg)
		go retentionWorker.Run(ctx)
	}

	// Create gRPC server with keepalive to detect dead connections
	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    15 * time.Second, // Ping client every 15s if idle
			Timeout: 5 * time.Second,  // Wait 5s for ping ack
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second, // Minimum time between client pings
			PermitWithoutStream: true,
		}),
	)
	storagepb.RegisterStorageServiceServer(grpcServer, server.New(store))

	// Register health check service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register reflection for debugging
	reflection.Register(grpcServer)

	// Start HTTP server for web UI
	if cfg.HTTPEnabled {
		httpServer, err := server.NewHTTPServer(store)
		if err != nil {
			slog.Error("failed to create HTTP server", "error", err)
			os.Exit(1)
		}

		go func() {
			slog.Info("HTTP server starting", "address", cfg.HTTPListenAddr)
			if err := http.ListenAndServe(cfg.HTTPListenAddr, httpServer.Routes()); err != nil && err != http.ErrServerClosed {
				slog.Error("HTTP server error", "error", err)
			}
		}()
	}

	// Start listening
	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		slog.Error("failed to listen", "address", cfg.ListenAddr, "error", err)
		os.Exit(1)
	}

	slog.Info("server starting",
		"grpc_address", cfg.ListenAddr,
		"http_address", cfg.HTTPListenAddr,
		"http_enabled", cfg.HTTPEnabled,
		"retention_days", cfg.RetentionDays,
	)

	// Handle shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		slog.Info("shutdown signal received")
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		grpcServer.GracefulStop()
		cancel()
	}()

	// Serve
	if err := grpcServer.Serve(lis); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}

	<-ctx.Done()
	slog.Info("server stopped")
}
