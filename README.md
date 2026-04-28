# gleann

[![Release](https://github.com/tevfik/gleann/actions/workflows/release.yml/badge.svg?event=push)](https://github.com/tevfik/gleann/actions/workflows/release.yml)

**A lightweight, brutally fast, and highly flexible AI/RAG workspace and autonomous agent framework built with Go. Inspired by the academic excellence of the Leann RAG backend, engineered for daily terminal use.**

> 🤖 **Note:** This project, including its documentation, was developed with the assistance of AI.
---

## The Story and Inspiration (Why Gleann?)

Gleann was born out of a personal need to automate daily engineering workflows and power smooth analysis of massive codebases and personal documents—all from the comfort of the terminal.

The core motivation for this project is the visionary [Leann](https://github.com/yichuan-w/LEANN) project. Leann is a remarkable academic work that introduced a high-performance RAG backend architecture designed for efficient indexing and retrieval. We owe a great debt to the original Leann authors for their groundbreaking approach to selective recomputation and vector retrieval.

While Leann provides a powerful RAG engine, it is primarily an academic backend. Deploying it typically requires a substantial Python/Node environment (taking up roughly 8.5 GB of space) and a complex set of dependencies. As an engineer who lives in the shell, I needed something more self-contained: an end-to-end assistant where the LLM, plugin system, and RAG storage operate as a single, zero-dependency unit.

Gleann was built as a lightweight, Go-native tribute to Leann’s vision.

By leveraging Go’s compiled, concurrent speed, I rebuilt the core RAG concepts into a compact architecture. On top of that foundation, I added an agent layer based on ReAct (Reasoning and Acting) logic and direct LLM integration.

The result is a highly portable system that boots in milliseconds, respects your RAM, and manages your entire AI workload from a single, lightweight binary.

## Key Features

- **Academic Vision, Full-Fledged Agent**: Built on the shoulders of Leann's RAG architecture to create an autonomous assistant where LLM, vector/graph DBs, and plugins unite in one Go app.
- **Zero-Config Extractive Summarization**: High-density sentences are extracted algorithmically during build time, bypassing LLMs and enabling zero-latency "Smart Summaries".
- **Flexible Intelligence (Local or Cloud)**: Run LLMs 100% locally via llama.cpp for total privacy, or connect to any OpenAI-compatible API for high-reasoning tasks.
- **Advanced RAG (Faiss / HNSW & Kuzu Graph DB)**: Indexes documents and code semantically (vector) and relationally (graph), not just via simple keyword matching.
- **Smart Chunking (Tree-sitter)**: Intelligent AST-aware partitioning preserves the structural integrity of your code functions and classes.
- **Graph-Augmented Search**: Search results are enriched with callers/callees from the AST graph, giving LLMs structural code context alongside semantic matches.
- **Impact Analysis**: Blast radius analysis via BFS traversal — find all direct and transitive callers of any symbol and the files they belong to.
- **Multi-Index Chat**: Ask questions across multiple indexes simultaneously with `gleann ask docs,code "question"`. Results are merged by relevance score.
- **Conversations**: Persistent conversation history with `--continue`, `--continue-last`, `--title`. Manage via `gleann chat --list / --show / --delete`.
- **Roles & Format Control**: Named system prompt roles (`--role code`, `--role shell`) and output format control (`--format json`, `--format markdown`). Custom roles in config.
- **Markdown Rendering**: Beautiful terminal markdown output via glamour. Disable with `--raw`.
- **Word-wrap**: Terminal-aware word wrapping with `--word-wrap N` for streaming output.
- **LLM Title Summarization**: Auto-generated conversation titles via LLM when no title is provided.
- **Embedding Cache**: Two-tier cache (L1: otter in-memory ≤50k vectors; L2: disk keyed by SHA-256). L2 hits are promoted to L1; unchanged chunks skip recompute entirely during rebuilds.
- **Pipe-Friendly**: Full stdin/pipe support (`cat file | gleann ask index "review"`), auto-raw mode when stdout is piped, `--quiet` for scripting.
- **No-Cache / No-Limit**: `--no-cache` skips conversation save, `--no-limit` removes token cap for unlimited output.
- **`.gleannignore`**: Gitignore-style patterns to exclude files during index builds.
- **Config Management**: `gleann config show/path/edit/validate` for easy configuration.
- **Model Context Protocol (MCP) Server**: A background service that bridges the gap between your local context and AI tools like Cursor or Claude Desktop.
- **Long-term Memory (BBolt Blocks)**: Hierarchical short/medium/long-term memory that is automatically injected into every LLM query. Store facts with `/remember`, browse with `/memories`.
- **OpenAI-Compatible Proxy**: Drop-in replacement for OpenAI API — use any OpenAI SDK with `model: "gleann/<index>"` for instant RAG.
- **Batch Query (MCP)**: `gleann_batch_ask` runs up to 10 questions concurrently against an index in a single round-trip.
- **Rate Limiting & Timeouts**: Per-IP token-bucket rate limiting (429) and per-endpoint context deadlines (504) protect the server in production.
- **Retry Logic**: Automatic exponential-backoff retry for transient LLM/embedding failures (503, 502, 429, connection refused).
- **Background Maintenance**: Scheduler auto-promotes memory blocks between tiers and prunes expired entries.
- **A2A Protocol (Agent-to-Agent)**: Google's A2A protocol for agent discovery — other AI agents find and communicate with gleann via `/.well-known/agent-card.json`.
- **Unified Memory API**: Single `POST /api/memory/ingest` + `POST /api/memory/recall` interface that orchestrates block memory, knowledge graph, and vector search in parallel.
- **Multimodal Detection**: Automatically detects and uses multimodal Ollama models (Gemma4, Qwen3-VL, LLaVA) for processing images, audio, and video.
- **Background Task Manager**: Monitor long-running operations (indexing, memory consolidation) with progress tracking via `GET /api/tasks`.
- **Zero-Config Auto-Bootstrap**: `gleann serve` detects Ollama, picks the best models, and creates a config file automatically — zero setup required.
- **`gleann go` — One-Command Onboarding**: Detect environment → confirm → pull missing models → index — zero to working in 90 seconds.
- **Cross-Platform Service Management**: `gleann service install/start/stop/status` manages a background server via systemd (Linux), launchd (macOS), or Task Scheduler (Windows).
- **Auto Model Pull**: Missing Ollama models are automatically pulled with streaming progress — no manual `ollama pull` needed.
- **Tiered Model Strategy**: Quick-start mode uses lightweight models (nomic-embed-text, 270 MB) for fast onboarding, with upgrade path to larger models.
- **Sleek and Fast Terminal Interface (TUI)**: A keyboard-centric, fluid interface that brings your documents and code to life directly in your shell.

## Documentation

Detailed guides:

- **[Getting Started](docs/getting-started.md)** — Zero to first search in 5 minutes
- **[Cookbook](docs/cookbook.md)** — Real-world usage recipes
- [Architecture & Design](docs/architecture.md) — Internals, module structure, data flow
- [Configuration](docs/configuration.md) — Config file, CLI flags, recommended models
- [Environment Variables](docs/env-vars.md) — Complete env var reference
- [REST API Reference](docs/api.md) — Endpoint docs, curl examples, OpenAPI/Swagger UI
- [MCP Server](docs/mcp.md) — Setup for Cursor, Claude Desktop, and other AI editors
- [Plugin System](docs/plugins.md) — External plugins for PDF, audio, and more
- [Plugin Installation Guide](docs/plugin-install-guide.md) — Step-by-step plugin setup
- [AST Graph Indexer](docs/graph.md) — KuzuDB-based code graph
- [FAISS Backend](docs/faiss.md) — Optional FAISS vector backend
- [Tree-sitter](docs/treesitter.md) — AST-aware code chunking
- [Benchmarks](docs/benchmarks.md) — Performance measurements
- [Troubleshooting](docs/troubleshooting.md) — Common issues and solutions
- [Memory Engine](docs/memory_engine.md) — Generic Knowledge Graph for AI agents
- [A2A Protocol](docs/a2a.md) — Agent-to-Agent discovery and communication
- [Multimodal Processing](docs/multimodal.md) — Image, audio, and video processing
- [Hierarchical GraphRAG](docs/hierarchical_graphrag.md) — Document-level structural intelligence

## Installation

### One-Liner Install (Linux / macOS)

```bash
curl -sSfL https://raw.githubusercontent.com/tevfik/gleann/main/scripts/install.sh | sh
```

Options:
```bash
GLEANN_VERSION=v1.0.0 curl -sSfL .../install.sh | sh   # specific version
GLEANN_FULL=1 curl -sSfL .../install.sh | sh            # full build (tree-sitter)
GLEANN_INSTALL_DIR=/usr/local/bin curl -sSfL .../install.sh | sh  # custom location
```

### From Source

```bash
git clone https://github.com/tevfik/gleann.git
cd gleann

# Build CLI (includes TUI, REST server, MCP server)
go build -o gleann ./cmd/gleann/

# Run setup wizard
./gleann setup
```

Requires Go 1.24+.

### Docker

```bash
# Pure-Go image (~10MB, no tree-sitter/FAISS)
docker build -t gleann .
docker run -p 8080:8080 -v gleann-data:/data/indexes gleann serve

# Full image with tree-sitter AST support (CGo)
docker build -f Dockerfile.full -t gleann-full .
docker run -p 8080:8080 -v gleann-data:/data/indexes gleann-full serve

# docker-compose (gleann + Ollama sidecar)
docker-compose up -d

# Or via Makefile
make docker          # Build pure-Go image
make docker-full     # Build CGo + tree-sitter image
make docker-run      # Run with docker-compose
```

### Install to PATH

The setup wizard (`gleann setup` / `gleann tui` → Setup) installs the binary to `~/.local/bin` or `/usr/local/bin` with shell completions (bash, zsh, fish).

You can also install via Makefile:

```bash
# Install gleann-full (FAISS + tree-sitter) to ~/.local/bin/gleann (recommended)
make install-user

# Install plain gleann (no FAISS, just tree-sitter) to ~/.local/bin/gleann
make install-user-lite

# Install gleann to /usr/local/bin (system-wide, needs sudo)
sudo make install
```

## Usage

### Quick Start

```bash
# Zero-friction onboarding: detect Ollama → auto-configure → pull models → index
gleann go

# Or with specific options
gleann go --docs ./my-project --name my-project --yes
```

`gleann go` detects your environment, shows the configuration for confirmation, pulls any missing models automatically, indexes your current directory, and prints next steps.

### Background Service

```bash
# Start gleann server in background
gleann service start

# Auto-start on login (systemd/launchd/schtasks)
gleann service install

# Server status
gleann service status

# View logs
gleann service logs

# Stop server
gleann service stop
```

### CLI

```bash
# Interactive setup wizard
gleann setup

# Quick auto-configuration (detects Ollama + models)
gleann setup --bootstrap

# Check system health
gleann doctor

# Build index from documents
gleann index build my-docs --docs ./documents/

# Build with AST code graph
gleann index build my-code --docs ./src --graph

# Search
gleann search my-docs "what is HNSW?"

# Search with reranking
gleann search my-docs "what is HNSW?" --rerank

# Search with graph context (callers/callees enrichment)
gleann search my-code "handleSearch" --graph

# Index management
gleann index list
gleann index info my-docs
gleann index remove my-docs
gleann index rebuild my-code --docs ./src --graph
gleann index watch my-code --docs ./src --graph

# Chat with an index (interactive TUI mode)
gleann chat my-docs

# Ask a question (single-shot)
gleann ask my-docs "Explain the architecture"
gleann ask my-docs "Explain the architecture" --interactive

# Multi-index ask (comma-separated)
gleann ask docs,code "How does auth work?"

# Pipe input
cat main.go | gleann ask my-code "Review this code"

# Continue a conversation
gleann ask my-docs --continue-last "What about error handling?"

# Use a role and output format
gleann ask my-docs "List the API endpoints" --role summarize --format json

# Unlimited output, skip conversation save
gleann ask my-docs "Give me everything" --no-limit --no-cache

# Word-wrap streaming output at 80 columns
gleann ask my-docs "Explain architecture" --word-wrap 80

# Raw output (no markdown rendering, for scripts)
gleann ask my-docs "List endpoints" --raw

# Manage conversations
gleann chat --list
gleann chat --show-last
gleann chat --delete-older-than 30d

# Configuration management
gleann config show
gleann config edit
gleann config validate

# Launch TUI
gleann tui

# Start MCP server (for AI editors)
gleann mcp

# Start REST API server
gleann serve --port 8080

# Open interactive API docs (Swagger UI)
open http://localhost:8080/api/docs
```

### Generic Plugin Architecture

Gleann supports external **Plugins** for parsing complex files via local HTTP APIs. Registry: `~/.gleann/plugins.json`.

* **[gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs)**: PDF, Docx, Xlsx extraction via MarkItDown.
* **[gleann-plugin-sound](https://github.com/tevfik/gleann-plugin-sound)**: Audio/Video transcription via whisper.cpp.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## Security

See [SECURITY.md](SECURITY.md) for security policy and reporting vulnerabilities.

## License

MIT
