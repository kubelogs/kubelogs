package collector

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxConcurrentStreams != 100 {
		t.Errorf("MaxConcurrentStreams = %d, want 100", cfg.MaxConcurrentStreams)
	}
	if cfg.BatchSize != 500 {
		t.Errorf("BatchSize = %d, want 500", cfg.BatchSize)
	}
	if cfg.BatchTimeout != 5*time.Second {
		t.Errorf("BatchTimeout = %v, want 5s", cfg.BatchTimeout)
	}
	if cfg.StreamBufferSize != 1000 {
		t.Errorf("StreamBufferSize = %d, want 1000", cfg.StreamBufferSize)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 30s", cfg.ShutdownTimeout)
	}
	if cfg.StreamIdleTimeout != 5*time.Minute {
		t.Errorf("StreamIdleTimeout = %v, want 5m", cfg.StreamIdleTimeout)
	}
	if len(cfg.ExcludeNamespaces) != 1 || cfg.ExcludeNamespaces[0] != "kube-system" {
		t.Errorf("ExcludeNamespaces = %v, want [kube-system]", cfg.ExcludeNamespaces)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				NodeName:             "node-1",
				MaxConcurrentStreams: 100,
				BatchSize:            500,
				BatchTimeout:         5 * time.Second,
				StreamBufferSize:     1000,
				ShutdownTimeout:      30 * time.Second,
				StreamIdleTimeout:    5 * time.Minute,
			},
			wantErr: false,
		},
		{
			name: "missing node name",
			cfg: Config{
				MaxConcurrentStreams: 100,
				BatchSize:            500,
				BatchTimeout:         5 * time.Second,
				StreamBufferSize:     1000,
				ShutdownTimeout:      30 * time.Second,
				StreamIdleTimeout:    5 * time.Minute,
			},
			wantErr: true,
		},
		{
			name: "zero max streams",
			cfg: Config{
				NodeName:             "node-1",
				MaxConcurrentStreams: 0,
				BatchSize:            500,
				BatchTimeout:         5 * time.Second,
				StreamBufferSize:     1000,
				ShutdownTimeout:      30 * time.Second,
				StreamIdleTimeout:    5 * time.Minute,
			},
			wantErr: true,
		},
		{
			name: "zero batch size",
			cfg: Config{
				NodeName:             "node-1",
				MaxConcurrentStreams: 100,
				BatchSize:            0,
				BatchTimeout:         5 * time.Second,
				StreamBufferSize:     1000,
				ShutdownTimeout:      30 * time.Second,
				StreamIdleTimeout:    5 * time.Minute,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_ShouldCollect(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		namespace string
		want      bool
	}{
		{
			name: "default excludes kube-system",
			cfg: Config{
				ExcludeNamespaces: []string{"kube-system"},
			},
			namespace: "kube-system",
			want:      false,
		},
		{
			name: "default allows other namespaces",
			cfg: Config{
				ExcludeNamespaces: []string{"kube-system"},
			},
			namespace: "default",
			want:      true,
		},
		{
			name: "include list only",
			cfg: Config{
				IncludeNamespaces: []string{"production", "staging"},
			},
			namespace: "production",
			want:      true,
		},
		{
			name: "include list excludes other",
			cfg: Config{
				IncludeNamespaces: []string{"production", "staging"},
			},
			namespace: "development",
			want:      false,
		},
		{
			name: "exclude takes precedence",
			cfg: Config{
				ExcludeNamespaces: []string{"kube-system"},
				IncludeNamespaces: []string{"kube-system", "default"},
			},
			namespace: "kube-system",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.ShouldCollect(tt.namespace)
			if got != tt.want {
				t.Errorf("ShouldCollect(%q) = %v, want %v", tt.namespace, got, tt.want)
			}
		})
	}
}
