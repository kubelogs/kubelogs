package collector

import (
	"testing"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

func TestParser_KubernetesTimestamp(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		line        string
		wantMessage string
		wantSev     storage.Severity
	}{
		{
			name:        "RFC3339Nano timestamp",
			line:        "2024-01-15T10:30:00.123456789Z Hello world",
			wantMessage: "Hello world",
			wantSev:     storage.SeverityUnknown,
		},
		{
			name:        "RFC3339 timestamp",
			line:        "2024-01-15T10:30:00Z Hello world",
			wantMessage: "Hello world",
			wantSev:     storage.SeverityUnknown,
		},
		{
			name:        "No timestamp",
			line:        "Hello world",
			wantMessage: "Hello world",
			wantSev:     storage.SeverityUnknown,
		},
		{
			name:        "With severity bracket",
			line:        "2024-01-15T10:30:00Z [INFO] Application started",
			wantMessage: "[INFO] Application started",
			wantSev:     storage.SeverityInfo,
		},
		{
			name:        "With severity bracket ERROR",
			line:        "2024-01-15T10:30:00Z [ERROR] Something failed",
			wantMessage: "[ERROR] Something failed",
			wantSev:     storage.SeverityError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, sev, msg := parser.Parse(tt.line)

			if msg != tt.wantMessage {
				t.Errorf("message = %q, want %q", msg, tt.wantMessage)
			}

			if sev != tt.wantSev {
				t.Errorf("severity = %v, want %v", sev, tt.wantSev)
			}

			// For lines with timestamps, verify it's not the current time
			if len(tt.line) > 20 && tt.line[4] == '-' {
				if time.Since(ts) < time.Second {
					t.Errorf("timestamp should be parsed, not current time")
				}
			}
		})
	}
}

func TestParser_JSONLogs(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name    string
		line    string
		wantSev storage.Severity
	}{
		{
			name:    "JSON level field",
			line:    `2024-01-15T10:30:00Z {"level":"INFO","message":"test"}`,
			wantSev: storage.SeverityInfo,
		},
		{
			name:    "JSON level ERROR",
			line:    `2024-01-15T10:30:00Z {"level":"ERROR","message":"test"}`,
			wantSev: storage.SeverityError,
		},
		{
			name:    "JSON severity field",
			line:    `2024-01-15T10:30:00Z {"severity":"WARN","message":"test"}`,
			wantSev: storage.SeverityWarn,
		},
		{
			name:    "JSON lvl field",
			line:    `2024-01-15T10:30:00Z {"lvl":"DEBUG","message":"test"}`,
			wantSev: storage.SeverityDebug,
		},
		{
			name:    "Invalid JSON",
			line:    `2024-01-15T10:30:00Z {not json}`,
			wantSev: storage.SeverityUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, sev, _ := parser.Parse(tt.line)

			if sev != tt.wantSev {
				t.Errorf("severity = %v, want %v", sev, tt.wantSev)
			}
		})
	}
}

func TestParser_CommonFormats(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name    string
		line    string
		wantSev storage.Severity
	}{
		{
			name:    "level=INFO",
			line:    "2024-01-15T10:30:00Z level=INFO msg=test",
			wantSev: storage.SeverityInfo,
		},
		{
			name:    "level=ERROR",
			line:    "2024-01-15T10:30:00Z level=ERROR msg=test",
			wantSev: storage.SeverityError,
		},
		{
			name:    "INFO: prefix",
			line:    "2024-01-15T10:30:00Z INFO: Application started",
			wantSev: storage.SeverityInfo,
		},
		{
			name:    "ERROR: prefix",
			line:    "2024-01-15T10:30:00Z ERROR: Something failed",
			wantSev: storage.SeverityError,
		},
		{
			name:    "WARNING mapped to WARN",
			line:    "2024-01-15T10:30:00Z [WARNING] Be careful",
			wantSev: storage.SeverityWarn,
		},
		{
			name:    "PANIC mapped to FATAL",
			line:    "2024-01-15T10:30:00Z level=PANIC msg=crash",
			wantSev: storage.SeverityFatal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, sev, _ := parser.Parse(tt.line)

			if sev != tt.wantSev {
				t.Errorf("severity = %v, want %v", sev, tt.wantSev)
			}
		})
	}
}
