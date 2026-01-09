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
    attributes  TEXT
);

CREATE INDEX IF NOT EXISTS idx_logs_k8s
    ON logs(namespace, pod, container);

CREATE INDEX IF NOT EXISTS idx_logs_timestamp
    ON logs(timestamp DESC);

CREATE INDEX IF NOT EXISTS idx_logs_severity
    ON logs(severity);

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
`

// pragmaSQL contains performance-critical SQLite settings.
const pragmaSQL = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA cache_size = -64000;
PRAGMA temp_store = MEMORY;
PRAGMA busy_timeout = 5000;
`
