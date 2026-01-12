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
			result := parser.Parse(tt.line)

			if result.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", result.Message, tt.wantMessage)
			}

			if result.Severity != tt.wantSev {
				t.Errorf("severity = %v, want %v", result.Severity, tt.wantSev)
			}

			// For lines with timestamps, verify it's not the current time
			if len(tt.line) > 20 && tt.line[4] == '-' {
				if time.Since(result.Timestamp) < time.Second {
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
			result := parser.Parse(tt.line)

			if result.Severity != tt.wantSev {
				t.Errorf("severity = %v, want %v", result.Severity, tt.wantSev)
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
			result := parser.Parse(tt.line)

			if result.Severity != tt.wantSev {
				t.Errorf("severity = %v, want %v", result.Severity, tt.wantSev)
			}
		})
	}
}

func TestParser_JSONFieldExtraction(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		line        string
		wantMessage string // Expected Message field (extracted msg or full line)
		wantAttrs   map[string]string
	}{
		{
			name:        "Extract msg field - msg becomes Message",
			line:        `2024-01-15T10:30:00Z {"level":"INFO","msg":"hello world"}`,
			wantMessage: "hello world",
			wantAttrs:   map[string]string{"level": "INFO"},
		},
		{
			name:        "Extract message as msg - message alias becomes Message",
			line:        `2024-01-15T10:30:00Z {"level":"INFO","message":"hello world"}`,
			wantMessage: "hello world",
			wantAttrs:   map[string]string{"level": "INFO"},
		},
		{
			name:        "Extract trace_id variants",
			line:        `2024-01-15T10:30:00Z {"level":"INFO","traceId":"abc123","msg":"test"}`,
			wantMessage: "test",
			wantAttrs: map[string]string{
				"trace_id": "abc123",
				"level":    "INFO",
			},
		},
		{
			name:        "Extract request_id",
			line:        `2024-01-15T10:30:00Z {"level":"INFO","requestId":"req-456","msg":"test"}`,
			wantMessage: "test",
			wantAttrs: map[string]string{
				"request_id": "req-456",
				"level":      "INFO",
			},
		},
		{
			name:        "Extract multiple fields",
			line:        `2024-01-15T10:30:00Z {"level":"ERROR","msg":"failed","trace_id":"t1","span_id":"s1","service":"api"}`,
			wantMessage: "failed",
			wantAttrs: map[string]string{
				"trace_id": "t1",
				"span_id":  "s1",
				"service":  "api",
				"level":    "ERROR",
			},
		},
		{
			name:        "Numeric values converted to string",
			line:        `2024-01-15T10:30:00Z {"level":"INFO","user_id":12345,"msg":"test"}`,
			wantMessage: "test",
			wantAttrs: map[string]string{
				"user_id": "12345",
				"level":   "INFO",
			},
		},
		{
			name:        "Non-JSON returns nil attrs",
			line:        `2024-01-15T10:30:00Z [INFO] plain text log`,
			wantMessage: "[INFO] plain text log",
			wantAttrs:   nil,
		},
		{
			name:        "JSON with custom fields extracts all",
			line:        `2024-01-15T10:30:00Z {"level":"INFO","custom":"value","another":123}`,
			wantMessage: `{"level":"INFO","custom":"value","another":123}`,
			wantAttrs: map[string]string{
				"level":   "INFO",
				"custom":  "value",
				"another": "123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.line)

			if result.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", result.Message, tt.wantMessage)
			}

			if tt.wantAttrs == nil {
				if result.Attributes != nil {
					t.Errorf("attributes = %v, want nil", result.Attributes)
				}
				return
			}

			if result.Attributes == nil {
				t.Errorf("attributes = nil, want %v", tt.wantAttrs)
				return
			}

			for key, wantVal := range tt.wantAttrs {
				if gotVal, ok := result.Attributes[key]; !ok {
					t.Errorf("missing attribute %q", key)
				} else if gotVal != wantVal {
					t.Errorf("attribute[%q] = %q, want %q", key, gotVal, wantVal)
				}
			}

			// Ensure msg is not in attributes (moved to Message)
			if _, ok := result.Attributes["msg"]; ok {
				t.Errorf("msg should not be in attributes, should be in Message")
			}
		})
	}
}

