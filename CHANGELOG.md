# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [v1.8.7] — 2026-10-19

### Added
- **Chat settings in the agenttui interface** (`/settings`, Ctrl+S): model, temperature (slider), repeat penalty, max tokens, top-k, reranker and model, role and system prompt, with visible limits and hints. `/history` resumes a saved conversation; `/clear` and sources-first answers; the header, sidebar and welcome screen are gleann-specific.
- **`gleann chat` runs on the agenttui interface by default** (cancellable streaming, collapsible reasoning, source cards, slash-command popup, `@file` and `@symbol` completion). `--classic` (or `GLEANN_CHAT_UI=classic`) selects the old interface; `--modular` is still accepted and does nothing.
- **Index and tag picker in chat.** `/index` opens a panel with one switch per index and per tag (`gleann index tag`); ticking a tag selects every index that carries it. Also `/index a,b`, `/index #tag`, `/index add|remove <name>`, `/index info`. The selection really changes what is retrieved (a new searcher is swapped in and the reranker is restored); before, `/index` only changed the label.
- **Retrieval sidebar** lists every index with passage count, graph marker and tags, which ones are searched, and how many passages of the last answer came from each.
- `@symbol` completion works over a multi-index search (first index with a code graph); sources of a multi-index answer show which index they came from.
- `/forget` asks for confirmation before deleting memories.
- `gleann.ChatContextWindow` asks the provider for the model's context window (Ollama `num_ctx` sent / Modelfile / loaded model, OpenAI-compatible `/models` fields). `GLEANN_CONTEXT_TOKENS` overrides it.

### Changed
- The `ext/eif-runtime` submodule is replaced by `ext/bitllm.c` (updated to `cef6d8a`); the embedded web UI is rebuilt (provider select fix). Requires `git submodule update --init --recursive`.
- The chat no longer guesses the context window from the model name; when the provider does not report one the gauge shows used tokens only.

## [v1.8.6] — 2026-10-15

### Added
- **Optional Ollama API key (`ollama_api_key` / `OLLAMA_API_KEY`)** for Ollama behind an auth proxy or hosted Ollama endpoints:
  - New config field `ollama_api_key`; when set (or `OLLAMA_API_KEY` is exported) every Ollama request carries `Authorization: Bearer <key>` — chat, streaming chat, embeddings, Ollama reranker, `/api/tags` model listing (TUI, Web UI proxy, `gleann doctor`, `gleann config validate`), multimodal (`/api/show`, `/api/chat`), auto-setup detection and `/api/pull`, conversation/memory summarizers.
  - CLI: `gleann config set ollama_api_key <key>` (aliases `ollama-api-key`, `ollama_key`); `--ollama-api-key` flag for build/search/ask/chat/serve/multimodal and `gleann setup --bootstrap|--auto`.
  - Setup wizard: optional masked "Ollama API Key" step after the Ollama URL (embedding and LLM; one shared value, Enter skips); shown masked in the summary and flagged as `(key)` in the settings menu.
  - Web UI: the API Key field is enabled for the `ollama` provider (bound to `ollama_api_key`, placeholder "Optional") and model listing passes the provider-specific key. Requires a UI rebuild (`internal/server/dist`).
  - `gleann doctor` reports "API key configured" / "no API key" for Ollama and hints when Ollama answers 401/403.
- **`gleann index repair <name>|--all [--check] [--force]`** checks an index for damage and rebuilds its vector index from the passages it already stores (no source files needed; embeddings come from the cache where the texts are unchanged). It repairs indexes that older versions left damaged by in-place updates (see Fixed), a missing vector file and a stale passage count. `--check` only reports and exits 1 when an index needs repair; `--force` rebuilds even when the check finds nothing. `gleann doctor` now flags such indexes under "Indexes" and says which command fixes them.
- **Update statistics.** `LeannBuilder.UpdateIndexWithStats` reports how an update was applied (`in-place`, `compact`, `emptied`, `noop`) and what it cost: passages added/removed, texts embedded, embedding time, total time. `gleann index sync` prints it (`↳ vector index compacted: -2 +1 passages (now 6), 6 text(s) re-embedded in 120ms`) and returns it in `SyncResult.Update`; `gleann index repair` reports passages, texts embedded and the embedding cache hit rate; `POST /api/indexes/{name}/update` returns `mode`, `passages`, `removed` and `embedded_texts`.
- **Searching a damaged index warns.** Loading an index whose passage IDs have gaps (see Fixed) logs a warning that names the fix (`gleann index repair <name>`), `LeannSearcher.Warnings()` returns it, `POST /api/indexes/{name}/search` includes it as `warnings`, and the MCP search tool prints it above the results so an agent does not trust them blindly. The check reads only the passage store's key count and last key, so it costs nothing per load.
- `gleann index repair` shows progress while passages are re-embedded (`--progress plain|json|none`, a line every couple of seconds; a warm embedding cache finishes before the first one).
- `gleann doctor` summarises every index on one line: passages, backend, embedding model, dimensions (or "lexical-only") and size on disk.
- **New `/metrics`**: `gleann_update_requests_total{mode}`, `gleann_update_errors_total`, `gleann_update_latency_avg_ms`, `gleann_update_embedded_texts_total`.

