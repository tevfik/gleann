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

- **Unit tests**: Fast, no external dependencies. Run with `make test`.
- **E2E tests**: Require Ollama running. Run with `make test-e2e`.
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
