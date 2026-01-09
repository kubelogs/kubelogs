# Storage Service Architecture

The Storage Service is a centralized gRPC server that receives logs from collectors and stores them in SQLite.

## Overview

```
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ Collector 1 │  │ Collector 2 │  │ Collector 3 │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘
       │                │                │
       └────────────────┼────────────────┘
                        │ gRPC (port 50051)
                        ▼
              ┌──────────────────────────────────┐
              │        Storage Service           │
              │                                  │
              │  ┌────────────────────────────┐  │
              │  │       gRPC Server          │  │
              │  │  - StorageService          │  │
              │  │  - Health checks           │  │
              │  │  - Reflection (debug)      │  │
              │  └─────────────┬──────────────┘  │
              │                │                 │
              │                ▼                 │
              │  ┌────────────────────────────┐  │
              │  │      Storage Backend       │  │
              │  │  - SQLite + FTS5           │  │
              │  │  - Write buffering         │  │
              │  │  - Full-text search        │  │
              │  └────────────────────────────┘  │
              └──────────────────────────────────┘
```

## gRPC API

### Service Definition

```protobuf
service StorageService {
  // Write persists a batch of log entries.
  rpc Write(WriteRequest) returns (WriteResponse);

  // Query searches for log entries matching the given criteria.
  rpc Query(QueryRequest) returns (QueryResponse);

  // GetByID retrieves a single entry by its ID.
  rpc GetByID(GetByIDRequest) returns (GetByIDResponse);

  // Delete removes entries older than the given timestamp.
  rpc Delete(DeleteRequest) returns (DeleteResponse);

  // Stats returns storage statistics.
  rpc Stats(StatsRequest) returns (StatsResponse);
}
```

### Message Types

**LogEntry**:
```protobuf
message LogEntry {
  int64 id = 1;              // Assigned by storage
  int64 timestamp_nanos = 2; // Unix nanoseconds
  string namespace = 3;
  string pod = 4;
  string container = 5;
  uint32 severity = 6;       // 0=Unknown, 1=Trace, ..., 6=Fatal
  string message = 7;
  map<string, string> attributes = 8;
}
```

**WriteRequest/Response**:
```protobuf
message WriteRequest {
  repeated LogEntry entries = 1;
}

message WriteResponse {
  int32 count = 1;  // Number of entries written
}
```

**QueryRequest**:
```protobuf
message QueryRequest {
  int64 start_time_nanos = 1;  // 0 = no lower bound
  int64 end_time_nanos = 2;    // 0 = no upper bound
  string search = 3;           // Full-text search
  string namespace = 4;        // Exact match
  string pod = 5;              // Exact match
  string container = 6;        // Exact match
  uint32 min_severity = 7;     // Returns entries >= this level
  map<string, string> attributes = 8;
  int32 limit = 9;             // Max results (default: 100)
  int64 after_id = 10;         // Cursor for forward pagination
  int64 before_id = 11;        // Cursor for reverse pagination
  Order order = 12;            // DESC (default) or ASC
}
```

## Components

### gRPC Server (`internal/server/server.go`)

Wraps the `storage.Store` interface with gRPC handlers.

```go
type Server struct {
    storagepb.UnimplementedStorageServiceServer
    store storage.Store
}

func New(store storage.Store) *Server
```

**Responsibilities**:
- Convert protobuf messages to storage types
- Handle gRPC error codes (NotFound, Internal)
- Delegate operations to storage backend

### Health Service

Standard gRPC health checking protocol for Kubernetes probes.

```yaml
livenessProbe:
  grpc:
    port: 50051
readinessProbe:
  grpc:
    port: 50051
```

### Reflection Service

Enabled for debugging with tools like `grpcurl`:

```bash
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 describe kubelogs.storage.v1.StorageService
```

## Remote Client

### Client Implementation (`internal/storage/remote/client.go`)

Implements `storage.Store` interface over gRPC.

