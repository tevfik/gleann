# Changelog

All notable changes to this project will be documented in this file.

## [v1.8.0] — 2026-09-25

### Added
- **Tiered Persistent Agent Memory**: Multi-tiered memory architecture (`short`, `medium`, `long`) backed by embedded BoltDB with auto-scope hierarchies (`project:{name}`, `session:{id}`, `global`), TTL expiration, and tier promotion.
- **Sleep-Time Engine**: Background reflection daemon that extracts entities and relationships from agent conversations and manages memory compaction.
- **Unified Memory API**: `/api/memory/recall` and `/api/memory/ingest` combining block memory, KùzuDB knowledge graph traversal, and vector search.
- **Index Governance & Target Isolation**:
  - Strict client target isolation across all REST API handlers (`/api/indexes/{name}/*`, `/api/search`, `/api/memory/*`, `/api/blocks/*`) using `X-Gleann-Target` / `X-Gleann-Index` headers and `?target=` parameters.
  - Automatic exclusion of private indexes (`mcp_exposed: false`) and untagged indexes from multi-search unless explicitly authorized.
  - Tag-based governance via `GLEANN_TAGS` and federated `@tag` search expansion.
- **Google A2A Protocol**: Agent discovery (`/.well-known/agent.json`), JSON-RPC message delivery, and targeted skills with parameter isolation.
- **MCP Tool Profiles**: Configurable profiles (`minimal`, `standard`, `full`, `memory-only`) via `--tools` flag.
- **Incremental Synchronization (`gleann_sync`)**: MCP and CLI tool to incrementally update both vector passages and AST knowledge graphs on code changes.

### Fixed
- **Client Target Isolation in Memory Blocks**: Enforced `?target=` parameter alias for `?scope=` in `/api/blocks/context`, `/api/blocks/search`, `/api/blocks`, and `/api/blocks` POST with mismatch rejection (403 Forbidden).
- **Cross-Platform Compatibility**: Fixed Windows file path delimiter issues (`filepath.Join` vs URL paths), macOS symlink resolution in file indexing, and BoltDB timeout handling on Linux.
- **AST and Treesitter Robustness**: Enhanced language parsers, FQN symbol matching in KùzuDB, and markdown/doc comment sanitization.

---

## [v1.1.0] — 2026-09-14

### Added
- **Web UI Architecture**: Completely modularized React frontend into clean, maintainable components (`ChatView`, `GraphView`, `MemoryView`, `TasksView`, `IndexesView`, `SettingsModal`).
- **Memory Scope Generics**: Clean generic hierarchical scoping patterns across memory engine and documentation.

### Fixed
- **Windows CI & Path Compatibility**: Resolved `models_handler_test` failure on Windows by honoring `USERPROFILE` path resolution.
- **Race Condition Handling**: Disabled Windows `-race` flag in CI to prevent TSAN crashes with CGO.
- **Graph CSV Import Crash**: Kuzu DB'de doc comment'lerdeki `"`, `,`, `\`, `\n` karakterleri nedeniyle STRING→DOUBLE cast hatası düzeltildi (`sanitizeCSVField()` eklendi).
- **TUI Test Timeout**: 200+ TUI testi 90sn timeout'u aşması `TestMain` ve test modu ile çözüldü.

### Stats
- **Coverage**: %59.6 total statements (26 packages, 0 fail)
- **Graph Index**: 8080 nodes, 22037 edges, 789 communities
- **Unit Tests**: Gleann Core: **26 packages**, all passing

---

## [v1.0.0] — 2026-06-28

### Added
- **A2A Protocol** (Google Agent-to-Agent standard)
  - `/.well-known/agent-card.json` agent discovery endpoint
  - `/a2a/v1/message:send` task submission
  - `/a2a/v1/tasks/{id}` task status polling
  - 8 skill exposed (Semantic Search, RAG Q&A, Code Graph, Memory, Community Detection, Repo Map, Risk Analysis, Multimodal)
- **Memory Engine** — Hierarchical path-style scope with ancestor visibility
- **Context Field Theory (Φ Scoring)** — MCP search re-ranking with recency decay, frequency, graph proximity, degree centrality
- **10 File Read Modes** — `map`, `signatures`, `entropy`, `diff`, `task`, `reference`, `aggressive`, `lines:N`, `auto`, `full`
- **Shell Output Compression** — 95+ tool-specific regex patterns
- **17 Agent Platform Support** — `gleann install` auto-configures OpenCode, Claude Code, Cursor, Codex, Gemini CLI, Windsurf, Cline/Roo, Amp, Kiro, Amazon Q, Continue, Zed, Neovim, JetBrains, OpenClaw, Aider, GitHub Copilot CLI
- **Token Gain Tracking** — `gleann_gain` MCP tool for cumulative session savings
- **Embedding Cache** — Two-tier (L1: otter in-memory ≤50k; L2: disk keyed by SHA-256)

### Changed
- Centralized LLM model defaults, removed hardcoded llama3.2 references
- Standardized plugin extraction benchmarks
- Unified Rust/Candle native embedding engine build system
- Consolidated commands, reduced cyclomatic complexity

### Security
- Bumped Go to 1.25.9, added SBOM, hardened code
- SSRF-safe webhooks, request body cap, configurable task auto-eviction
- Fixed 7 stability bugs (memory lock, Kuzu corruption, dedupe, num_ctx, timeouts, multimodal, TUI-TTY)

### Performance
- Single tree-sitter parse + content-hash cache + language-aware weights
- DiskANN backend with optional FAISS CGo acceleration
- VectorSyncer bridge + IMPLEMENTS/REFERENCES edge extraction

---

## Historical Highlights

| Commit | Description |
|--------|-------------|
| `d518721` | TUI sandbox test mode (precursor to TestMain fix) |
| `6d19e52` | 7 stability bug fixes (memory lock, kuzu corruption, etc.) |
| `73285ce` | Shell compression, 10 read modes, Context Field Theory |
| `b2e1b55` | Native embedding engine (Rust/Candle) integration |
| `e3f450a` | Go 1.25.9 bump, SBOM, security hardening |

---

## Versioning

Gleann follows [Semantic Versioning](https://semver.org/):
- **Major** — Breaking API/config changes
- **Minor** — New features (backward compatible)
- **Patch** — Bug fixes and performance improvements
