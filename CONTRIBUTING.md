# Contributing to Lifecycle

Thank you for your interest in contributing! Here's how to get started.

## Development Setup

### Prerequisites

| Tool | Purpose | Install |
|------|---------|---------|
| [Go 1.24+](https://go.dev/) | Runtime | Your package manager or [golang.org](https://go.dev/dl/) |
| [just](https://just.systems/) | Command runner | `cargo install just` or `brew install just` |
| [mise](https://mise.jdx.dev/) | Tool version manager | `curl https://mise.jdx.dev/install.sh \| sh` |
| [golangci-lint](https://golangci-lint.run/) | Linter | `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest` |

### Getting Started

```bash
git clone https://github.com/mschulkind/lifecycle.git
cd lifecycle
mise install        # Install tool versions
go mod download     # Fetch Go dependencies
just check          # Format, lint, and test — verify everything works
```

### Running Tests

```bash
just check    # Format, lint, and test — run this before every PR
just test     # All tests
just test-cov # Tests with coverage report
```

## Making Changes

### Coding Standards

- Follow `gofmt` formatting (enforced by `just format`)
- Pass `golangci-lint` with no warnings
- Write tests for new functionality
- Keep packages focused — business logic in `internal/`, CLI in `cmd/`

### Code Quality

Always run `just check` before submitting. It runs:
- `gofmt` — Go formatting
- `goimports` — Import organization
- `golangci-lint` — Linting
- `go test` — All tests

### Commit Messages

Use conventional commit style:

```
feat: add iteration scoring to cycle engine
fix: SQLite connection leak on concurrent queries
docs: update CLI reference for serve command
```

## Versioning

Lifecycle follows [Semantic Versioning](https://semver.org/):

- **MAJOR** (x.0.0) — breaking changes to CLI, config format, or API
- **MINOR** (0.x.0) — new features, backward-compatible
- **PATCH** (0.0.x) — bug fixes, documentation, internal improvements

While in 0.x.y, the API is not considered stable and minor versions may include breaking changes.

## Pull Request Process

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes with tests
4. Run `just check` and ensure everything passes
5. Submit a PR with a clear description of what and why

### What Makes a Good PR

- **Small and focused** — one logical change per PR
- **Tested** — new features have tests, bug fixes include regression tests
- **Documented** — update docs if behavior changes

## Bug Reports

Please include:
- Steps to reproduce
- Expected vs actual behavior
- Lifecycle version (`lifecycle --version`)
- OS and Go version

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