### Changed
- `RemoveVectors` is gone from the vector backend interfaces (`gleann.BackendBuilder`, `modules/hnsw`, `modules/diskann`, `pkg/backends`, FAISS). Removing vectors in place is what damaged indexes: vectors are addressed by position, so removing one moves every later vector onto the wrong passage. Removals now go through `UpdateIndex`, which rebuilds from the remaining passages. Out-of-tree backends that implement `RemoveVectors` can simply drop it.

### Fixed
- Embedding progress was never reported during an index build: `embedding.ProgressCallback` was a defined func type, so the embedders' `SetProgressCallback` did not satisfy the `SetProgressCallback(func(int, int))` interface the index builder looks for. It is an alias now, so `--progress` and the MCP build progress see embedding batches.
- The API key is now selected per provider: an `openai_api_key` is no longer sent to Ollama endpoints (CLI ask/chat, MCP ask/batch_ask, TUI chat, server ask/search/proxy, embeddings, reranker).
- `gleann config show` and `gleann config set` mask API keys (Ollama, OpenAI, Anthropic) instead of printing them in clear text.
- `--api-key` value is no longer mistaken for the question/index name in `gleann ask`.
- `gleann install --user` no longer falls back to `sudo` (and fails) when the target directory does not exist yet, e.g. a fresh home without `~/.local`.
- **`gleann install --user/--system` installed an unusable binary.** Run through the self-extracting `gleann-full` launcher, it copied the extracted `gleann-full-bin` (the `--system` path copied only `libfaiss*` next to it), which fails with `error while loading shared libraries: libgleann_core_rs.so`. The launcher now exports `GLEANN_LAUNCHER` and install copies that self-contained file instead; without it the previous behaviour stays and the sudo path copies every bundled library. `gleann help` now lists `install`/`uninstall`.
- `DELETE /api/indexes/{name}` no longer fails on Windows while searches still hold the index open (it retries for up to 5 s, dropping any re-loaded searcher).
- **Incremental index updates no longer corrupt the index.** Removing passages in place (a changed, deleted or renamed file in `gleann index sync`/watch, `UpdateIndex`, `Reconcile`, memory-entity delete) left permanent gaps in the passage IDs while the vector backends address a vector by position, so vectors ended up attached to the wrong passages: searches returned the wrong text or far fewer results than a fresh build. `UpdateIndex` now appends in place only when IDs are dense and the backend can take the add; otherwise it rebuilds the index from the remaining passages plus the new items (embeddings are computed before any file changes, so a failure leaves the index untouched). This also repairs indexes already left with gaps by older versions on their next update. Removing the last passage empties the index; a later add builds it afresh.
- `gleann index sync` edited the vector file with the backend named by the caller's flag/default instead of the one the index was built with (any sync touching a file failed with `backend "" not registered`, or silently rewrote the index with another backend); it now uses the index's own backend.
- A full rebuild no longer drops an index's tags, description, MCP exposure, auto-watch flag and creation time.
- An HNSW index built with pruned embeddings is no longer edited in place (it cannot be); the update compacts instead.
- **`gleann install claude` installed every detected platform.** A bare platform name was ignored, so auto-detection ran and also configured gemini, copilot, etc. `gleann install <name>` now means `--platform <name>`.
- **`gleann install` for `claude` and `gemini` wrote into the current directory.** They now write the user-level `~/.claude/CLAUDE.md` and `~/.gemini/GEMINI.md` (plus the hook settings next to them); `--project` or `--dir` keeps the old project-level behaviour. `uninstall` follows the same rule.
- **Graph endpoints reported a missing index on builds without graph support.** On a binary built without `-tags treesitter` (pure-Go and EIF-only builds, including ARM64), graph requests answered `404 graph index "x" not found: graph database requires CGo ...`. They now answer `503` with a message that says the binary has no graph support and points to the full build.
- **Web UI could not show or keep the EIF provider.** The embedding and LLM provider selects had no `eif` option, so an `eif` config rendered as "Ollama" and picking a model from that list switched the active model to an Ollama one. The selects now offer `EIF (Embedded GGUF)` with the local GGUF picker, and show any provider they do not know instead of silently displaying the first option. Requires a UI rebuild (`internal/server/dist`).
- **False "Mismatch / rebuild" warnings in the Web UI.** The index list compared an index's model only against the general embedding model and by exact string. It now accepts the code and docs models (`current_embedding_models` in `GET /api/indexes`) and compares file names, as the searcher does.

## [v1.8.5] — 2026-10-14

