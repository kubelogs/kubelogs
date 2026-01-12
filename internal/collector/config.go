package collector

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Config holds collector configuration.
type Config struct {
	// NodeName filters pods to only those on this node.
	// Required for DaemonSet deployment. Uses NODE_NAME env var.
	NodeName string

	// MaxConcurrentStreams limits active log streams.
	// Default: 100. Prevents memory exhaustion.
	MaxConcurrentStreams int

	// BatchSize is entries to buffer before storage write.
	// Default: 500. Balances latency vs efficiency.
	BatchSize int

	// BatchTimeout forces flush after this duration.
	// Default: 5s. Ensures logs aren't delayed too long.
	BatchTimeout time.Duration

	// StreamBufferSize is the channel buffer per stream.
	// Default: 1000 lines. Provides backpressure relief.
	StreamBufferSize int

	// SinceTime starts collecting logs from this time.
	// Zero means collect from pod start.
	// Default: 15 minutes.
	SinceTime time.Time

	// ExcludeNamespaces skips these namespaces.
	// Default: ["kube-system"]. Reduces noise.
	ExcludeNamespaces []string

	// IncludeNamespaces only collects from these namespaces.
	// Empty means all namespaces (except excluded).
	IncludeNamespaces []string

	// ShutdownTimeout is max time to drain logs on shutdown.
	// Default: 30s.
	ShutdownTimeout time.Duration
}

// DefaultConfig returns sensible defaults for <256MB RAM constraint.
func DefaultConfig() Config {
	return Config{
		MaxConcurrentStreams: 100,
		BatchSize:            500,
		BatchTimeout:         5 * time.Second,
		StreamBufferSize:     1000,
		ExcludeNamespaces:    []string{"kube-system"},
		ShutdownTimeout:      30 * time.Second,
		SinceTime:            time.Now().Add(-(15 * time.Minute)),
	}
}

// ConfigFromEnv creates a Config from environment variables.
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	cfg.NodeName = os.Getenv("NODE_NAME")

	if v := os.Getenv("KUBELOGS_MAX_STREAMS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxConcurrentStreams = n
		}
	}

	if v := os.Getenv("KUBELOGS_BATCH_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BatchSize = n
		}
	}

	if v := os.Getenv("KUBELOGS_BATCH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.BatchTimeout = d
		}
	}

	if v := os.Getenv("KUBELOGS_STREAM_BUFFER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.StreamBufferSize = n
		}
	}

	if v := os.Getenv("KUBELOGS_SINCE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.SinceTime = time.Now().Add(-d)
		}
	}

	if v := os.Getenv("KUBELOGS_EXCLUDE_NS"); v != "" {
		cfg.ExcludeNamespaces = splitTrim(v, ",")
	}

	if v := os.Getenv("KUBELOGS_INCLUDE_NS"); v != "" {
		cfg.IncludeNamespaces = splitTrim(v, ",")
	}

	if v := os.Getenv("KUBELOGS_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.ShutdownTimeout = d
		}
	}

	return cfg
}

// Validate checks if the configuration is valid.
func (c Config) Validate() error {
	if c.NodeName == "" {
		return &ConfigError{Field: "NodeName", Message: "NODE_NAME environment variable is required"}
	}
	if c.MaxConcurrentStreams <= 0 {
		return &ConfigError{Field: "MaxConcurrentStreams", Message: "must be positive"}
	}
	if c.BatchSize <= 0 {
		return &ConfigError{Field: "BatchSize", Message: "must be positive"}
	}
	if c.BatchTimeout <= 0 {
		return &ConfigError{Field: "BatchTimeout", Message: "must be positive"}
	}
	if c.StreamBufferSize <= 0 {
		return &ConfigError{Field: "StreamBufferSize", Message: "must be positive"}
	}
	if c.ShutdownTimeout <= 0 {
		return &ConfigError{Field: "ShutdownTimeout", Message: "must be positive"}
	}
	return nil
}

// ShouldCollect returns true if logs from the given namespace should be collected.
func (c Config) ShouldCollect(namespace string) bool {
	// Check exclusions first
	if slices.Contains(c.ExcludeNamespaces, namespace) {
		return false
	}

	// If include list is empty, collect all (except excluded)
	if len(c.IncludeNamespaces) == 0 {
		return true
	}

	// Check if in include list
	return slices.Contains(c.IncludeNamespaces, namespace)
}

// ConfigError represents a configuration validation error.
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return "config: " + e.Field + ": " + e.Message
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
