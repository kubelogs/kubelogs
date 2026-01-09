package storage

import "time"

// Severity represents log severity levels.
type Severity uint8

const (
	SeverityUnknown Severity = iota
	SeverityTrace
	SeverityDebug
	SeverityInfo
	SeverityWarn
	SeverityError
	SeverityFatal
)

// String returns the human-readable severity name.
func (s Severity) String() string {
	switch s {
	case SeverityTrace:
		return "TRACE"
	case SeverityDebug:
		return "DEBUG"
	case SeverityInfo:
		return "INFO"
	case SeverityWarn:
		return "WARN"
	case SeverityError:
		return "ERROR"
	case SeverityFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// ParseSeverity converts a string to Severity.
func ParseSeverity(s string) Severity {
	switch s {
	case "TRACE", "trace":
		return SeverityTrace
	case "DEBUG", "debug":
		return SeverityDebug
	case "INFO", "info":
		return SeverityInfo
	case "WARN", "warn", "WARNING", "warning":
		return SeverityWarn
	case "ERROR", "error":
		return SeverityError
	case "FATAL", "fatal", "PANIC", "panic":
		return SeverityFatal
	default:
		return SeverityUnknown
	}
}

// LogEntry represents a single log record from a Kubernetes container.
type LogEntry struct {
	// ID is a unique identifier assigned by storage.
	// Zero means the entry hasn't been persisted yet.
	ID int64

	// Timestamp when the log was produced.
	Timestamp time.Time

	// Kubernetes context fields - indexed for fast filtering.
	Namespace string
	Pod       string
	Container string

	// Severity level of the log entry.
	Severity Severity

	// Message is the log body.
	Message string

	// Attributes holds arbitrary structured fields.
	// nil means no attributes.
	Attributes map[string]string
}

// LogBatch is a slice of entries for bulk operations.
type LogBatch []LogEntry

// Query defines parameters for searching logs.
// Zero values mean "no filter" for that field.
type Query struct {
	// Time range (StartTime inclusive, EndTime exclusive).
	StartTime time.Time
	EndTime   time.Time

	// Full-text search on message body.
	Search string

	// Kubernetes field filters (exact match).
	Namespace string
	Pod       string
	Container string

	// Severity filter - returns entries >= this level.
	MinSeverity Severity

	// Attribute filters (exact match, AND logic).
	Attributes map[string]string

	// Pagination controls.
	Pagination Pagination
}

// Pagination defines how to page through results.
type Pagination struct {
	// Limit is the maximum number of entries to return.
	// Zero means use default.
	Limit int

	// AfterID returns entries with ID after this value (for forward pagination).
	AfterID int64

	// BeforeID returns entries with ID before this value (for reverse pagination).
	BeforeID int64

	// Order specifies result ordering.
	Order Order
}

// Order defines sort order for query results.
type Order uint8

const (
	// OrderDesc returns newest entries first (default for log viewing).
	OrderDesc Order = iota
	// OrderAsc returns oldest entries first.
	OrderAsc
)

// QueryResult contains the results of a log query.
type QueryResult struct {
	// Entries contains the matching log entries.
	Entries []LogEntry

	// HasMore indicates if more results exist beyond this page.
	HasMore bool

	// NextCursor is the ID to use for fetching the next page.
	NextCursor int64

	// TotalEstimate is an approximate count of total matches.
	// -1 means count is not available.
	TotalEstimate int64
}