### Added
- **Table-Aware Markdown Chunker (`modules/chunking`)**:
  - Implemented atomic markdown table detection and preservation during document chunking. Tables under the token limit remain intact in a single chunk with their headers.
  - Large tables exceeding chunk size are partitioned with replicated header rows across every sub-chunk, ensuring continuous tabular context for embedding and RAG.
  - Fixed slice bounds out-of-range regression (`[len:len-1]`) in heading parser.
- **Plugin Priority & Capability Routing (`pkg/gleann/plugin.go`)**:
  - Upgraded document extractor discovery to prioritize high-capability, native plugins (`Priority: 10`) over legacy fallbacks.
  - Full seamless integration with standalone native `gleann-plugin-docs` daemon.
- **Optional API Keys for OpenAI-compatible & Ollama endpoints**:
  - `Authorization: Bearer <key>` is sent only when a key is configured; empty keys are omitted so local/unauthenticated servers (Ollama, vLLM, LM Studio, LocalAI) work unchanged.
  - New `--api-key`, `--base-url` flags and `gleann config set api_key|base_url|ollama_host` aliases.
  - `gleann doctor` validates OpenAI-compatible and Anthropic providers without requiring `ollama pull`.
- **Single-step docs plugin install**: `gleann-plugin-docs` binary and Granite Docling BitNet model are fetched from GitHub release assets.

### Fixed
- Plugin install no longer fails with `'NoneType' object is not iterable` when `plugins.json` contains `{"plugins": null}`; registry is always loaded/written with a non-nil list.
- `gleann-plugin-docs` is installed via the native Go pipeline (catalog metadata corrected from Python).
- eif-runtime diagnostics now go to stderr, keeping stdout clean for JSON/pipes.

### Changed
- **Default LLM Model Standardization (`llama3.2:3b`)**:
  - Centralized default chat model to `llama3.2:3b` across CLI defaults (`cmd/gleann/defaults.go`), core chat engine (`pkg/gleann/chat.go`), and system diagnostics (`gleann doctor`).
  - `gleann doctor` checks now verify local LLM availability cleanly against configured providers.

### Documentation & Maintenance
- **Documentation Audit & Streamlining**:
  - Consolidated scattered integration test plans into `docs/TEST_PLAN.md` and removed redundant ephemeral test reports.
  - Synchronized plugin guides (`docs/plugins.md`, `docs/plugin-install-guide.md`) to reflect the standalone Go/C VLM architecture.
- **Refreshed Terminal Screencast**:
  - Updated VHS demo recording (`scripts/demo/gleann-demo.gif` & `scripts/demo/gleann-demo.mp4`) showcasing all-green system health, instant table indexing, and cross-session memory retrieval.

## [v1.8.4] — 2026-10-13

### Optimized
- **EIF-Runtime High-Performance Engine Upgrade (`ext/eif-runtime`)**:
  - Upgraded submodule to commit `bde2895` with full AVX2 / ARM NEON multi-tile vector GEMM kernels and Q8_0 4-token unrolled matrix multiplication.
  - Multi-Head Attention parallelism: uncapped thread allocation in `eif_bert_embed` and `bert_gemm_int8_gguf`, delivering up to 62% latency reduction across local GGUF models on multi-core CPUs.
  - Portable Binary Distribution: replaced host `-march=native` with portable `-mavx2 -mfma` architecture compilation, strictly preventing AVX-512 EVEX illegal instructions (`SIGILL`) on non-AVX512 host environments and Docker containers.
  - Host CPU Capability Pre-flight: added runtime probing in `internal/embedding` ensuring zero CGo illegal instruction panics on unsupported architectures.
  - Measured live inference latency across local backend models:
    - `snowflake-xs-q8_0`: 14.20 ms / embedding (62.4% faster)
    - `minilm.gguf`: 14.43 ms / embedding (59.9% faster)
    - `jina-code-q8_0`: 85.68 ms / embedding (39.5% faster)
    - `bge-m3-q8_0`: 237.98 ms / embedding (30.5% faster, down from 425 ms)
    - `bge-small-en-q8_0`: 40.08 ms / embedding (24.1% faster)
    - `granite-30m-q8_0`: 19.54 ms / embedding (13.7% faster)
  - Full semantic separation parity: preserved 100% mathematical vector clustering and code search accuracy.
  - In-process EIF embedding and causal LLM validation: verified with 100% pass rate in Gleann integration and end-to-end test suites.

### Added
- **Multi-Model Embedding Selection (Code & Docs)**:
  - Enabled dedicated code and documentation embedding model selection across TUI setup (`gleann setup` wizard and settings menu), CLI setup (`gleann config set code_model / doc_model`, `gleann setup --bootstrap`, `gleann setup --auto`), and Web UI (System settings and Directory indexing).
  - Seamless fallback to the primary embedding model when dedicated models are not specified.

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
