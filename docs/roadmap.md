# Roadmap

## Completed Features

### Core RAG Engine
- [x] Pure-Go HNSW vector index (CSR format, selective recomputation)
- [x] Optional FAISS backend (CGo, AVX2 SIMD)
- [x] Sentence-aware text chunking
- [x] AST-aware code chunking (Tree-sitter)
- [x] BM25 hybrid search (vector + keyword)
- [x] Two-stage reranking
- [x] Incremental indexing (file sync)
- [x] `.gleannignore` — gitignore-style build exclusions

### LLM Chat & Conversations
- [x] RAG-based Q&A (`gleann ask`)
- [x] Interactive chat mode (`--interactive`)
- [x] Streaming responses (token-by-token)
- [x] Multi-index chat — ask across multiple indexes (`gleann ask idx1,idx2 "question"`)
- [x] Conversation persistence — auto-save and resume with `--continue` / `--continue-last`
- [x] Conversation management — `gleann chat --list / --show / --delete / --delete-older-than`
- [x] Named roles — `--role code`, `--role shell`, `--role explain`, custom roles in config
- [x] Output format control — `--format json`, `--format markdown`, `--format raw`
- [x] Config-driven roles & format-text — custom definitions in `~/.gleann/config.json`
- [x] Stdin/pipe support — `cat file.go | gleann ask my-code "Review this"`
- [x] Raw mode — `--raw` flag, auto-enabled when stdout is piped
- [x] Quiet mode — `--quiet` suppresses status messages (for scripting)
- [x] Markdown rendering — glamour-based terminal markdown in CLI ask and TUI chat
- [x] `--no-cache` flag — skip conversation save for ephemeral queries
- [x] `--no-limit` flag — remove token limit for unlimited output

### Code Intelligence
- [x] AST Graph Indexer (KuzuDB)
- [x] Graph-Augmented Search (callers/callees enrichment)
- [x] Impact Analysis (BFS blast radius)
- [x] Incremental graph updates

### Infrastructure
- [x] REST API server (`gleann serve`)
- [x] OpenAPI/Swagger spec & interactive docs
- [x] SSE streaming for ask endpoint
- [x] Multi-index search endpoint
- [x] MCP server (stdio, for AI editors)
- [x] Interactive TUI (Bubble Tea)
- [x] Setup wizard (`gleann setup` / `gleann tui`)
- [x] Docker image (pure-Go + full with tree-sitter)
- [x] Prometheus/OpenTelemetry metrics
- [x] Webhook notifications
- [x] Plugin architecture (PDF, audio extraction)
- [x] `gleann config` subcommand (show/path/edit/validate)
- [x] Backward compat aliases removed — clean `gleann index *` only
- [x] Graph+vector watch sync — incremental graph updates with changed files

## Planned

- [x] Word-wrap control (`--word-wrap N`)
- [x] Conversation titles via LLM auto-summarization
- [x] REST API: ask with role & conversation_id fields
- [x] REST API: conversation endpoints (list, get, delete)
- [x] TUI: conversation browser panel (`/history` command)
- [x] TUI: role selector in settings
- [x] Embedding cache (avoid recomputing unchanged chunks)
- [ ] DiskANN backend
- [ ] Agent mode: multi-step ReAct with tool use

