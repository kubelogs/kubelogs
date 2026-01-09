package collector

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// Parser extracts timestamps and severity from log lines.
type Parser struct {
	// Compiled patterns for severity detection
	severityPatterns []*severityPattern
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

// Parse extracts timestamp and severity from a log line.
// Kubernetes log lines have the format: "2024-01-15T10:30:00.123456789Z message"
// Returns defaults (current time, SeverityUnknown) if parsing fails.
func (p *Parser) Parse(line string) (time.Time, storage.Severity, string) {
	timestamp, message := p.parseTimestamp(line)
	severity := p.parseSeverity(message)
	return timestamp, severity, message
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

// parseSeverity attempts to detect log severity from the message.
func (p *Parser) parseSeverity(message string) storage.Severity {
	// Try JSON parsing first for structured logs
	if severity := p.parseSeverityJSON(message); severity != storage.SeverityUnknown {
		return severity
	}

	// Try regex patterns (case-insensitive)
	for _, pattern := range p.severityPatterns {
		if matches := pattern.regex.FindStringSubmatch(message); len(matches) > 1 {
			return storage.ParseSeverity(strings.ToUpper(matches[1]))
		}
	}

	return storage.SeverityUnknown
}

// parseSeverityJSON tries to parse a JSON log and extract severity.
func (p *Parser) parseSeverityJSON(message string) storage.Severity {
	// Quick check - must start with { and contain level/severity
	if len(message) == 0 || message[0] != '{' {
		return storage.SeverityUnknown
	}

	// Try to parse just enough to get the level field
	var logEntry struct {
		Level    string `json:"level"`
		Severity string `json:"severity"`
		Lvl      string `json:"lvl"`
	}

	if err := json.Unmarshal([]byte(message), &logEntry); err != nil {
		return storage.SeverityUnknown
	}

	// Check various common field names
	if logEntry.Level != "" {
		return storage.ParseSeverity(logEntry.Level)
	}
	if logEntry.Severity != "" {
		return storage.ParseSeverity(logEntry.Severity)
	}
	if logEntry.Lvl != "" {
		return storage.ParseSeverity(logEntry.Lvl)
	}

	return storage.SeverityUnknown
}
