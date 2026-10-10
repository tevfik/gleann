# Changelog

All notable changes to this project will be documented in this file.

## [v1.8.3] — 2026-10-10

### Added
- **Dual Embedding Model Architecture (Code + Docs Specialization)**:
  - Added `--code-model` and `--doc-model` (alias: `--docs-model`) CLI flags and `code_embedding_model` / `doc_embedding_model` configuration fields.
  - Automated dual-stage indexing: running `gleann index build <name> --docs <dir> --code-model <m1> --doc-model <m2>` automatically builds `<name>-code` and `<name>-docs` with their respective specialized models in a single command.
  - Automatic Dual-Index Expansion: querying `gleann search <name>` or `gleann ask <name>` transparently routes queries across both `<name>-code` and `<name>-docs` via `MultiSearcher`.
- **Per-Index Dynamic Embedder Resolution (`EmbedderResolver`)**:
  - Implemented `EmbedderResolver` across `pkg/gleann`, `internal/embedding`, CLI, and REST server.
  - In multi-index search, queries against each index are dynamically embedded using that index's native model (`meta.EmbeddingModel`), completely eliminating latent vector space mismatch and dimension mismatch panics across indices with different dimensions (e.g., 768-dim Jina Code + 384-dim BGE Small).
- **Web UI & REST API Dual Model Settings**:
  - Added dedicated Code Embedding Model and Docs Embedding Model fields to Web UI System Settings (`ui/src/components/System.tsx`).
  - Updated `/api/config` GET/POST endpoints and server searcher cache to support multi-model indexes without false model mismatch errors.
- **In-Process EIF-Runtime LLM & ALiBi Support**:
  - Submodule updated to latest `ext/eif-runtime` with ALiBi double LayerNorm support, enabling Jina Embeddings v2 Base Code (~93 ms/emb on CPU).
  - Added C99 in-process LLM generation and real-time token streaming callbacks (`pkg/gleann/chat_eif.go`).

### Fixed
- **CI/CD Toolchain Compatibility**:
  - Added `GOTOOLCHAIN=auto` across `.github/workflows/ci.yml` and `.github/workflows/release.yml` to prevent Go toolchain version mismatch failures.
  - Cleaned up mock structs in MCP token budget tests, passing 100% of `staticcheck`, `go vet`, and race-detector test suites.

## [v1.8.2] — 2026-10-07

### Added
- **In-Process EIF-Runtime Integration**:
  - Embedded high-performance C99 `eif-runtime` engine as a Git submodule (`ext/eif-runtime`) compiled directly into `gleann-full` standalone binaries.
  - Zero-heap, zero-copy `mmap` CPU inference achieving 450+ embeddings/sec for MiniLM and 90+ embeddings/sec for 12-layer models.
  - Added native EIF provider support (`ProviderEIF = "eif"`) across TUI onboarding wizard, model scanner, CLI indexing, and Go embedding computers.
  - Supported direct loading of both `.eifm` binary models and GGUF (`.gguf`, `_q8_0.gguf`) BERT-family models without external Python or server dependencies.
- **Multimodal Chat & Screenshot Capture**:
  - Added `/screenshot` (and `/shot`, `/paste-image`) slash commands to interactive TUI chat.
  - Automatic clipboard image capture via `wl-paste` (Wayland), `xclip` (X11), `pngpaste` (macOS), or PowerShell (Windows) with automatic screen capture fallback.
  - Queued images are formatted and sent seamlessly alongside user prompts to vision/multimodal models (Ollama, OpenAI, Anthropic).
- **Repetition Penalty Configuration**:
  - Added configurable repetition penalty across CLI flags (`--repeat-penalty`, `--repetition-penalty`), environment variables (`GLEANN_REPEAT_PENALTY`), TUI settings slider, and interactive slash commands (`/repeat <val>`, `/penalty <val>`).
  - Seamlessly propagated across Ollama (`options.repeat_penalty`) and OpenAI/llama.cpp APIs.

### Fixed
- **EIF-Runtime BERT Engine Robustness**:
  - Added support for quantized `Q8_0` position embeddings (`position_embd.weight`) in GGUF models with on-the-fly FP32 dequantization, fixing NaN generation in models such as Multilingual-E5.
  - Added dynamic vocabulary hash table allocation (up to 512K slots) to support large multilingual models (250K+ tokens) without collision drops.
  - Added detection and mapping for SentencePiece / LLaMA special tokens (`<s>`, `</s>`, `<unk>`, `<pad>`) via GGUF metadata (`tokenizer.ggml.bos_token_id` / `eos_token_id`).
- **TUI Settings Alignment**:
  - Positioned Repetition Penalty slider directly below Temperature in TUI Settings menu for intuitive keyboard navigation.

## [v1.8.1] — 2026-10-06

### Fixed
- **Embedded Llama.cpp Runner & Inference**:
  - Automatically configured `--embedding` flag when spawning embedded `llama-server` process to enable native embedding inference.
  - Tuned default CPU batch size (`32`) and concurrency (`2`) for local llama.cpp embeddings to prevent memory thrashing and CPU thread contention.
  - Added robust response parsing for OpenAI-compatible embedding servers returning all zero indices (e.g. `llama-server`).
  - Unified base URL routing and API key propagation across CLI commands, REST server, MCP server, and interactive TUI chat.
- **Test Suite Isolation**:
  - Fixed test cleanup in MCP remote routing tests to prevent race conditions during full concurrent test runs.
  - Consolidated embedder computer initialization (`newEmbedder`) across `cmd/gleann` commands, eliminating duplicate code and unused imports.

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
- **Graph CSV Import Crash**: Resolved STRING→DOUBLE cast errors in Kuzu DB caused by special characters (`"`, `,`, `\`, `\n`) in doc comments (added `sanitizeCSVField()`).
- **TUI Test Timeout**: Resolved 200+ TUI tests exceeding 90s timeout by introducing `TestMain` and dedicated test mode.

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
