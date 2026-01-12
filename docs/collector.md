# Collector Architecture

The Collector is a DaemonSet component that tails container logs via the Kubernetes API and writes them to storage. It runs on each node and collects logs only from pods scheduled on that node.

**Related Documentation:**
- [Overall Architecture](architecture.md)
- [Storage Service](server.md)
- [Storage Backend](storage.md)

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              Collector                                   │
│                                                                          │
│  ┌──────────────┐     ┌─────────────────┐     ┌──────────────────────┐  │
│  │              │     │                 │     │                      │  │
│  │  Pod         │────▶│  Stream         │────▶│     Batcher          │  │
│  │  Discovery   │     │  Manager        │     │                      │  │
│  │              │     │                 │     │                      │  │
│  └──────────────┘     └─────────────────┘     └──────────────────────┘  │
│         │                     │                         │               │
│         │                     │                         │               │
│         ▼                     ▼                         ▼               │
│  ┌──────────────┐     ┌─────────────────┐     ┌──────────────────────┐  │
│  │  Kubernetes  │     │   Log Streams   │     │      Storage         │  │
│  │  API Server  │     │   (per container)│     │      Interface       │  │
│  └──────────────┘     └─────────────────┘     └──────────────────────┘  │
│                               │                                          │
│                               ▼                                          │
│                       ┌─────────────────┐                               │
│                       │     Parser      │                               │
│                       │  (timestamp,    │                               │
│                       │   severity,     │                               │
│                       │   attributes)   │                               │
│                       └─────────────────┘                               │
└─────────────────────────────────────────────────────────────────────────┘
```

## Components

### PodDiscovery (`discovery.go`)

Watches for pod changes on the current node using Kubernetes informers.

**Responsibilities:**
- Maintains a local cache of pods via SharedInformerFactory
- Filters pods by `spec.nodeName` field selector
- Tracks container states to detect starts, stops, and restarts
- Emits `PodEvent` when containers start or stop

**Key Design Decisions:**
- Uses informers instead of polling for efficiency
- Field selector at API level reduces network traffic
- Tracks `containerID` to detect container restarts (same pod, new container instance)

```go
type PodEvent struct {
    Type      PodEventType  // ContainerStarted or ContainerStopped
    Container ContainerRef
}

type ContainerRef struct {
    Namespace     string
    PodName       string
    PodUID        string  // Distinguishes pods with same name after restart
    ContainerName string
}
```

### StreamManager (`streammanager.go`)

Coordinates multiple concurrent log streams with resource limits.

**Responsibilities:**
- Manages goroutine pool for log streams
- Enforces `MaxConcurrentStreams` limit via semaphore
- Routes all log lines to a single output channel
- Handles stream lifecycle (start/stop)

**Concurrency Model:**

```
                    ┌─────────────┐
                    │  Semaphore  │ (capacity: MaxConcurrentStreams)
                    └──────┬──────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
         ▼                 ▼                 ▼
   ┌──────────┐     ┌──────────┐     ┌──────────┐
   │ Stream 1 │     │ Stream 2 │     │ Stream N │
   │ goroutine│     │ goroutine│     │ goroutine│
   └────┬─────┘     └────┬─────┘     └────┬─────┘
        │                │                │
        └────────────────┼────────────────┘
                         │
                         ▼
               ┌─────────────────┐
               │  Output Channel │ (buffered)
               └─────────────────┘
