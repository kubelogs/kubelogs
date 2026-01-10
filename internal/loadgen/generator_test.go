package loadgen

import (
	"testing"
	"time"
)

func TestGenerator_Next(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Namespaces = 3
	cfg.Pods = 5

	gen := NewGenerator(cfg)

	// Generate 100 entries and verify they have valid fields
	for i := 0; i < 100; i++ {
		entry := gen.Next()

		if entry.Namespace == "" {
			t.Error("namespace should not be empty")
		}
		if entry.Pod == "" {
			t.Error("pod should not be empty")
		}
		if entry.Container == "" {
			t.Error("container should not be empty")
		}
		if entry.Message == "" {
			t.Error("message should not be empty")
		}
		if entry.TimestampNanos == 0 {
			t.Error("timestamp should not be zero")
		}
		if entry.Severity > 6 {
			t.Errorf("invalid severity: %d", entry.Severity)
		}
	}
}

func TestGenerator_SeverityDistribution(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ErrorRate = 20 // 20% errors

	gen := NewGenerator(cfg)

	var errors, fatals int
	const iterations = 10000

	for i := 0; i < iterations; i++ {
		entry := gen.Next()
		if entry.Severity == 5 {
			errors++
		}
		if entry.Severity == 6 {
			fatals++
		}
	}

	// Allow tolerance for randomness
	errorRate := float64(errors+fatals) / float64(iterations) * 100
	if errorRate < 10 || errorRate > 30 {
		t.Errorf("error rate %.1f%% outside expected range (10-30%%)", errorRate)
	}
}

func TestGenerator_UniqueNamespaces(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Namespaces = 3
	cfg.Pods = 10

	gen := NewGenerator(cfg)

	namespaces := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		entry := gen.Next()
		namespaces[entry.Namespace] = true
	}

	if len(namespaces) > cfg.Namespaces {
		t.Errorf("expected at most %d namespaces, got %d", cfg.Namespaces, len(namespaces))
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{"valid default", func(c *Config) {}, false},
		{"empty addr", func(c *Config) { c.Addr = "" }, true},
		{"zero rate", func(c *Config) { c.Rate = 0 }, true},
		{"negative rate", func(c *Config) { c.Rate = -1 }, true},
		{"zero duration", func(c *Config) { c.Duration = 0 }, true},
		{"zero batch size", func(c *Config) { c.BatchSize = 0 }, true},
		{"zero namespaces", func(c *Config) { c.Namespaces = 0 }, true},
		{"zero pods", func(c *Config) { c.Pods = 0 }, true},
		{"error rate > 100", func(c *Config) { c.ErrorRate = 101 }, true},
		{"error rate < 0", func(c *Config) { c.ErrorRate = -1 }, true},
		{"valid high rate", func(c *Config) { c.Rate = 100000 }, false},
		{"valid long duration", func(c *Config) { c.Duration = 24 * time.Hour }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.modify(&cfg)
			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Addr != ":50051" {
		t.Errorf("expected default addr :50051, got %s", cfg.Addr)
	}
	if cfg.Rate != 100 {
		t.Errorf("expected default rate 100, got %d", cfg.Rate)
	}
	if cfg.Duration != time.Minute {
		t.Errorf("expected default duration 1m, got %v", cfg.Duration)
	}
	if cfg.BatchSize != 100 {
		t.Errorf("expected default batch size 100, got %d", cfg.BatchSize)
	}
	if cfg.ErrorRate != 5 {
		t.Errorf("expected default error rate 5, got %d", cfg.ErrorRate)
	}
}
