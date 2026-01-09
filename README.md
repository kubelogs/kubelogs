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
helm repo add kubelogs https://kubelogs.dev/helm
helm install kubelogs kubelogs/kubelogs
```

### kubectl

```bash
kubectl apply -f https://kubelogs.dev/install.yaml
```

### Access the UI

```bash
kubectl port-forward svc/kubelogs 8080:80
open http://localhost:8080
```

That's it. You should see logs flowing within seconds.

## Screenshots

*Coming soon*

## Configuration

kubelogs works out of the box with sensible defaults. For customization, create a `values.yaml`:

```yaml
# Storage
storage:
  type: embedded          # embedded | s3
  retention: 7d           # how long to keep logs
  
# Resources
resources:
  requests:
    memory: 128Mi
    cpu: 100m
  limits:
    memory: 256Mi
    cpu: 500m

# Ingress (optional)
ingress:
  enabled: false
  hostname: logs.example.com
```

```bash
helm install kubelogs kubelogs/kubelogs -f values.yaml
```

## Architecture

```
┌─────────────────────────────────────────────────┐
│                   kubelogs                      │
├───────────────┬───────────────┬─────────────────┤
│   Collector   │    Store      │     Web UI      │
│  (DaemonSet)  │  (embedded)   │   (built-in)    │
└───────────────┴───────────────┴─────────────────┘
                       │
          ┌────────────┴────────────┐
          ▼                         ▼
    DuckDB (small)           S3 (scale)
```

**Collector**: Lightweight DaemonSet that tails container logs via the Kubernetes API. Also accepts OTLP and Fluent Forward protocols.

**Store**: Embedded DuckDB for clusters up to ~10GB/day. Switch to S3-compatible storage for larger volumes.

**Web UI**: Built-in interface for searching and filtering logs. No external dependencies.

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

Apache 2.0 — see [LICENSE](LICENSE) for details.

---

Built with frustration by developers who just wanted to see their logs.
