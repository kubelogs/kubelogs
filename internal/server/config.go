package server

import (
	"os"
	"strconv"
	"time"
)

// Config holds server configuration.
type Config struct {
	// ListenAddr is the gRPC server listen address.
	// Default: ":50051"
	ListenAddr string

	// HTTPListenAddr is the HTTP server listen address for the web UI.
	// Default: ":8080"
	HTTPListenAddr string

	// HTTPEnabled controls whether the HTTP server is started.
	// Default: true
	HTTPEnabled bool

	// DBPath is the path to the SQLite database file.
	// Default: "kubelogs.db"
	DBPath string

	// RetentionDays is the number of days to retain logs.
	// 0 means disabled (no automatic deletion).
	// Default: 0 (disabled)
	RetentionDays int

	// RetentionInterval is how often the retention cleanup runs.
	// Default: 1 hour
	RetentionInterval time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ListenAddr:        ":50051",
		HTTPListenAddr:    ":8080",
		HTTPEnabled:       true,
		DBPath:            "kubelogs.db",
		RetentionDays:     0,
		RetentionInterval: time.Hour,
	}
}

// ConfigFromEnv creates a Config from environment variables.
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("KUBELOGS_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}

	if v := os.Getenv("KUBELOGS_HTTP_ADDR"); v != "" {
		cfg.HTTPListenAddr = v
	}

	if v := os.Getenv("KUBELOGS_HTTP_ENABLED"); v == "false" {
		cfg.HTTPEnabled = false
	}

	if v := os.Getenv("KUBELOGS_DB_PATH"); v != "" {
		cfg.DBPath = v
	}

	if v := os.Getenv("KUBELOGS_RETENTION_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.RetentionDays = n
		}
	}

	return cfg
}

// RetentionEnabled returns true if log retention is configured.
func (c Config) RetentionEnabled() bool {
	return c.RetentionDays > 0
}

// RetentionCutoff returns the time before which logs should be deleted.
func (c Config) RetentionCutoff() time.Time {
	return time.Now().Add(-time.Duration(c.RetentionDays) * 24 * time.Hour)
}
