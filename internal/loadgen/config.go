package loadgen

import (
	"errors"
	"flag"
	"time"
)

// Config holds load generator configuration.
type Config struct {
	// Addr is the gRPC server address.
	Addr string

	// Rate is the number of logs per second to generate.
	Rate int

	// Duration is how long to run the generator.
	Duration time.Duration

	// BatchSize is the number of logs per batch sent to server.
	BatchSize int

	// Namespaces is the number of unique namespaces to generate.
	Namespaces int

	// Pods is the number of unique pods to generate.
	Pods int

	// ErrorRate is the percentage of logs that should be errors (0-100).
	ErrorRate int

	// Verbose enables debug logging.
	Verbose bool
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		Addr:       ":50051",
		Rate:       100,
		Duration:   time.Minute,
		BatchSize:  100,
		Namespaces: 5,
		Pods:       20,
		ErrorRate:  5,
		Verbose:    false,
	}
}

// ParseFlags parses command-line flags into Config.
func ParseFlags() Config {
	cfg := DefaultConfig()

	flag.StringVar(&cfg.Addr, "addr", cfg.Addr, "gRPC server address")
	flag.IntVar(&cfg.Rate, "rate", cfg.Rate, "logs per second")
	flag.DurationVar(&cfg.Duration, "duration", cfg.Duration, "how long to run")
	flag.IntVar(&cfg.BatchSize, "batch-size", cfg.BatchSize, "logs per batch")
	flag.IntVar(&cfg.Namespaces, "namespaces", cfg.Namespaces, "number of unique namespaces")
	flag.IntVar(&cfg.Pods, "pods", cfg.Pods, "number of unique pods")
	flag.IntVar(&cfg.ErrorRate, "error-rate", cfg.ErrorRate, "percentage of error logs (0-100)")
	flag.BoolVar(&cfg.Verbose, "v", cfg.Verbose, "enable verbose logging")

	flag.Parse()
	return cfg
}

// Validate checks if the configuration is valid.
func (c Config) Validate() error {
	if c.Addr == "" {
		return errors.New("addr cannot be empty")
	}
	if c.Rate <= 0 {
		return errors.New("rate must be positive")
	}
	if c.Duration <= 0 {
		return errors.New("duration must be positive")
	}
	if c.BatchSize <= 0 {
		return errors.New("batch-size must be positive")
	}
	if c.Namespaces <= 0 {
		return errors.New("namespaces must be positive")
	}
	if c.Pods <= 0 {
		return errors.New("pods must be positive")
	}
	if c.ErrorRate < 0 || c.ErrorRate > 100 {
		return errors.New("error-rate must be between 0 and 100")
	}
	return nil
}