```

### Stream (`stream.go`)

Tails logs from a single container via the Kubernetes API.

**Responsibilities:**
- Opens log stream using `pods/log` subresource
- Reads lines with `bufio.Scanner`
- Parses timestamp and severity from each line
- Implements retry with exponential backoff

**Kubernetes API Integration:**

```go
opts := &corev1.PodLogOptions{
    Container:  containerName,
    Follow:     true,       // Stream continuously
    Timestamps: true,       // Prefix each line with RFC3339Nano timestamp
}
stream, _ := clientset.CoreV1().Pods(namespace).GetLogs(podName, opts).Stream(ctx)
```

**Retry Strategy:**

| Error Type | Retryable | Action |
|------------|-----------|--------|
| Pod not found | No | Terminate stream |
| Container not running | Yes | Backoff and retry |
| Connection reset | Yes | Immediate retry |
| Context canceled | No | Clean shutdown |
| EOF | No | Normal termination |

Backoff: 1s → 2s → 4s → ... → 30s (max)

### Parser (`parser.go`)

Extracts timestamps and severity levels from log lines.

**Timestamp Parsing:**

Kubernetes prepends RFC3339Nano timestamps when `Timestamps: true`:
```
2024-01-15T10:30:00.123456789Z {"level":"INFO","message":"Started"}
└──────────────────────────┘ └─────────────────────────────────────┘
         Timestamp                         Message
```

**Severity Detection:**

Supports multiple log formats (case-insensitive):

| Format | Example | Detection Pattern |
|--------|---------|-------------------|
| JSON | `{"level":"INFO"}` | `"level": "INFO"` |
| Bracket | `[ERROR] Failed` | `[ERROR]` |
| Key-value | `level=WARN msg=...` | `level=WARN` |
| Prefix | `INFO: Starting` | `INFO:` |

Severity levels: `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL`

**Structured Log Parsing (JSON):**

For JSON-formatted logs, the parser extracts well-known fields into the `Attributes` map for filtering and correlation:

| Canonical Field | Aliases | Description |
|-----------------|---------|-------------|
| `msg` | msg, message, error, err | Log message content |
| `trace_id` | trace_id, traceId, trace-id, traceID | Distributed trace ID |
| `span_id` | span_id, spanId, span-id, spanID | Span ID for tracing |
| `request_id` | request_id, requestId, request-id, requestID, req_id | Request correlation ID |
| `caller` | caller, source, file, location | Source code location |
| `service` | service, app, application | Service/app name |
| `user_id` | user_id, userId, user | User identifier |

Example JSON log:
```json
{"level":"ERROR","msg":"connection failed","trace_id":"abc123","service":"api"}
```

Extracted attributes:
```json
{"msg": "connection failed", "trace_id": "abc123", "service": "api"}
```

These attributes are stored in the `attributes` JSON column and can be queried using attribute filters. Only scalar values (strings, numbers, booleans) are extracted; nested objects and arrays are skipped.

### Batcher (`batcher.go`)

Buffers log entries and writes them to storage in batches.

**Responsibilities:**
- Accumulates `LogLine` from streams
- Converts to `storage.LogEntry`
- Flushes on size threshold or timeout
- Performs final flush on shutdown

**Flush Triggers:**

```
         LogLine arrives
               │
               ▼
      ┌────────────────┐
      │ Buffer append  │
      └───────┬────────┘
              │
    ┌─────────┴─────────┐
    │                   │
    ▼                   ▼
len >= BatchSize?   Timeout elapsed?
    │                   │
    ▼                   ▼
   Yes ──────────▶ Flush to Storage
    │
    ▼
   No ──────────▶ Wait for more
