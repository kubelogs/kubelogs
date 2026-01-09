# kubelogs Architecture

kubelogs is a lightweight, single-binary log aggregator for Kubernetes designed for solo developers and small platform teams.

## System Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            Kubernetes Cluster                                │
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         User Workloads                                │   │
│  │  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐    │   │
│  │  │  Pod    │  │  Pod    │  │  Pod    │  │  Pod    │  │  Pod    │    │   │
│  │  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘  └────┬────┘    │   │
│  └───────┼────────────┼────────────┼────────────┼────────────┼─────────┘   │
│          │            │            │            │            │              │
│          │      Kubernetes API (pods/log)       │            │              │
│          │            │            │            │            │              │
│  ┌───────┴────────────┴────────────┴────────────┴────────────┴─────────┐   │
│  │                      Collector (DaemonSet)                           │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                  │   │
│  │  │ Collector   │  │ Collector   │  │ Collector   │                  │   │
│  │  │  Node 1     │  │  Node 2     │  │  Node 3     │                  │   │
│  │  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘                  │   │
│  └─────────┼────────────────┼────────────────┼─────────────────────────┘   │
│            │                │                │                              │
│            └────────────────┼────────────────┘                              │
│                             │ gRPC                                          │
│                             ▼                                               │
│                   ┌──────────────────┐                                      │
│                   │  Storage Service │  (Deployment)                        │
│                   │  ┌────────────┐  │                                      │
│                   │  │   SQLite   │  │                                      │
│                   │  └────────────┘  │                                      │
│                   └────────┬─────────┘                                      │
│                            │                                                │
│                            ▼                                                │
│                   ┌──────────────────┐                                      │
│                   │   Web UI / API   │  (future)                            │
│                   └──────────────────┘                                      │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Deployment Modes

### Single-Node Mode

For development or small clusters, run everything in one process:

```
┌─────────────────────────────────┐
│         Single Binary           │
│  ┌───────────┐  ┌───────────┐  │
│  │ Collector │─▶│  SQLite   │  │
│  └───────────┘  └───────────┘  │
└─────────────────────────────────┘
```

```bash
NODE_NAME=node-1 ./kubelogs-collector
# Uses local SQLite at ./kubelogs.db
```

### Multi-Node Mode

For production clusters with multiple nodes:

```
┌─────────────┐  ┌─────────────┐  ┌─────────────┐
│ Collector   │  │ Collector   │  │ Collector   │
│ (DaemonSet) │  │ (DaemonSet) │  │ (DaemonSet) │
└──────┬──────┘  └──────┬──────┘  └──────┬──────┘
       │                │                │
       └────────────────┼────────────────┘
                        │ gRPC :50051
                        ▼
              ┌──────────────────┐
              │  Storage Service │
              │   (Deployment)   │
              └──────────────────┘
```

```bash
# Storage Service (Deployment, 1 replica)
KUBELOGS_DB_PATH=/data/kubelogs.db ./kubelogs-server

# Collectors (DaemonSet, one per node)
NODE_NAME=$NODE_NAME KUBELOGS_STORAGE_ADDR=kubelogs-server:50051 ./kubelogs-collector
```

## Components

### Collector

**Purpose**: Tail container logs from pods on the local node.

**Deployment**: DaemonSet (one per node)

**Key Features**:
- Kubernetes API-based log streaming (`pods/log` subresource)
- Informer-based pod discovery with local cache
- Automatic handling of pod restarts and crashes
- Log parsing for timestamp and severity extraction
- Batched writes for efficiency

**Documentation**: [collector.md](collector.md)

### Storage Service

**Purpose**: Centralized log storage with gRPC API.

**Deployment**: Deployment (single replica for SQLite)

**Key Features**:
- gRPC API for write, query, and management operations
- SQLite backend with FTS5 full-text search
- Health checks for Kubernetes probes
- Graceful shutdown with connection draining

**Documentation**: [server.md](server.md)

### Storage Backend

**Purpose**: Persistent log storage with indexing and search.

**Current Implementation**: SQLite with FTS5

**Key Features**:
- Full-text search on log messages
- Indexed Kubernetes fields (namespace, pod, container)
- Time-range and severity filtering
- Cursor-based pagination
- Write buffering for performance

**Documentation**: [storage.md](storage.md)

## Data Flow

### Write Path

```
Container stdout/stderr
        │
        ▼
Kubernetes API (pods/log)
        │
        ▼
┌───────────────────────────────────────┐
│            Collector                   │
│                                        │
│  Pod Discovery ──▶ Stream Manager     │
│                         │              │
│                         ▼              │
│                    Log Streams         │
│                    (per container)     │
│                         │              │
│                         ▼              │
│                      Parser            │
│                    (timestamp,         │
│                     severity)          │
│                         │              │
│                         ▼              │
│                     Batcher            │
│                   (500 entries         │
│                    or 5s timeout)      │
└─────────────────────────┬─────────────┘
                          │
                          ▼
              ┌──────────────────────┐
              │   Storage Service    │
              │   (gRPC Server)      │
              │          │           │
              │          ▼           │
              │   ┌────────────┐     │
              │   │   SQLite   │     │
              │   │  + FTS5    │     │
              │   └────────────┘     │
              └──────────────────────┘
```

