# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

kubelogs is a lightweight, single-binary log aggregator for Kubernetes. It's designed for solo developers and small platform teams who need log visibility without deploying complex observability stacks like Loki/Grafana or ELK.

## Build Commands

```bash
make dev      # Run locally
make test     # Run tests
```

## Architecture

Three main components in a single binary:

- **Collector** (DaemonSet): Tails container logs via Kubernetes API, accepts OTLP and Fluent Forward protocols
- **Store**: Embedded SQLite with FTS5 full-text search
- **Web UI**: Built-in search/filter interface, no external dependencies

## Key Design Constraints

- Must run on <256MB RAM
- Zero-config Kubernetes native with auto-discovery of pods, deployments, namespaces
- Kubernetes-aware: understands pod restarts, OOMKills, crash loops
- Plain text search (no query language like PromQL/LogQL)
