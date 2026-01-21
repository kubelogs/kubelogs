package sqlite

// schemaSQL contains the DDL for creating the logs table and FTS5 index.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS logs (
    id          INTEGER PRIMARY KEY,
    timestamp   INTEGER NOT NULL,
    namespace   TEXT NOT NULL,
    pod         TEXT NOT NULL,
    container   TEXT NOT NULL,
    severity    INTEGER NOT NULL,
    message     TEXT NOT NULL,
    attributes  TEXT,
    dedup_hash  INTEGER
);

CREATE INDEX IF NOT EXISTS idx_logs_k8s
    ON logs(namespace, pod, container);

CREATE INDEX IF NOT EXISTS idx_logs_timestamp
    ON logs(timestamp DESC);

CREATE INDEX IF NOT EXISTS idx_logs_severity
    ON logs(severity);

-- Note: The unique index on dedup_hash is created during migration
-- after ensuring no duplicate hash values exist. This prevents
-- "UNIQUE constraint failed" errors when opening existing databases
-- that may have duplicate log entries from before deduplication was added.

CREATE VIRTUAL TABLE IF NOT EXISTS logs_fts USING fts5(
    message,
    content='logs',
    content_rowid='id',
    tokenize='porter unicode61 remove_diacritics 1'
);

CREATE TRIGGER IF NOT EXISTS logs_ai AFTER INSERT ON logs BEGIN
    INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
END;

CREATE TRIGGER IF NOT EXISTS logs_ad AFTER DELETE ON logs BEGIN
    INSERT INTO logs_fts(logs_fts, rowid, message)
        VALUES('delete', old.id, old.message);
END;

CREATE TRIGGER IF NOT EXISTS logs_au AFTER UPDATE ON logs BEGIN
    INSERT INTO logs_fts(logs_fts, rowid, message)
        VALUES('delete', old.id, old.message);
    INSERT INTO logs_fts(rowid, message) VALUES (new.id, new.message);
END;

-- Authentication tables
CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY,
    username   TEXT NOT NULL UNIQUE,
    password   TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);
`

// pragmaSQL contains performance-critical SQLite settings.
// Uses DELETE journal mode instead of WAL for compatibility with
// network-attached storage (Longhorn, NFS, etc.) where WAL's shared
// memory files can cause I/O errors.
const pragmaSQL = `
PRAGMA journal_mode = DELETE;
PRAGMA synchronous = FULL;
PRAGMA locking_mode = EXCLUSIVE;
PRAGMA cache_size = -64000;
PRAGMA temp_store = MEMORY;
PRAGMA busy_timeout = 10000;
`