### Query Path

```
User / Web UI
      │
      ▼
┌──────────────────────┐
│   Storage Service    │
│   (gRPC Server)      │
│          │           │
│          ▼           │
│   Query Builder      │
│   - Time range       │
│   - Namespace/Pod    │
│   - Full-text search │
│   - Severity filter  │
│          │           │
│          ▼           │
│   ┌────────────┐     │
│   │   SQLite   │     │
│   │  + FTS5    │     │
│   └────────────┘     │
└──────────────────────┘
```

## Communication Protocols

### Collector → Storage Service

**Protocol**: gRPC over HTTP/2

**Service Definition**: `api/proto/storage.proto`

```protobuf
service StorageService {
  rpc Write(WriteRequest) returns (WriteResponse);
  rpc Query(QueryRequest) returns (QueryResponse);
  rpc GetByID(GetByIDRequest) returns (GetByIDResponse);
  rpc Delete(DeleteRequest) returns (DeleteResponse);
  rpc Stats(StatsRequest) returns (StatsResponse);
}
```

**Why gRPC**:
- Efficient binary serialization (protobuf)
- Built-in connection pooling
- Streaming support for future pagination
- Standard health check protocol

### Collector → Kubernetes API

**Protocol**: HTTP/2 (Kubernetes client-go)

**Endpoints Used**:
- `GET /api/v1/pods` - Pod discovery (via informer)
- `GET /api/v1/namespaces/{ns}/pods/{pod}/log` - Log streaming

## Resource Requirements

### Collector (per node)

| Resource | Request | Limit |
|----------|---------|-------|
| Memory | 64Mi | 256Mi |
| CPU | 100m | 500m |

**Memory Breakdown**:
- Stream goroutines: ~1MB (100 streams × 10KB)
- Stream buffers: ~6MB (100 × 64KB)
- Informer cache: ~500KB
- Channels/buffers: ~2MB
- **Total**: ~10MB typical, 30MB peak

### Storage Service

| Resource | Request | Limit |
|----------|---------|-------|
| Memory | 128Mi | 512Mi |
| CPU | 100m | 1000m |
| Storage | 10Gi+ | - |

**Memory Breakdown**:
- SQLite cache: 64MB (configurable)
- gRPC connections: ~1MB per client
- Query buffers: variable

## Security

### RBAC Requirements

**Collector ServiceAccount**:
```yaml
rules:
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  verbs: ["get", "list", "watch"]
```

### Network Policies

Recommended network policies:
1. Collectors can reach Storage Service on port 50051
2. Storage Service cannot initiate connections to Collectors
3. External access to Storage Service is blocked (internal only)

### Future: mTLS

The gRPC connection between Collectors and Storage Service currently uses insecure transport. Future versions will support mTLS for encryption and authentication.

## Failure Modes

### Collector Failure

| Failure | Impact | Recovery |
|---------|--------|----------|
| Pod crash | Logs from that node not collected | DaemonSet restarts pod |
| Stream error | Individual container logs interrupted | Automatic retry with backoff |
| Storage unreachable | Logs buffered, then dropped if full | Reconnect when available |

### Storage Service Failure

| Failure | Impact | Recovery |
|---------|--------|----------|
| Pod crash | All collectors lose connection | Deployment restarts pod |
| Disk full | Writes fail | Alert, add storage or enable retention |
| SQLite corruption | Data loss | Restore from backup |

### Kubernetes API Failure

| Failure | Impact | Recovery |
|---------|--------|----------|
| API server down | No new pod discovery | Existing streams continue |
| Network partition | Log streams interrupted | Reconnect when available |

## Limitations

1. **Single Storage Replica**: SQLite doesn't support concurrent writers, so the storage service runs as a single replica. For HA, use S3 backend (future).

2. **No Persistence Across Restarts**: Log collection restarts from current time after collector restart. Historical logs require pod restart.

3. **Memory-Bound Collection**: Very high log volumes may exceed collector memory limits. Configure `KUBELOGS_MAX_STREAMS` to limit.

4. **No Log Shipping**: Logs are stored locally in the cluster. No built-in export to external systems.

## Future Enhancements

1. **Web UI**: Built-in search and filter interface
2. **S3 Storage**: Parquet files on S3-compatible storage for scale
3. **OTLP Ingestion**: Accept logs via OpenTelemetry Protocol
4. **Fluent Forward**: Accept logs from Fluent Bit/Fluentd
5. **Log Alerting**: Pattern-based alerts on log content
6. **Multi-Tenancy**: Namespace-based access control
