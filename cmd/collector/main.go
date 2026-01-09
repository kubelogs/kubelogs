package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kubelogs/kubelogs/internal/collector"
	"github.com/kubelogs/kubelogs/internal/storage"
	"github.com/kubelogs/kubelogs/internal/storage/remote"
	"github.com/kubelogs/kubelogs/internal/storage/sqlite"
)

func main() {
	// Initialize logger
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Load collector configuration
	cfg := collector.ConfigFromEnv()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// Initialize storage
	store, err := initStore()
	if err != nil {
		slog.Error("failed to initialize storage", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Initialize Kubernetes client
	clientset, err := initKubernetesClient()
	if err != nil {
		slog.Error("failed to initialize kubernetes client", "error", err)
		os.Exit(1)
	}

	// Create collector
	c, err := collector.New(clientset, store, cfg)
	if err != nil {
		slog.Error("failed to create collector", "error", err)
		os.Exit(1)
	}

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh

		slog.Info("shutdown signal received")
		cancel()
	}()

	// Start collector
	slog.Info("collector starting",
		"node", cfg.NodeName,
		"storageAddr", os.Getenv("KUBELOGS_STORAGE_ADDR"),
	)

	if err := c.Start(ctx); err != nil && err != context.Canceled {
		slog.Error("collector error", "error", err)
		os.Exit(1)
	}

	slog.Info("collector stopped")
}

// initStore initializes the storage backend.
// Uses remote storage if KUBELOGS_STORAGE_ADDR is set, otherwise local SQLite.
func initStore() (storage.Store, error) {
	if addr := os.Getenv("KUBELOGS_STORAGE_ADDR"); addr != "" {
		slog.Info("using remote storage", "address", addr)
		return remote.NewClient(addr)
	}

	dbPath := os.Getenv("KUBELOGS_DB_PATH")
	if dbPath == "" {
		dbPath = "kubelogs.db"
	}

	slog.Info("using local storage", "path", dbPath)
	return sqlite.New(sqlite.Config{Path: dbPath})
}

// initKubernetesClient initializes the Kubernetes client.
// Uses in-cluster config if available, falls back to kubeconfig.
func initKubernetesClient() (kubernetes.Interface, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig
		kubeconfig := os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = os.Getenv("HOME") + "/.kube/config"
		}

		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	}

	return kubernetes.NewForConfig(config)
}
