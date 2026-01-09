package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"

	_ "modernc.org/sqlite"
)

const (
	defaultWriteBuffer = 1000
	defaultQueryLimit  = 100
)

// Store implements storage.Store using SQLite with FTS5.
type Store struct {
	db     *sql.DB
	path   string
	closed bool

	mu     sync.Mutex // Protects buffer and closed flag
	buffer storage.LogBatch
	bufCap int

	writeMu sync.Mutex // Serializes SQL write transactions
}

// Config holds SQLite store configuration.
type Config struct {
	// Path to the SQLite database file.
	// Use ":memory:" for in-memory database.
	Path string

	// WriteBufferSize is the number of entries to buffer before flushing.
	WriteBufferSize int
}

// New creates a new SQLite store.
func New(cfg Config) (*Store, error) {
	if cfg.WriteBufferSize <= 0 {
		cfg.WriteBufferSize = defaultWriteBuffer
	}

	db, err := sql.Open("sqlite", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if _, err := db.Exec(pragmaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Store{
		db:     db,
		path:   cfg.Path,
		buffer: make(storage.LogBatch, 0, cfg.WriteBufferSize),
		bufCap: cfg.WriteBufferSize,
	}, nil
}

// Write implements storage.Store.
func (s *Store) Write(ctx context.Context, entries storage.LogBatch) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, storage.ErrStorageClosed
	}
	s.buffer = append(s.buffer, entries...)
	needFlush := len(s.buffer) >= s.bufCap
	s.mu.Unlock()

	if needFlush {
		if err := s.Flush(ctx); err != nil {
			return 0, err
		}
	}
	return len(entries), nil
}