func TestParser_LogfmtSeverity(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name    string
		line    string
		wantSev storage.Severity
	}{
		{
			name:    "level=info",
			line:    `2024-01-15T10:30:00Z level=info msg=test`,
			wantSev: storage.SeverityInfo,
		},
		{
			name:    "level=INFO uppercase",
			line:    `2024-01-15T10:30:00Z level=INFO msg=test`,
			wantSev: storage.SeverityInfo,
		},
		{
			name:    "level=error",
			line:    `2024-01-15T10:30:00Z level=error msg="something failed"`,
			wantSev: storage.SeverityError,
		},
		{
			name:    "level=warn",
			line:    `2024-01-15T10:30:00Z level=warn msg=warning`,
			wantSev: storage.SeverityWarn,
		},
		{
			name:    "level=debug",
			line:    `2024-01-15T10:30:00Z level=debug msg=debugging`,
			wantSev: storage.SeverityDebug,
		},
		{
			name:    "severity field",
			line:    `2024-01-15T10:30:00Z severity=ERROR msg=test`,
			wantSev: storage.SeverityError,
		},
		{
			name:    "lvl field",
			line:    `2024-01-15T10:30:00Z lvl=FATAL msg=panic`,
			wantSev: storage.SeverityFatal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.line)

			if result.Severity != tt.wantSev {
				t.Errorf("severity = %v, want %v", result.Severity, tt.wantSev)
			}
		})
	}
}

func TestParser_LogfmtFieldExtraction(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name        string
		line        string
		wantMessage string // Expected Message field (extracted msg or full line)
		wantAttrs   map[string]string
	}{
		{
			name:        "Extract msg field - msg becomes Message",
			line:        `2024-01-15T10:30:00Z level=info msg="hello world"`,
			wantMessage: "hello world",
			wantAttrs:   map[string]string{"level": "info"},
		},
		{
			name:        "Extract unquoted msg",
			line:        `2024-01-15T10:30:00Z level=info msg=hello`,
			wantMessage: "hello",
			wantAttrs:   map[string]string{"level": "info"},
		},
		{
			name:        "Extract trace_id",
			line:        `2024-01-15T10:30:00Z level=info trace_id=abc123 msg=test`,
			wantMessage: "test",
			wantAttrs: map[string]string{
				"trace_id": "abc123",
				"level":    "info",
			},
		},
		{
			name:        "Extract request_id",
			line:        `2024-01-15T10:30:00Z level=info request_id=req-456 msg=test`,
			wantMessage: "test",
			wantAttrs: map[string]string{
				"request_id": "req-456",
				"level":      "info",
			},
		},
		{
			name:        "Extract multiple fields",
			line:        `2024-01-15T10:30:00Z level=error msg=failed trace_id=t1 span_id=s1 service=api`,
			wantMessage: "failed",
			wantAttrs: map[string]string{
				"trace_id": "t1",
				"span_id":  "s1",
				"service":  "api",
				"level":    "error",
			},
		},
		{
			name:        "Quoted value with spaces",
			line:        `2024-01-15T10:30:00Z level=info msg="hello world with spaces" service=test`,
			wantMessage: "hello world with spaces",
			wantAttrs: map[string]string{
				"service": "test",
				"level":   "info",
			},
		},
		{
			name:        "Escaped quotes in value",
			line:        `2024-01-15T10:30:00Z level=info msg="say \"hello\""`,
			wantMessage: `say "hello"`,
			wantAttrs:   map[string]string{"level": "info"},
		},
		{
			name:        "Error field as msg alias - err becomes Message",
			line:        `2024-01-15T10:30:00Z level=error err="connection timeout"`,
			wantMessage: "connection timeout",
			wantAttrs:   map[string]string{"level": "error"},
		},
		{
			name:        "Non-logfmt returns nil attrs",
			line:        `2024-01-15T10:30:00Z [INFO] plain text log`,
			wantMessage: "[INFO] plain text log",
			wantAttrs:   nil,
		},
		{
			name:        "Logfmt with custom fields extracts all",
			line:        `2024-01-15T10:30:00Z level=info custom=value another=123`,
			wantMessage: `level=info custom=value another=123`,
			wantAttrs: map[string]string{
				"level":   "info",
				"custom":  "value",
				"another": "123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parser.Parse(tt.line)

			if result.Message != tt.wantMessage {
				t.Errorf("message = %q, want %q", result.Message, tt.wantMessage)
			}

			if tt.wantAttrs == nil {
				if result.Attributes != nil {
					t.Errorf("attributes = %v, want nil", result.Attributes)
				}
				return
			}

			if result.Attributes == nil {
				t.Errorf("attributes = nil, want %v", tt.wantAttrs)
				return
			}

			for key, wantVal := range tt.wantAttrs {
				if gotVal, ok := result.Attributes[key]; !ok {
					t.Errorf("missing attribute %q", key)
				} else if gotVal != wantVal {
					t.Errorf("attribute[%q] = %q, want %q", key, gotVal, wantVal)
				}
			}

			// Ensure msg is not in attributes (moved to Message)
			if _, ok := result.Attributes["msg"]; ok {
				t.Errorf("msg should not be in attributes, should be in Message")
			}
		})
	}
}

