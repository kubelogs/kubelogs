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

	// AuthEnabled enables authentication when true.
	// Default: false (disabled)
	AuthEnabled bool

	// SessionDuration is how long sessions remain valid.
	// Default: 24 hours
	SessionDuration time.Duration

	// SessionCookieName is the name of the session cookie.
	// Default: "kubelogs_session"
	SessionCookieName string

	// SessionCookieSecure sets the Secure flag on session cookies.
	// Default: true
	SessionCookieSecure bool
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		ListenAddr:          ":50051",
		HTTPListenAddr:      ":8080",
		HTTPEnabled:         true,
		DBPath:              "kubelogs.db",
		RetentionDays:       0,
		RetentionInterval:   time.Hour,
		AuthEnabled:         false,
		SessionDuration:     24 * time.Hour,
		SessionCookieName:   "kubelogs_session",
		SessionCookieSecure: true,
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

	if v := os.Getenv("KUBELOGS_AUTH_ENABLED"); v == "true" {
		cfg.AuthEnabled = true
	}

	if v := os.Getenv("KUBELOGS_SESSION_DURATION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionDuration = d
		}
	}

	if v := os.Getenv("KUBELOGS_SESSION_SECURE"); v == "false" {
		cfg.SessionCookieSecure = false
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