// Flush implements storage.WriteOptimizer.
func (s *Store) Flush(ctx context.Context) error {
	// Step 1: Atomically swap the buffer (fast, under mu)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return storage.ErrStorageClosed
	}
	if len(s.buffer) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := s.buffer
	s.buffer = make(storage.LogBatch, 0, s.bufCap)
	s.mu.Unlock()

	// Step 2: Serialize SQL writes (may block other flushes, but not buffer appends)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Check context before starting potentially slow operation
	if err := ctx.Err(); err != nil {
		// Re-queue batch on cancellation to avoid data loss
		s.mu.Lock()
		s.buffer = append(batch, s.buffer...)
		s.mu.Unlock()
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		// Re-queue batch on failure
		s.mu.Lock()
		s.buffer = append(batch, s.buffer...)
		s.mu.Unlock()
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO logs (timestamp, namespace, pod, container, severity, message, attributes)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		s.mu.Lock()
		s.buffer = append(batch, s.buffer...)
		s.mu.Unlock()
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, e := range batch {
		var attrs *string
		if len(e.Attributes) > 0 {
			b, _ := json.Marshal(e.Attributes)
			str := string(b)
			attrs = &str
		}

		_, err := stmt.ExecContext(ctx,
			e.Timestamp.UnixNano(),
			e.Namespace,
			e.Pod,
			e.Container,
			e.Severity,
			e.Message,
			attrs,
		)
		if err != nil {
			s.mu.Lock()
			s.buffer = append(batch, s.buffer...)
			s.mu.Unlock()
			return fmt.Errorf("insert: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		s.mu.Lock()
		s.buffer = append(batch, s.buffer...)
		s.mu.Unlock()
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// SetWriteBuffer implements storage.WriteOptimizer.
func (s *Store) SetWriteBuffer(entries int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entries > 0 {
		s.bufCap = entries
	}
}

// Query implements storage.Store.
func (s *Store) Query(ctx context.Context, q storage.Query) (*storage.QueryResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, storage.ErrStorageClosed
	}
	s.mu.Unlock()

	// Flush before querying to ensure recent writes are visible
	if err := s.Flush(ctx); err != nil {
		return nil, err
	}

	query, args := buildQuery(q)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	limit := q.Pagination.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}

	entries := make([]storage.LogEntry, 0, limit)
	for rows.Next() {
		var e storage.LogEntry
		var ts int64
		var attrs sql.NullString

		err := rows.Scan(&e.ID, &ts, &e.Namespace, &e.Pod, &e.Container, &e.Severity, &e.Message, &attrs)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}

		e.Timestamp = time.Unix(0, ts)
		if attrs.Valid && attrs.String != "" {
			json.Unmarshal([]byte(attrs.String), &e.Attributes)
		}

		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	result := &storage.QueryResult{
		TotalEstimate: -1,
	}

	// Check if we fetched more than limit (hasMore indicator)
	if len(entries) > limit {
		result.HasMore = true
		result.NextCursor = entries[limit].ID
		entries = entries[:limit]
	}
	result.Entries = entries

	return result, nil
}

// GetByID implements storage.Store.
func (s *Store) GetByID(ctx context.Context, id int64) (*storage.LogEntry, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, storage.ErrStorageClosed
	}
	s.mu.Unlock()

	var e storage.LogEntry
	var ts int64
	var attrs sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, timestamp, namespace, pod, container, severity, message, attributes
		FROM logs WHERE id = ?
	`, id).Scan(&e.ID, &ts, &e.Namespace, &e.Pod, &e.Container, &e.Severity, &e.Message, &attrs)

	if err == sql.ErrNoRows {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	e.Timestamp = time.Unix(0, ts)
	if attrs.Valid && attrs.String != "" {
		json.Unmarshal([]byte(attrs.String), &e.Attributes)
	}

	return &e, nil
}

// Delete implements storage.Store.
func (s *Store) Delete(ctx context.Context, olderThan time.Time) (int64, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, storage.ErrStorageClosed
	}
	s.mu.Unlock()

	// Serialize with other writes to prevent SQLITE_BUSY
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	result, err := s.db.ExecContext(ctx, `DELETE FROM logs WHERE timestamp < ?`, olderThan.UnixNano())
	if err != nil {
		return 0, fmt.Errorf("delete: %w", err)
	}

	return result.RowsAffected()
}

// Stats implements storage.Store.
func (s *Store) Stats(ctx context.Context) (*storage.Stats, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, storage.ErrStorageClosed
	}
	s.mu.Unlock()

	stats := &storage.Stats{}

	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM logs`).Scan(&stats.TotalEntries)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	var oldest, newest sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT MIN(timestamp), MAX(timestamp) FROM logs`).Scan(&oldest, &newest)
	if err != nil {
		return nil, fmt.Errorf("min/max: %w", err)
	}

	if oldest.Valid {
		stats.OldestEntry = time.Unix(0, oldest.Int64)
	}
	if newest.Valid {
		stats.NewestEntry = time.Unix(0, newest.Int64)
	}

	// Get database file size if not in-memory
	if s.path != ":memory:" {
		var pageCount, pageSize int64
		s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount)
		s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize)
		stats.DiskSizeBytes = pageCount * pageSize
	}

	return stats, nil
}

// Close implements storage.Store.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	batch := s.buffer
	s.buffer = nil
	s.mu.Unlock()

	// Wait for any in-flight writes to complete
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Flush remaining buffer
	if len(batch) > 0 {
		tx, err := s.db.Begin()
		if err == nil {
			stmt, _ := tx.Prepare(`
				INSERT INTO logs (timestamp, namespace, pod, container, severity, message, attributes)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`)
			if stmt != nil {
				for _, e := range batch {
					var attrs *string
					if len(e.Attributes) > 0 {
						b, _ := json.Marshal(e.Attributes)
						str := string(b)
						attrs = &str
					}
					stmt.Exec(e.Timestamp.UnixNano(), e.Namespace, e.Pod, e.Container, e.Severity, e.Message, attrs)
				}
				stmt.Close()
			}
			tx.Commit()
		}
	}

	return s.db.Close()
}

// buildQuery constructs a parameterized SQL query from Query.
func buildQuery(q storage.Query) (string, []any) {
	var sql strings.Builder
	var args []any

	sql.WriteString("SELECT l.id, l.timestamp, l.namespace, l.pod, l.container, l.severity, l.message, l.attributes FROM logs l")

	if q.Search != "" {
		sql.WriteString(" JOIN logs_fts f ON l.id = f.rowid")
	}

	sql.WriteString(" WHERE 1=1")

	if !q.StartTime.IsZero() {
		sql.WriteString(" AND l.timestamp >= ?")
		args = append(args, q.StartTime.UnixNano())
	}
	if !q.EndTime.IsZero() {
		sql.WriteString(" AND l.timestamp < ?")
		args = append(args, q.EndTime.UnixNano())
	}

	if q.Search != "" {
		sql.WriteString(" AND logs_fts MATCH ?")
		args = append(args, q.Search)
	}

	if q.Namespace != "" {
		sql.WriteString(" AND l.namespace = ?")
		args = append(args, q.Namespace)
	}
	if q.Pod != "" {
		sql.WriteString(" AND l.pod = ?")
		args = append(args, q.Pod)
	}
	if q.Container != "" {
		sql.WriteString(" AND l.container = ?")
		args = append(args, q.Container)
	}

	if q.MinSeverity > storage.SeverityUnknown {
		sql.WriteString(" AND l.severity >= ?")
		args = append(args, q.MinSeverity)
	}

	for k, v := range q.Attributes {
		sql.WriteString(" AND json_extract(l.attributes, ?) = ?")
		args = append(args, "$."+k, v)
	}

	if q.Pagination.AfterID > 0 {
		if q.Pagination.Order == storage.OrderAsc {
			sql.WriteString(" AND l.id > ?")
		} else {
			sql.WriteString(" AND l.id < ?")
		}
		args = append(args, q.Pagination.AfterID)
	}
	if q.Pagination.BeforeID > 0 {
		if q.Pagination.Order == storage.OrderAsc {
			sql.WriteString(" AND l.id < ?")
		} else {
			sql.WriteString(" AND l.id > ?")
		}
		args = append(args, q.Pagination.BeforeID)
	}

	if q.Pagination.Order == storage.OrderAsc {
		sql.WriteString(" ORDER BY l.id ASC")
	} else {
		sql.WriteString(" ORDER BY l.id DESC")
	}

	limit := q.Pagination.Limit
	if limit <= 0 {
		limit = defaultQueryLimit
	}
	sql.WriteString(fmt.Sprintf(" LIMIT %d", limit+1))

	return sql.String(), args
}

// ListNamespaces returns distinct namespace values.
func (s *Store) ListNamespaces(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, storage.ErrStorageClosed
	}
	s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT namespace FROM logs ORDER BY namespace`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	namespaces := make([]string, 0)
	for rows.Next() {
		var ns string
		if err := rows.Scan(&ns); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		namespaces = append(namespaces, ns)
	}

	return namespaces, rows.Err()
}

// ListContainers returns distinct container values.
func (s *Store) ListContainers(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, storage.ErrStorageClosed
	}
	s.mu.Unlock()

	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT container FROM logs ORDER BY container`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	containers := make([]string, 0)
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		containers = append(containers, c)
	}

	return containers, rows.Err()
}
