package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"

	_ "github.com/mattn/go-sqlite3"
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

	// Clean up stale WAL mode files before opening. These can cause
	// SQLITE_IOERR_SHMSIZE errors if left over from a previous crash
	// when the database was in WAL mode.
	if cfg.Path != ":memory:" {
		os.Remove(cfg.Path + "-shm")
		os.Remove(cfg.Path + "-wal")
	}

	db, err := sql.Open("sqlite3", cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// SQLite works best with a single connection for write serialization.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(pragmaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}

	// Verify journal mode was set correctly. PRAGMA journal_mode doesn't
	// error on failure - it returns the actual mode instead.
	// In-memory databases always use "memory" journal mode.
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		db.Close()
		return nil, fmt.Errorf("check journal_mode: %w", err)
	}
	if cfg.Path != ":memory:" && journalMode != "delete" {
		db.Close()
		return nil, fmt.Errorf("failed to set journal_mode=DELETE, got %q", journalMode)
	}

	// Create base schema (tables and indexes that don't depend on migrated columns)
	if _, err := db.Exec(baseSchemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create base schema: %w", err)
	}

	// Run migrations for existing databases (e.g., add dedup_hash column)
	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	// Create post-migration schema (indexes that depend on migrated columns)
	if _, err := db.Exec(postMigrationSchemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create post-migration schema: %w", err)
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
		INSERT OR IGNORE INTO logs (timestamp, namespace, pod, container, severity, message, attributes, dedup_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
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

		hash := computeDedupHash(
			e.Timestamp.UnixNano(),
			e.Namespace,
			e.Pod,
			e.Container,
			e.Message,
		)

		_, err := stmt.ExecContext(ctx,
			e.Timestamp.UnixNano(),
			e.Namespace,
			e.Pod,
			e.Container,
			e.Severity,
			e.Message,
			attrs,
			hash,
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
				INSERT OR IGNORE INTO logs (timestamp, namespace, pod, container, severity, message, attributes, dedup_hash)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`)
			if stmt != nil {
				for _, e := range batch {
					var attrs *string
					if len(e.Attributes) > 0 {
						b, _ := json.Marshal(e.Attributes)
						str := string(b)
						attrs = &str
					}
					hash := computeDedupHash(
						e.Timestamp.UnixNano(),
						e.Namespace,
						e.Pod,
						e.Container,
						e.Message,
					)
					stmt.Exec(e.Timestamp.UnixNano(), e.Namespace, e.Pod, e.Container, e.Severity, e.Message, attrs, hash)
				}
				stmt.Close()
			}
			tx.Commit()
		}
	}

	return s.db.Close()
}

// DB returns the underlying database connection.
// This is used by the auth package to share the same connection.
func (s *Store) DB() *sql.DB {
	return s.db
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

	// Sort attribute keys for deterministic query building
	attrKeys := make([]string, 0, len(q.Attributes))
	for k := range q.Attributes {
		attrKeys = append(attrKeys, k)
	}
	sort.Strings(attrKeys)
	for _, k := range attrKeys {
		sql.WriteString(" AND json_extract(l.attributes, ?) = ?")
		args = append(args, "$."+k, q.Attributes[k])
	}

	if q.Pagination.AfterID > 0 {
		sql.WriteString(" AND l.id > ?")
		args = append(args, q.Pagination.AfterID)
	}
	if q.Pagination.BeforeID > 0 {
		sql.WriteString(" AND l.id < ?")
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

// runMigrations handles schema updates for existing databases.
func runMigrations(db *sql.DB) error {
	// Check if dedup_hash column exists
	hasColumn, err := columnExists(db, "logs", "dedup_hash")
	if err != nil {
		return fmt.Errorf("check column: %w", err)
	}

	if !hasColumn {
		// Fresh migration: add column, backfill, create index
		if _, err := db.Exec(`ALTER TABLE logs ADD COLUMN dedup_hash INTEGER`); err != nil {
			return fmt.Errorf("add dedup_hash column: %w", err)
		}

		// Backfill existing rows in batches
		if err := backfillDedupHashes(db); err != nil {
			return fmt.Errorf("backfill hashes: %w", err)
		}

		// Create the unique index after backfill
		if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_logs_dedup ON logs(dedup_hash) WHERE dedup_hash IS NOT NULL`); err != nil {
			return fmt.Errorf("create dedup index: %w", err)
		}
		return nil
	}

	// Column exists - check if index exists
	hasIndex, err := indexExists(db, "logs", "idx_logs_dedup")
	if err != nil {
		return fmt.Errorf("check index: %w", err)
	}

	if !hasIndex {
		// Column exists but index doesn't - need to backfill NULLs and deduplicate
		// This handles the case where a previous migration partially completed,
		// or where duplicates were inserted before the unique index was created.
		if err := backfillDedupHashes(db); err != nil {
			return fmt.Errorf("backfill hashes: %w", err)
		}
		if err := deduplicateHashes(db); err != nil {
			return fmt.Errorf("deduplicate hashes: %w", err)
		}
		if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_logs_dedup ON logs(dedup_hash) WHERE dedup_hash IS NOT NULL`); err != nil {
			return fmt.Errorf("create dedup index: %w", err)
		}
	}

	return nil
}

// columnExists checks if a column exists in the given table.
func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// indexExists checks if an index exists on the given table.
func indexExists(db *sql.DB, table, indexName string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA index_list(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var seq int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			return false, err
		}
		if name == indexName {
			return true, nil
		}
	}
	return false, rows.Err()
}

// backfillDedupHashes computes and sets dedup_hash for existing rows.
func backfillDedupHashes(db *sql.DB) error {
	const batchSize = 10000

	for {
		// Fetch batch of rows without hashes
		rows, err := db.Query(`
			SELECT id, timestamp, namespace, pod, container, message
			FROM logs
			WHERE dedup_hash IS NULL
			LIMIT ?
		`, batchSize)
		if err != nil {
			return err
		}

		type row struct {
			id        int64
			timestamp int64
			namespace string
			pod       string
			container string
			message   string
		}
		var batch []row

		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.timestamp, &r.namespace, &r.pod, &r.container, &r.message); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		rows.Close()

		if len(batch) == 0 {
			break // All rows processed
		}

		// Update batch with computed hashes
		tx, err := db.Begin()
		if err != nil {
			return err
		}

		stmt, err := tx.Prepare(`UPDATE logs SET dedup_hash = ? WHERE id = ?`)
		if err != nil {
			tx.Rollback()
			return err
		}

		for _, r := range batch {
			hash := computeDedupHash(r.timestamp, r.namespace, r.pod, r.container, r.message)
			if _, err := stmt.Exec(hash, r.id); err != nil {
				stmt.Close()
				tx.Rollback()
				return err
			}
		}

		stmt.Close()
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

// deduplicateHashes removes duplicate dedup_hash entries, keeping the oldest (smallest id) for each hash.
// This handles the case where duplicates exist in the database before the unique index was created.
func deduplicateHashes(db *sql.DB) error {
	const batchSize = 10000

	for {
		// Delete duplicates in batches, keeping the row with smallest id for each hash
		result, err := db.Exec(`
			DELETE FROM logs WHERE id IN (
				SELECT l.id FROM logs l
				WHERE l.dedup_hash IS NOT NULL
				AND EXISTS (
					SELECT 1 FROM logs l2
					WHERE l2.dedup_hash = l.dedup_hash
					AND l2.id < l.id
				)
				LIMIT ?
			)
		`, batchSize)
		if err != nil {
			return err
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			break
		}
	}
	return nil
}