```go
type Client struct {
    conn   *grpc.ClientConn
    client storagepb.StorageServiceClient
}

func NewClient(addr string) (*Client, error)

// Implements storage.Store
func (c *Client) Write(ctx context.Context, entries storage.LogBatch) (int, error)
func (c *Client) Query(ctx context.Context, q storage.Query) (*storage.QueryResult, error)
func (c *Client) GetByID(ctx context.Context, id int64) (*storage.LogEntry, error)
func (c *Client) Delete(ctx context.Context, olderThan time.Time) (int64, error)
func (c *Client) Stats(ctx context.Context) (*storage.Stats, error)
func (c *Client) Close() error
```

**Features**:
- Transparent `storage.Store` implementation
- Automatic connection management
- Error translation (gRPC codes → storage errors)

### Usage in Collector

```go
// Multi-node mode: use remote storage
if addr := os.Getenv("KUBELOGS_STORAGE_ADDR"); addr != "" {
    store, err = remote.NewClient(addr)
} else {
    // Single-node mode: use local SQLite
    store, err = sqlite.New(sqlite.Config{Path: dbPath})
}

// Collector uses store interface - doesn't know the difference
collector.New(clientset, store, cfg)
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `KUBELOGS_LISTEN_ADDR` | `:50051` | gRPC server listen address |
| `KUBELOGS_DB_PATH` | `kubelogs.db` | SQLite database file path |

### Command Line

```bash
# Start storage service
KUBELOGS_DB_PATH=/data/kubelogs.db \
KUBELOGS_LISTEN_ADDR=:50051 \
./kubelogs-server
```

## Kubernetes Deployment

### Deployment Manifest

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kubelogs-server
  labels:
    app: kubelogs-server
spec:
  replicas: 1  # Single replica for SQLite
  selector:
    matchLabels:
      app: kubelogs-server
  template:
    metadata:
      labels:
        app: kubelogs-server
    spec:
      containers:
      - name: server
        image: kubelogs:latest
        command: ["/kubelogs-server"]
        ports:
        - name: grpc
          containerPort: 50051
        env:
        - name: KUBELOGS_DB_PATH
          value: /data/kubelogs.db
        - name: KUBELOGS_LISTEN_ADDR
          value: ":50051"
        volumeMounts:
        - name: data
          mountPath: /data
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "1000m"
        livenessProbe:
          grpc:
            port: 50051
          initialDelaySeconds: 5
          periodSeconds: 10
        readinessProbe:
          grpc:
            port: 50051
          initialDelaySeconds: 5
          periodSeconds: 5
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: kubelogs-data
---
apiVersion: v1
kind: Service
metadata:
  name: kubelogs-server
spec:
  selector:
    app: kubelogs-server
  ports:
  - name: grpc
    port: 50051
    targetPort: 50051
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: kubelogs-data
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

## Data Flow

### Write Operation

```
Collector
    │
    │ WriteRequest{entries: [...]}
    ▼
┌─────────────────────────────────────┐
│           gRPC Server               │
│                                     │
│  1. Receive WriteRequest            │
│  2. Convert protobuf → LogEntry     │
│  3. Call store.Write(batch)         │
│  4. Return WriteResponse{count}     │
└─────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────┐
│         SQLite Store                │
│                                     │
│  1. Buffer entries                  │
│  2. Flush when buffer full          │
│  3. Insert with transaction         │
│  4. Update FTS5 index               │
└─────────────────────────────────────┘
```

### Query Operation

```
Client (grpcurl, future Web UI)
    │
    │ QueryRequest{namespace: "prod", search: "error"}
    ▼
