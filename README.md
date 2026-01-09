# kubelogs

Lightweight log management for Kubernetes.

kubelogs is a single-binary log aggregator built for solo developers and small platform teams who want to understand what's happening in their cluster without deploying a complex observability stack.

## Why kubelogs?

| Problem | kubelogs |
|---------|----------|
| Loki/Grafana requires 5+ components | Single binary, one Helm install |
| ELK stack needs 4GB+ RAM | Runs on <256MB RAM |
| Learning PromQL/LogQL takes days | Click-to-filter UI, plain text search |
| Expensive SaaS pricing | Free and open source |

## Features

- **Zero-config Kubernetes native** — auto-discovers pods, deployments, namespaces
- **Tiny footprint** — runs comfortably on small clusters and budget VPS
- **Built-in UI** — no Grafana setup, no dashboard JSON imports
- **Plain text search** — find what you need without learning a query language
- **Kubernetes-aware** — understands pod restarts, OOMKills, crash loops
- **Scales when you need it** — embedded storage for small clusters, S3 for growth

## Quick Start

### Helm (recommended)

```bash
# Install from the charts directory
helm install kubelogs ./charts/kubelogs

# Or from OCI registry (coming soon)
helm install kubelogs oci://ghcr.io/kubelogs/charts/kubelogs
```

### Verify Installation

```bash
# Check that pods are running
kubectl get pods -l app.kubernetes.io/part-of=kubelogs

# View collector logs
kubectl logs -l app.kubernetes.io/name=collector

# View server logs
kubectl logs -l app.kubernetes.io/name=server
```

## Helm Configuration

kubelogs works out of the box with sensible defaults. For customization, create a `values.yaml`:

```yaml
global:
  imageRegistry: ghcr.io
  imageOwner: kubelogs

# Server: centralized storage service
server:
  enabled: true
  persistence:
    enabled: true
    size: 10Gi
  resources:
    requests:
      memory: 128Mi
      cpu: 100m
    limits:
      memory: 512Mi
      cpu: 1000m

# Collector: DaemonSet on each node
collector:
  enabled: true
  env:
    excludeNamespaces: "kube-system"  # namespaces to skip
    maxStreams: 100                    # concurrent log streams per node
    batchSize: 500                     # entries before flush
  resources:
    requests:
      memory: 64Mi
      cpu: 100m
    limits:
      memory: 256Mi
      cpu: 500m
```

```bash
helm install kubelogs ./charts/kubelogs -f values.yaml
```

### Common Configurations

**Collect logs from all namespaces (including kube-system):**
```bash
helm install kubelogs ./charts/kubelogs \
  --set collector.env.excludeNamespaces=""
```

**Only collect from specific namespaces:**
```bash
helm install kubelogs ./charts/kubelogs \
  --set collector.env.includeNamespaces="default,production"
```

**Run on all nodes (including control plane):**
```bash
helm install kubelogs ./charts/kubelogs \
  --set collector.tolerations[0].operator=Exists
```

**Standalone mode (local SQLite, no server):**
```bash
helm install kubelogs ./charts/kubelogs \
  --set server.enabled=false \
  --set collector.standaloneMode=true
```

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                    │
├───────────────────────────────────────────────────────────┤
│                                                           │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐       │
│  │  Collector  │  │  Collector  │  │  Collector  │       │
│  │ (DaemonSet) │  │ (DaemonSet) │  │ (DaemonSet) │       │
│  │   Node 1    │  │   Node 2    │  │   Node 3    │       │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘       │
│         │                │                │               │
│         └────────────────┼────────────────┘               │
│                          │ gRPC                           │
│                          ▼                                │
│                   ┌─────────────┐                         │
│                   │   Server    │                         │
│                   │ (Deployment)│                         │
│                   │  + SQLite   │                         │
│                   └─────────────┘                         │
│                                                           │
└───────────────────────────────────────────────────────────┘
```

**Collector** (DaemonSet): Runs on every node, tails container logs via Kubernetes API, and streams them to the server over gRPC.

**Server** (Deployment): Centralized storage service with embedded SQLite and full-text search. Receives logs from all collectors.

## Resource Usage

| Cluster Size | Memory | CPU | Storage |
|--------------|--------|-----|---------|
| Small (<10 pods) | 128MB | 0.1 | embedded |
| Medium (<100 pods) | 256MB | 0.3 | embedded |
| Large (100+ pods) | 512MB | 0.5 | S3 |

## Roadmap

- [x] Core log collection
- [x] Embedded storage
- [x] Basic web UI
- [ ] Full-text search
- [ ] Alerting (webhooks)
- [ ] Slack/PagerDuty integrations
- [ ] Multi-cluster support
- [ ] S3 storage backend
- [ ] RBAC

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting a PR.

```bash
# Clone the repo
git clone https://github.com/kubelogs/kubelogs.git
cd kubelogs

# Run locally
make dev

# Run tests
make test
```

## Community

- [GitHub Discussions](https://github.com/kubelogs/kubelogs/discussions) — questions and ideas
- [Discord](https://discord.gg/kubelogs) — chat with the community
- [Twitter](https://twitter.com/kubelogsdev) — updates and announcements

## License

BSL 1.1 — see [LICENSE](LICENSE) for details.

---

Built with frustration by developers who just wanted to see their logs.