```

**Shutdown Sequence:**
1. Context canceled
2. Flush remaining buffer with 5s timeout
3. Return

## Data Flow

```
┌──────────────────────────────────────────────────────────────────────────┐
│                            Data Flow                                      │
│                                                                           │
│  Kubernetes API          Collector                        Storage         │
│                                                                           │
│  ┌─────────────┐    ┌──────────────────────────────┐    ┌─────────────┐  │
│  │             │    │                              │    │             │  │
│  │ Pod Watch   │───▶│ PodDiscovery                 │    │             │  │
│  │ (informer)  │    │   │                          │    │             │  │
│  │             │    │   ▼ PodEvent                 │    │             │  │
│  └─────────────┘    │ Collector.handlePodEvent()   │    │             │  │
│                     │   │                          │    │             │  │
│  ┌─────────────┐    │   ▼                          │    │             │  │
│  │             │    │ StreamManager.StartStream()  │    │             │  │
│  │ Pod Logs    │───▶│   │                          │    │             │  │
│  │ (streaming) │    │   ▼                          │    │             │  │
│  │             │    │ Stream.Start()               │    │             │  │
│  └─────────────┘    │   │                          │    │             │  │
│                     │   ▼ (per line)               │    │             │  │
│                     │ Parser.Parse()               │    │             │  │
│                     │   │                          │    │             │  │
│                     │   ▼ LogLine                  │    │             │  │
│                     │ output channel               │    │             │  │
│                     │   │                          │    │             │  │
│                     │   ▼                          │    │             │  │
│                     │ Batcher.Run()                │    │             │  │
│                     │   │                          │    │             │  │
│                     │   ▼ LogBatch                 │    │             │  │
│                     │ store.Write() ─────────────────▶│ SQLite/S3   │  │
│                     │                              │    │             │  │
│                     └──────────────────────────────┘    └─────────────┘  │
└──────────────────────────────────────────────────────────────────────────┘
```

## Memory Management

Designed to run within 256MB RAM constraint.

### Memory Budget

| Component | Calculation | Estimate |
|-----------|-------------|----------|
| Stream goroutines | 100 streams × 10KB stack | 1 MB |
| Stream buffers | 100 × 64KB scanner buffer | 6.4 MB |
| Output channel | 10,000 × 200 bytes | 2 MB |
| Batcher buffer | 500 × 500 bytes | 250 KB |
| Informer cache | 100 pods × 5KB | 500 KB |
| **Total Collector** | | **~10 MB** |

### Backpressure

Three-level buffering prevents memory exhaustion:

```
Level 1: Per-stream scanner buffer (64KB)
    │
    ▼
Level 2: StreamManager output channel (10,000 lines)
    │
    ▼
Level 3: Batcher buffer (500 entries)
    │
    ▼
Storage
```

When storage is slow:
1. Batcher buffer fills → blocks reading from output channel
2. Output channel fills → individual streams block on send
3. Stream buffers fill → TCP backpressure to kubelet

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_NAME` | (required) | Current node name (Kubernetes downward API) |
| `KUBELOGS_STORAGE_ADDR` | (none) | Storage service address for multi-node mode (e.g., `kubelogs-server:50051`) |
| `KUBELOGS_MAX_STREAMS` | 100 | Maximum concurrent log streams |
| `KUBELOGS_BATCH_SIZE` | 500 | Entries per storage write |
| `KUBELOGS_BATCH_TIMEOUT` | 5s | Max time before flush |
| `KUBELOGS_STREAM_BUFFER` | 1000 | Lines buffered per stream |
| `KUBELOGS_SINCE` | (none) | Collect logs from last duration (e.g., "1h") |
| `KUBELOGS_EXCLUDE_NS` | kube-system | Namespaces to skip (comma-separated) |
| `KUBELOGS_INCLUDE_NS` | (all) | Only collect from these namespaces |
| `KUBELOGS_SHUTDOWN_TIMEOUT` | 30s | Grace period for draining logs |

### Storage Modes

The collector supports two storage modes:

**Single-Node Mode** (default):
- Uses local SQLite database
- Suitable for development or single-node clusters
- No `KUBELOGS_STORAGE_ADDR` configured

**Multi-Node Mode**:
- Sends logs to centralized Storage Service via gRPC
- Required for production multi-node clusters
- Set `KUBELOGS_STORAGE_ADDR` to storage service address

```
Single-Node:                    Multi-Node:
┌─────────────┐                 ┌─────────────┐  ┌─────────────┐
│ Collector   │                 │ Collector   │  │ Collector   │
│   + SQLite  │                 │  (Node 1)   │  │  (Node 2)   │
└─────────────┘                 └──────┬──────┘  └──────┬──────┘
                                       │                │
                                       └────────┬───────┘
                                                │ gRPC
                                                ▼
                                      ┌──────────────────┐
                                      │  Storage Service │
                                      │    (+ SQLite)    │
                                      └──────────────────┘
```