┌─────────────────────────────────────┐
│           gRPC Server               │
│                                     │
│  1. Receive QueryRequest            │
│  2. Convert to storage.Query        │
│  3. Handle zero time = no filter    │
│  4. Call store.Query(q)             │
│  5. Convert results to protobuf     │
│  6. Return QueryResponse            │
└─────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────┐
│         SQLite Store                │
│                                     │
│  1. Flush pending writes            │
│  2. Build SQL with filters          │
│  3. Use FTS5 for search queries     │
│  4. Apply pagination                │
│  5. Return results                  │
└─────────────────────────────────────┘
```

## Error Handling

### gRPC Status Codes

| Situation | gRPC Code | Description |
|-----------|-----------|-------------|
| Entry not found | `NotFound` | GetByID with unknown ID |
| Internal error | `Internal` | Database errors, write failures |
| Invalid request | `InvalidArgument` | Malformed request (future) |

### Client Error Translation

```go
// Remote client translates gRPC errors to storage errors
func (c *Client) GetByID(ctx context.Context, id int64) (*storage.LogEntry, error) {
    resp, err := c.client.GetByID(ctx, &storagepb.GetByIDRequest{Id: id})
    if err != nil {
        if status.Code(err) == codes.NotFound {
            return nil, storage.ErrNotFound  // Translated error
        }
        return nil, err
    }
    // ...
}
```

## Performance Considerations

### Connection Pooling

gRPC uses HTTP/2 with connection multiplexing. A single connection handles multiple concurrent RPCs.

### Batching

Collectors batch logs (default 500 entries or 5s timeout) before sending to reduce RPC overhead.

### Write Buffering

SQLite store buffers writes (default 1000 entries) to batch inserts for better throughput.

### Query Optimization

- Indexes on namespace, pod, container, timestamp, severity
- FTS5 for full-text search (porter stemmer)
- Cursor-based pagination (no offset counting)

## Monitoring

### Logging

JSON-formatted logs to stdout:

```json
{"time":"2024-01-15T10:30:00Z","level":"INFO","msg":"server starting","address":":50051"}
{"time":"2024-01-15T10:30:00Z","level":"INFO","msg":"database opened","path":"/data/kubelogs.db"}
```

### Metrics (Future)

Planned Prometheus metrics:
- `kubelogs_write_total` - Total write requests
- `kubelogs_write_entries_total` - Total entries written
- `kubelogs_query_total` - Total query requests
- `kubelogs_query_duration_seconds` - Query latency histogram

## Graceful Shutdown

```
SIGTERM received
        │
        ▼
Set health status to NOT_SERVING
        │
        ▼
Stop accepting new connections
        │
        ▼
Wait for in-flight RPCs to complete
        │
        ▼
Close storage backend
        │
        ▼
Exit
```

## Testing

### Unit Tests

```bash
go test ./internal/server/...
```

Tests cover:
- Write and query operations
- GetByID with existing and non-existent IDs
- Stats retrieval
- gRPC error handling

### Integration Testing

```bash
# Terminal 1: Start server
KUBELOGS_DB_PATH=:memory: ./kubelogs-server

# Terminal 2: Test with grpcurl
grpcurl -plaintext -d '{"entries":[{"timestamp_nanos":1234567890,"namespace":"test","pod":"pod1","container":"main","message":"hello"}]}' \
  localhost:50051 kubelogs.storage.v1.StorageService/Write

grpcurl -plaintext -d '{"limit":10}' \
  localhost:50051 kubelogs.storage.v1.StorageService/Query
```

## Limitations

1. **Single Replica**: SQLite requires single-writer, no horizontal scaling
2. **No Authentication**: Currently uses insecure gRPC transport
3. **No Rate Limiting**: Relies on Kubernetes resource limits
4. **Synchronous Writes**: No async write acknowledgment

## Future Enhancements

1. **mTLS**: Encrypted and authenticated connections
2. **S3 Backend**: Replace SQLite for horizontal scaling
3. **Streaming Queries**: Server-side streaming for large result sets
4. **Write-Ahead Confirmation**: Async writes with delivery guarantees
5. **Prometheus Metrics**: Built-in observability
