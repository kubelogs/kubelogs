# Storage Backend

The storage package provides a pluggable backend for persisting and querying Kubernetes logs.

**Related Documentation:**
- [Overall Architecture](architecture.md)
- [Storage Service](server.md) - gRPC server for multi-node access
- [Collector](collector.md) - Log collection component

## Architecture

```
internal/storage/
├── storage.go       # Store interface
├── model.go         # Data types (LogEntry, Query, etc.)
├── testing.go       # Test suite for backend implementations
├── sqlite/          # SQLite + FTS5 implementation
└── remote/          # gRPC client for centralized storage
```

## Store Interface

```go
type Store interface {
    Write(ctx context.Context, entries LogBatch) (int, error)
    Query(ctx context.Context, q Query) (*QueryResult, error)
    GetByID(ctx context.Context, id int64) (*LogEntry, error)
    Delete(ctx context.Context, olderThan time.Time) (int64, error)
    Stats(ctx context.Context) (*Stats, error)
    Close() error
}
```

### Methods

| Method | Description |
|--------|-------------|
| `Write` | Persist a batch of log entries. Returns count written. |
| `Query` | Search logs with filters, full-text search, and pagination. |
| `GetByID` | Retrieve a single entry by ID. Returns `ErrNotFound` if missing. |
| `Delete` | Remove entries older than timestamp. Used for retention. |
| `Stats` | Return storage statistics (count, size, time range). |
| `Close` | Release resources. Flushes any buffered writes. |

### Optional: WriteOptimizer

Backends that benefit from batching can implement:

```go
type WriteOptimizer interface {
    Flush(ctx context.Context) error
    SetWriteBuffer(entries int)
}
```

## Data Model

### LogEntry

```go
type LogEntry struct {
    ID         int64             // Assigned by storage
    Timestamp  time.Time
    Namespace  string            // K8s namespace (indexed)
    Pod        string            // K8s pod name (indexed)
    Container  string            // Container name (indexed)
    Severity   Severity          // Log level
    Message    string            // Log body (full-text indexed)
    Attributes map[string]string // Structured fields
}
```

### Severity Levels

```go
const (
    SeverityUnknown Severity = iota
    SeverityTrace   // 1
    SeverityDebug   // 2
    SeverityInfo    // 3
    SeverityWarn    // 4
    SeverityError   // 5
    SeverityFatal   // 6
)
```

Use `ParseSeverity(s string)` to convert from strings like `"INFO"`, `"error"`, `"WARNING"`.

### Query

```go
type Query struct {
    StartTime   time.Time         // Inclusive
    EndTime     time.Time         // Exclusive
    Search      string            // Full-text search (FTS5 syntax)
    Namespace   string            // Exact match
    Pod         string            // Exact match
    Container   string            // Exact match
    MinSeverity Severity          // Returns entries >= this level
    Attributes  map[string]string // All must match (AND)
    Pagination  Pagination
}
```

Zero values mean "no filter" for that field.

### Pagination

```go
type Pagination struct {
    Limit    int   // Max entries (default: 100)
    AfterID  int64 // Cursor for forward pagination
    BeforeID int64 // Cursor for reverse pagination
    Order    Order // OrderDesc (default) or OrderAsc
}
```

Cursor-based pagination using entry IDs. More efficient than OFFSET for large datasets.

## SQLite Backend

The default backend uses SQLite with FTS5 for full-text search.

### Usage

```go
import "github.com/kubelogs/kubelogs/internal/storage/sqlite"

store, err := sqlite.New(sqlite.Config{
    Path:            "/var/lib/kubelogs/logs.db",
    WriteBufferSize: 1000, // Entries buffered before flush
})
if err != nil {
    log.Fatal(err)
}
defer store.Close()
```

Use `Path: ":memory:"` for in-memory database (testing).

### Schema

**Main table** (`logs`):
- `id` - INTEGER PRIMARY KEY (rowid alias)
- `timestamp` - INTEGER (Unix nanoseconds)
- `namespace`, `pod`, `container` - TEXT
- `severity` - INTEGER (0-6)
- `message` - TEXT
- `attributes` - TEXT (JSON, nullable)

**Indexes**:
- `idx_logs_k8s` - Composite on (namespace, pod, container)
- `idx_logs_timestamp` - Descending timestamp
- `idx_logs_severity` - Severity level

**FTS5 table** (`logs_fts`):
- Virtual table with `content='logs'` (no data duplication)
- Tokenizer: `porter unicode61` (stemming + Unicode)
- Synchronized via triggers on INSERT/UPDATE/DELETE

### Full-Text Search Syntax

The `Search` field in queries accepts FTS5 syntax:

| Pattern | Example | Matches |
|---------|---------|---------|
| Single term | `error` | Messages containing "error" |
| Phrase | `"connection refused"` | Exact phrase |
| Boolean AND | `error AND timeout` | Both terms |
| Boolean OR | `error OR warning` | Either term |
| Prefix | `connect*` | "connection", "connected", etc. |
| NOT | `error NOT timeout` | "error" without "timeout" |
| NEAR | `NEAR(error timeout, 5)` | Terms within 5 tokens |

### Performance Tuning

SQLite pragmas applied on open:

```sql
PRAGMA journal_mode = WAL;      -- Concurrent read/write
PRAGMA synchronous = NORMAL;    -- Durability vs speed tradeoff
PRAGMA cache_size = -64000;     -- 64MB cache
PRAGMA temp_store = MEMORY;     -- Temp tables in memory
PRAGMA mmap_size = 268435456;   -- 256MB memory-mapped I/O
```

**Write buffering**: Entries are buffered (default: 1000) and batch-inserted in a single transaction. This reduces fsync overhead significantly. Call `Flush()` to force immediate persistence.

**Query behavior**: `Query()` automatically flushes the buffer before searching to ensure recent writes are visible.

## Remote Client

For multi-node deployments, the remote client implements `Store` over gRPC.

### Usage

```go
import "github.com/kubelogs/kubelogs/internal/storage/remote"

client, err := remote.NewClient("kubelogs-server:50051")
if err != nil {
    log.Fatal(err)
}
defer client.Close()

// Use exactly like local SQLite
n, err := client.Write(ctx, entries)
result, err := client.Query(ctx, query)
```

### When to Use

| Scenario | Backend |
|----------|---------|
| Development / single node | SQLite (`sqlite.New(...)`) |
| Multi-node cluster | Remote (`remote.NewClient(...)`) |
| Testing | SQLite with `:memory:` |

### How It Works

```
Collector                           Storage Service
    │                                     │
    │  Write(entries)                     │
    │ ─────────────────────────────────▶  │
    │        gRPC WriteRequest            │
    │                                     │  SQLite.Write()
    │  WriteResponse{count: N}            │
    │ ◀─────────────────────────────────  │
    │                                     │
```

The remote client:
- Translates `storage.LogEntry` to protobuf messages
- Handles gRPC connection and error codes
- Converts `codes.NotFound` → `storage.ErrNotFound`

See [Storage Service](server.md) for the server-side implementation.

## Implementing a New Backend

1. Create a new package under `internal/storage/` (e.g., `internal/storage/s3/`)

2. Implement the `Store` interface

3. Verify with the shared test suite:

```go
func TestStore(t *testing.T) {
    storage.StoreTestSuite(t, func() (storage.Store, func()) {
        store := NewMyStore(...)
        return store, func() { store.Close() }
    })
}
```

The test suite covers:
- Write and query roundtrip
- Time range filtering
- Namespace/pod/container filtering
- Severity filtering
- Attribute filtering
- GetByID and ErrNotFound
- Delete by timestamp
- Stats retrieval
- Cursor pagination

## Example Usage

### Ingesting Logs

```go
entries := storage.LogBatch{
    {
        Timestamp: time.Now(),
        Namespace: "production",
        Pod:       "api-server-xyz",
        Container: "app",
        Severity:  storage.SeverityInfo,
        Message:   "Request handled successfully",
        Attributes: map[string]string{
            "method":      "GET",
            "path":        "/api/users",
            "status_code": "200",
            "duration_ms": "45",
        },
    },
}

n, err := store.Write(ctx, entries)
```

### Querying Logs

```go
// Find errors in production namespace containing "database"
result, err := store.Query(ctx, storage.Query{
    StartTime:   time.Now().Add(-1 * time.Hour),
    EndTime:     time.Now(),
    Namespace:   "production",
    MinSeverity: storage.SeverityError,
    Search:      "database",
    Pagination:  storage.Pagination{Limit: 50},
})

for _, entry := range result.Entries {
    fmt.Printf("[%s] %s/%s: %s\n",
        entry.Severity,
        entry.Namespace,
        entry.Pod,
        entry.Message,
    )
}

// Fetch next page
if result.HasMore {
    result, _ = store.Query(ctx, storage.Query{
        // ... same filters ...
        Pagination: storage.Pagination{
            Limit:   50,
            AfterID: result.NextCursor,
        },
    })
}
```

### Retention Policy

```go
// Delete logs older than 7 days
cutoff := time.Now().Add(-7 * 24 * time.Hour)
deleted, err := store.Delete(ctx, cutoff)
log.Printf("Deleted %d old entries", deleted)
```