### Kubernetes DaemonSet Configuration

**Multi-Node Mode** (recommended for production):

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kubelogs-collector
spec:
  selector:
    matchLabels:
      app: kubelogs-collector
  template:
    metadata:
      labels:
        app: kubelogs-collector
    spec:
      serviceAccountName: kubelogs
      containers:
      - name: collector
        image: kubelogs:latest
        command: ["/kubelogs-collector"]
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: KUBELOGS_STORAGE_ADDR
          value: "kubelogs-server:50051"
        resources:
          requests:
            memory: "64Mi"
            cpu: "100m"
          limits:
            memory: "256Mi"
            cpu: "500m"
```

**Single-Node Mode** (development):

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: kubelogs
spec:
  selector:
    matchLabels:
      app: kubelogs
  template:
    spec:
      serviceAccountName: kubelogs
      containers:
      - name: collector
        image: kubelogs:latest
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        # No KUBELOGS_STORAGE_ADDR = uses local SQLite
        volumeMounts:
        - name: data
          mountPath: /data
        resources:
          requests:
            memory: "64Mi"
            cpu: "100m"
          limits:
            memory: "256Mi"
            cpu: "500m"
      volumes:
      - name: data
        emptyDir: {}
```

### RBAC Requirements

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubelogs
rules:
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  verbs: ["get", "list", "watch"]
```

## Error Handling

### Stream Failures

Individual stream failures don't affect other streams:

```
Stream 1: Running ────────────────────────────▶
Stream 2: Running ──── Error ── Retry ── Running ──▶
Stream 3: Running ────────────────────────────▶
```

### Storage Failures

On write failure:
1. Log error with batch size
2. Drop batch (bounded memory)
3. Continue collecting

Future improvement: retry queue with bounded size.

### Graceful Shutdown

```
SIGTERM received
      │
      ▼
Cancel collector context
      │
      ▼
StreamManager.StopAll()
├── Cancel all stream contexts
├── Wait for goroutines
└── Release semaphore slots
      │
      ▼
Wait for Batcher to drain (ShutdownTimeout)
      │
      ▼
Final Batcher.Flush()
      │
      ▼
Exit
```

## Metrics

Available via `Collector.Stats()`:

```go
type CollectorStats struct {
    ActiveStreams  int           // Current streaming containers
    TotalLinesRead int64         // Lines processed
    TotalErrors    int64         // Stream/write errors
    BatcherStats   BatcherStats  // Write statistics
    StreamStats    []StreamStats // Per-stream statistics
}
```

## Testing

### Unit Tests

```bash
go test ./internal/collector/...
```

Coverage:
- `config_test.go`: Configuration validation, namespace filtering
- `parser_test.go`: Timestamp parsing, severity detection
- `batcher_test.go`: Flush triggers, graceful shutdown

### Integration Testing

With a Kind cluster:

```bash
# Create cluster
kind create cluster

# Deploy test pods
kubectl run nginx --image=nginx
kubectl run busybox --image=busybox -- sh -c "while true; do echo hello; sleep 1; done"

# Run collector locally
NODE_NAME=kind-control-plane go run ./cmd/kubelogs
```

### Fake Client Testing

For unit tests without a real cluster:

```go
import "k8s.io/client-go/kubernetes/fake"

clientset := fake.NewSimpleClientset()
// Note: GetLogs doesn't work with fake client
// Use envtest or real cluster for log streaming tests
```

## Future Enhancements

1. **OTLP Protocol**: Accept logs via OpenTelemetry Protocol (gRPC/HTTP)
2. **Fluent Forward**: Accept logs from Fluent Bit/Fluentd
3. **Container File Tailing**: Direct file access for higher throughput
4. **Retry Queue**: Bounded retry for storage failures
5. **Metrics Export**: Prometheus metrics endpoint
