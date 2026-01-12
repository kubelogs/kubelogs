# Contributing to kubelogs

Thank you for your interest in contributing to kubelogs! This document provides guidelines for contributing to the project.

## Prerequisites

- Go 1.25.5 or later
- Docker (for container builds)
- kubectl and access to a Kubernetes cluster (for integration testing)

## Development Setup

1. Clone the repository:
   ```bash
   git clone https://github.com/your-org/kubelogs.git
   cd kubelogs
   ```

2. Run the collector locally:
   ```bash
   make dev
   ```

3. Or run the server locally:
   ```bash
   make dev-server
   ```

4. Verify your setup by running tests:
   ```bash
   make test
   ```

## Project Structure

```
kubelogs/
├── cmd/                 # Executable entry points
│   ├── collector/       # DaemonSet collector binary
│   ├── server/          # Server/storage binary
│   └── loadgen/         # Load generator utility
├── internal/            # Internal packages (not for external import)
│   ├── collector/       # Collector implementation
│   ├── server/          # Server implementation
│   ├── storage/         # Storage abstraction and implementations
│   └── web/             # Web UI templates and assets
├── api/proto/           # Protocol buffer definitions
├── build/               # Dockerfiles
└── docs/                # Technical documentation
```

For detailed architecture information, see the [docs/](docs/) directory.

## Code Style

- Follow standard Go idioms and conventions
- Use `log/slog` for structured logging
- Keep packages in `internal/` (they are not meant for external import)
- Match existing patterns in the codebase
- Keep changes focused and minimal

## Testing

- Write tests alongside your code in `*_test.go` files
- Use table-driven tests with `t.Run()` for subtests
- Run the full test suite before submitting:
  ```bash
  make test
  ```
  This runs tests with the race detector enabled.
- All tests must pass before a PR can be merged

## Commit Messages

Use conventional commit format:

- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation changes
- `chore:` for maintenance tasks
- `refactor:` for code refactoring
- `test:` for test additions/changes

Scope is optional but encouraged for clarity:
```
feat(collector): add logfmt parser support
fix(storage): handle connection timeout
docs: update architecture diagram
```

## Pull Requests

1. Branch from `main`
2. Keep PRs focused on a single change
3. Include tests for new functionality
4. Update documentation if your change affects user-facing behavior
5. Ensure all tests pass locally before pushing
6. Provide a clear description of what your PR does and why

## License

kubelogs is licensed under the Business Source License 1.1 (BSL 1.1). By contributing, you agree that your contributions will be licensed under the same terms. See [LICENSE](LICENSE) for details.