func TestParseLogfmtFields(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect map[string]string
	}{
		{
			name:  "simple key=value",
			input: "key=value",
			expect: map[string]string{
				"key": "value",
			},
		},
		{
			name:  "multiple pairs",
			input: "a=1 b=2 c=3",
			expect: map[string]string{
				"a": "1",
				"b": "2",
				"c": "3",
			},
		},
		{
			name:  "quoted value",
			input: `msg="hello world"`,
			expect: map[string]string{
				"msg": "hello world",
			},
		},
		{
			name:  "mixed quoted and unquoted",
			input: `level=info msg="hello world" count=42`,
			expect: map[string]string{
				"level": "info",
				"msg":   "hello world",
				"count": "42",
			},
		},
		{
			name:  "escaped quotes",
			input: `msg="say \"hello\""`,
			expect: map[string]string{
				"msg": `say "hello"`,
			},
		},
		{
			name:  "escaped backslash",
			input: `path="C:\\Users\\test"`,
			expect: map[string]string{
				"path": `C:\Users\test`,
			},
		},
		{
			name:  "empty value",
			input: "key=",
			expect: map[string]string{
				"key": "",
			},
		},
		{
			name:  "key with underscore",
			input: "trace_id=abc request_id=123",
			expect: map[string]string{
				"trace_id":   "abc",
				"request_id": "123",
			},
		},
		{
			name:  "key with hyphen",
			input: "x-request-id=abc",
			expect: map[string]string{
				"x-request-id": "abc",
			},
		},
		{
			name:  "extra whitespace",
			input: "  a=1   b=2  ",
			expect: map[string]string{
				"a": "1",
				"b": "2",
			},
		},
		{
			name:   "no equals sign",
			input:  "just some text",
			expect: map[string]string{},
		},
		{
			name:  "newline escape sequence",
			input: `msg="line1\nline2"`,
			expect: map[string]string{
				"msg": "line1\nline2",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseLogfmtFields(tt.input)

			if len(result) != len(tt.expect) {
				t.Errorf("field count = %d, want %d", len(result), len(tt.expect))
			}

			for key, wantVal := range tt.expect {
				if gotVal, ok := result[key]; !ok {
					t.Errorf("missing field %q", key)
				} else if gotVal != wantVal {
					t.Errorf("field[%q] = %q, want %q", key, gotVal, wantVal)
				}
			}
		})
	}
}

func TestParser_MaxAttributesLimit(t *testing.T) {
	parser := NewParser()

	// Build a JSON log with more than maxAttributes (20) fields
	jsonWithManyFields := `2024-01-15T10:30:00Z {"level":"INFO","msg":"test","f1":"v1","f2":"v2","f3":"v3","f4":"v4","f5":"v5","f6":"v6","f7":"v7","f8":"v8","f9":"v9","f10":"v10","f11":"v11","f12":"v12","f13":"v13","f14":"v14","f15":"v15","f16":"v16","f17":"v17","f18":"v18","f19":"v19","f20":"v20","f21":"v21","f22":"v22"}`

	result := parser.Parse(jsonWithManyFields)

	// Message should be extracted from msg field
	if result.Message != "test" {
		t.Errorf("message = %q, want %q", result.Message, "test")
	}

	// Attributes should be capped at maxAttributes (20)
	// Note: msg was moved to Message, so it doesn't count
	if len(result.Attributes) > maxAttributes {
		t.Errorf("attributes count = %d, want <= %d", len(result.Attributes), maxAttributes)
	}

	// Should have extracted the level field at minimum
	if _, ok := result.Attributes["level"]; !ok {
		t.Errorf("expected level attribute to be extracted")
	}
}

func TestParser_ExtractsAllScalarFields(t *testing.T) {
	parser := NewParser()

	// JSON with various field types
	line := `2024-01-15T10:30:00Z {"level":"INFO","msg":"test","count":42,"enabled":true,"ratio":3.14,"nested":{"skip":"this"},"array":["skip","this"]}`

	result := parser.Parse(line)

	if result.Message != "test" {
		t.Errorf("message = %q, want %q", result.Message, "test")
	}

	// Should extract scalar fields
	expected := map[string]string{
		"level":   "INFO",
		"count":   "42",
		"enabled": "true",
		"ratio":   "3.14",
	}

	for key, wantVal := range expected {
		if gotVal, ok := result.Attributes[key]; !ok {
			t.Errorf("missing attribute %q", key)
		} else if gotVal != wantVal {
			t.Errorf("attribute[%q] = %q, want %q", key, gotVal, wantVal)
		}
	}

	// Should NOT extract nested objects or arrays
	if _, ok := result.Attributes["nested"]; ok {
		t.Errorf("should not extract nested objects")
	}
	if _, ok := result.Attributes["array"]; ok {
		t.Errorf("should not extract arrays")
	}
}
