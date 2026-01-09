package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/kubelogs/kubelogs/api/storagepb"
	"github.com/kubelogs/kubelogs/internal/server"
	"github.com/kubelogs/kubelogs/internal/storage/sqlite"
)

func main() {
	// Configuration from environment
	listenAddr := getEnv("KUBELOGS_LISTEN_ADDR", ":50051")
	dbPath := getEnv("KUBELOGS_DB_PATH", "kubelogs.db")

	// Initialize logger
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Open SQLite store
	store, err := sqlite.New(sqlite.Config{Path: dbPath})
	if err != nil {
		slog.Error("failed to open database", "path", dbPath, "error", err)
		os.Exit(1)
	}
	defer store.Close()

	slog.Info("database opened", "path", dbPath)

	// Create gRPC server
	grpcServer := grpc.NewServer()
	storagepb.RegisterStorageServiceServer(grpcServer, server.New(store))

	// Register health check service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register reflection for debugging
	reflection.Register(grpcServer)

	// Start listening
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		slog.Error("failed to listen", "address", listenAddr, "error", err)
		os.Exit(1)
	}

	slog.Info("server starting", "address", listenAddr)

	// Handle shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
