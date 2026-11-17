# Contributing to Gleann

Thank you for your interest in contributing to Gleann! This document provides guidelines for contributing.

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR_USERNAME/gleann.git`
3. Create a feature branch: `git checkout -b feature/my-feature`
4. Make your changes
5. Run tests: `make test`
6. Commit and push
7. Open a Pull Request

## Development Setup

```bash
# Build (pure Go)
make build

# Build with tree-sitter support (CGo)
make build-cgo

# Run tests
make test

# Run all pre-commit checks (vet + test)
make vet && make test

# Run linter
go vet ./...
```

## Go Modules Layout

`go install github.com/tevfik/gleann/cmd/gleann@latest` only works if the root
`go.mod` has no `replace` directives. For that reason:

- `modules/bm25`, `modules/chunking`, `modules/diskann` and `modules/hnsw` are
  ordinary packages of the root module (no `go.mod` of their own). Edit and test
  them like any other package (`go test ./modules/...`).
- `modules/settings` is a separate module because the plugin repositories import
  it on its own. The root `go.mod` requires a published pseudo-version of it.
  To change it: edit `modules/settings`, merge to `main` and push, then bump the
  root requirement with
  `go get github.com/tevfik/gleann/modules/settings@<commit>` followed by
  `go mod tidy`. Until the bump, the root module keeps building against the
  previously published version, not your local edit.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use table-driven tests with `t.Run()` subtests
- Keep functions focused and under ~100 lines where possible
- Add doc comments to exported identifiers

## Testing

All tests live under `tests/`:

```
tests/
  integration/   — Go integration tests (mock embedder, vault, plugins)
  benchmarks/    — Performance benchmarks (HNSW, recall, FAISS vs Go)
  e2e/           — Full end-to-end bash suite + fixtures + benchmark mode
    run.sh       — Main e2e test runner (77+ checks)
    fixtures/    — Deterministic test documents (MD, code, binary)
```

- **Unit tests**: Co-located with source (`*_test.go`). Run with `make test`.
- **Integration tests**: `make test-integration`.
- **Full E2E suite**: `make test-e2e` (requires Ollama + markitdown).
- **Go benchmarks**: `go test -bench=. ./...`.
- Write tests for all new functionality.
- Aim for >80% coverage on new code.

## Pull Request Guidelines

- Keep PRs focused on a single change
- Include tests for new functionality
- Update documentation if behavior changes
- Ensure `make vet && make test` pass before submitting

## Reporting Issues

- Use GitHub Issues for bug reports and feature requests
- Include Go version, OS, and reproduction steps for bugs
- Search existing issues before creating a new one

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
