package collector

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// ParseResult contains the parsed components of a log line.
type ParseResult struct {
	Timestamp  time.Time
	Severity   storage.Severity
	Message    string
	Attributes map[string]string // Extracted structured fields (nil if none)
}

// Parser extracts timestamps and severity from log lines.
type Parser struct {
	// Compiled patterns for severity detection
	severityPatterns []*severityPattern
}

// Well-known JSON field names to extract (with aliases)
var jsonFieldAliases = map[string][]string{
	"msg":        {"msg", "message", "error", "err"},
	"trace_id":   {"trace_id", "traceId", "trace-id", "traceID"},
	"span_id":    {"span_id", "spanId", "span-id", "spanID"},
	"request_id": {"request_id", "requestId", "request-id", "requestID", "req_id"},
	"caller":     {"caller", "source", "file", "location"},
	"service":    {"service", "app", "application"},
	"user_id":    {"user_id", "userId", "user"},
}

type severityPattern struct {
	regex    *regexp.Regexp
	severity storage.Severity
}

// NewParser creates a log parser with common format patterns.
func NewParser() *Parser {
	return &Parser{
		severityPatterns: []*severityPattern{
			// JSON level field (case-insensitive)
			{regexp.MustCompile(`(?i)"level"\s*:\s*"(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)"`), 0},
			// Bracket format: [INFO], [ERROR], etc. (case-insensitive)
			{regexp.MustCompile(`(?i)\[(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)\]`), 0},
			// Space-separated format: level=INFO, level=ERROR (case-insensitive)
			{regexp.MustCompile(`(?i)level=(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC)\b`), 0},
			// Common formats: INFO:, ERROR:, etc. (case-insensitive)
			{regexp.MustCompile(`(?i)\b(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|PANIC):`), 0},
		},
	}
}

// Parse extracts timestamp, severity, and structured fields from a log line.
// Kubernetes log lines have the format: "2024-01-15T10:30:00.123456789Z message"
// Returns defaults (current time, SeverityUnknown) if parsing fails.
// For JSON logs, also extracts well-known fields into Attributes.
func (p *Parser) Parse(line string) ParseResult {
	timestamp, message := p.parseTimestamp(line)
	severity, attrs := p.parseStructured(message)
	return ParseResult{
		Timestamp:  timestamp,
		Severity:   severity,
		Message:    message,
		Attributes: attrs,
	}
}

// parseTimestamp extracts the Kubernetes timestamp prefix.
// Format: "2024-01-15T10:30:00.123456789Z <message>"
func (p *Parser) parseTimestamp(line string) (time.Time, string) {
	// Kubernetes log lines start with RFC3339Nano timestamp followed by space
	// Minimum format: "2024-01-15T10:30:00Z " = 21 chars
	if len(line) < 21 {
		return time.Now(), line
	}

	// Find first space after timestamp
	spaceIdx := strings.Index(line, " ")
	if spaceIdx < 20 { // Too short to be a valid timestamp
		return time.Now(), line
	}

	timestampStr := line[:spaceIdx]
	message := line[spaceIdx+1:]

	// Try parsing as RFC3339Nano (Kubernetes default)
	t, err := time.Parse(time.RFC3339Nano, timestampStr)
	if err != nil {
		// Try RFC3339 (without nanoseconds)
		t, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			return time.Now(), line
		}
	}

	return t, message
}

// parseStructured attempts to detect log severity and extract structured fields.
// Returns severity and attributes map (nil if no structured data found).
func (p *Parser) parseStructured(message string) (storage.Severity, map[string]string) {
	// Try JSON parsing first for structured logs
	if severity, attrs := p.parseJSON(message); severity != storage.SeverityUnknown || attrs != nil {
		return severity, attrs
	}

	// Try regex patterns for unstructured logs (case-insensitive)
	for _, pattern := range p.severityPatterns {
		if matches := pattern.regex.FindStringSubmatch(message); len(matches) > 1 {
			return storage.ParseSeverity(strings.ToUpper(matches[1])), nil
		}
	}

	return storage.SeverityUnknown, nil
}

// parseJSON parses a JSON log line and extracts severity and well-known fields.
func (p *Parser) parseJSON(message string) (storage.Severity, map[string]string) {
	// Quick check - must start with {
	if len(message) == 0 || message[0] != '{' {
		return storage.SeverityUnknown, nil
	}

	// Parse into generic map to extract all fields
	var data map[string]any
	if err := json.Unmarshal([]byte(message), &data); err != nil {
		return storage.SeverityUnknown, nil
	}

	// Extract severity from common field names
	severity := storage.SeverityUnknown
	for _, key := range []string{"level", "severity", "lvl"} {
		if val, ok := data[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				severity = storage.ParseSeverity(str)
				if severity != storage.SeverityUnknown {
					break
				}
			}
		}
	}

	// Extract well-known fields into attributes
	attrs := extractJSONFields(data)

	return severity, attrs
}

// extractJSONFields extracts well-known fields from a parsed JSON log.
// Only extracts string values to keep things simple and memory-efficient.
func extractJSONFields(data map[string]any) map[string]string {
	attrs := make(map[string]string)

	for canonicalName, aliases := range jsonFieldAliases {
		for _, alias := range aliases {
			if val, ok := data[alias]; ok {
				str := stringifyValue(val)
				if str != "" {
					attrs[canonicalName] = str
					break // Use first match
				}
			}
		}
	}

	// Return nil if no fields extracted (saves memory)
	if len(attrs) == 0 {
		return nil
	}

	return attrs
}

// stringifyValue converts a JSON value to string.
// Only handles scalar types to avoid memory-heavy nested structures.
func stringifyValue(val any) string {
	switch v := val.(type) {
	case string:
		return v
	case float64:
		// JSON numbers are float64
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%g", v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		// Skip arrays, objects, null
		return ""
	}
}
