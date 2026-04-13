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

# Run all pre-commit checks (vet + test + e2e)
make pre-commit

# Run linter
go vet ./...
```

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
- **Integration tests**: `make test-e2e` (requires Ollama + markitdown).
- **Full E2E suite**: `make test-e2e-full` (requires gleann-full binary).
- **Benchmark scoring**: `make test-benchmark` (outputs JSON with weak point detection).
- **Go benchmarks**: `make test-bench` (HNSW insert/search/recall).
- Write tests for all new functionality.
- Aim for >80% coverage on new code.

## Pull Request Guidelines

- Keep PRs focused on a single change
- Include tests for new functionality
- Update documentation if behavior changes
- Ensure `make pre-commit` passes before submitting

## Reporting Issues

- Use GitHub Issues for bug reports and feature requests
- Include Go version, OS, and reproduction steps for bugs
- Search existing issues before creating a new one

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
