# Gleann — Security & Quality Audit (October 2025)

## Scope
Comprehensive security, dead-code, documentation, and code-quality audit of the gleann repository — a foundational GraphRAG/memory engine consumed by yaver-go and se-agent.

## Toolchain
- `govulncheck` (golang.org/x/vuln) — CVE detection (callable paths only)
- `staticcheck` (honnef.co/go/tools) — lint + bug patterns
- `deadcode` (golang.org/x/tools) — unreachable function detection
- `cyclonedx-gomod` v1.10.0 — SBOM generation (CycloneDX 1.5)
- `go vet` / `go build` — compile-time correctness

## Results

### CVE Status
- **Before:** 19 callable stdlib CVEs (Go 1.25.0)
- **After:** 0 callable vulnerabilities
- **Action:** Bumped `go` directive to `1.25.9` (closes ~20 stdlib CVEs across `archive/tar`, `crypto/x509`, `database/sql`, `net/http`, `os`, `syscall`)
- **Report:** [security/govulncheck.txt](security/govulncheck.txt)

### SBOM
- Generated: [security/sbom.cdx.json](security/sbom.cdx.json) (CycloneDX 1.5, ~87 KB)
- Format suitable for upload to OWASP Dependency-Track, GitHub Dependency Graph, etc.

### Real Bugs Fixed
1. `pkg/gleann/chat.go` — unchecked `json.Marshal` errors in OpenAI + Anthropic
   request paths (silent failure: bad request body sent if marshal failed).
2. `internal/multimodal/pdf_vision.go` — replaced manual prefix/suffix slicing
   with `strings.TrimPrefix` / `strings.TrimSuffix` (was off-by-one safe but
   fragile).

### Code Quality Improvements
- Removed dead code: `openCodeJSON` constant, `mockCovComputer` test helper,
  unused `panelW` UI variable.
- Replaced deprecated `strings.Title` with internal `titleASCII` helper in
  `internal/server/a2a_handler.go`.
- Fixed nil-deref risk in `cmd/gleann/install_test.go` (`continue` on nil entry).
- Lower-cased Ollama error string per Go style (errors should not be
  capitalized).
- Boolean simplification in `pkg/gleann/filter.go`.

### Dead Code (Intentional)
The `deadcode` tool flagged ~30 unreachable functions, primarily:
- A2A FSM helpers (`TaskFSMResume`, `TaskFSMGraph`) — public API for downstream
  consumers (yaver-go, se-agent) that aren't called inside gleann itself.
- Background manager methods (`ActiveCount`, `NewTaskFSM`, etc.) — public API.
- Event bus `Subscribe` — exported for plugin authors.

These are deliberate public-API surface and should not be removed.

### Documentation Status
- `SECURITY.md` rewritten with threat model, supply-chain section, and
  hardening checklist.
- Existing docs (`docs/architecture.md`, `docs/graph.md`, `docs/api.md`,
  `docs/configuration.md`, `docs/cookbook.md`) reviewed — accurate as of audit
  date, no deletions needed.

## Test Status (post-audit)
| Suite | Result |
|-------|--------|
| Unit tests (`internal/...`, `pkg/...`, `cmd/...`) | ✅ all green |
| `tests/integration` | ✅ green (39.7s) |
| `tests/benchmarks` | ✅ green |

## Awesome-Go Readiness Checklist
- [x] Stable, semantic public API
- [x] Comprehensive README with quickstart
- [x] LICENSE present (MIT)
- [x] CI-friendly tests (`go test ./...` works without external deps in short mode)
- [x] `go.mod` with current Go version
- [x] SECURITY.md with disclosure policy
- [x] CONTRIBUTING.md
- [x] No callable CVEs
- [x] SBOM published

## Outstanding (Lower Priority)
- ~21 remaining `staticcheck` findings, mostly U1000 (unused fields under
  build-tagged code paths and test-only struct fields). False-positive heavy;
  manual review needed before suppressing.
- Coverage measurement not yet centralized — recommend adding `make coverage`
  target with `go test -coverprofile=coverage.out ./...`.
