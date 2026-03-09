# gleann

[![Release](https://github.com/tevfik/gleann/actions/workflows/release.yml/badge.svg?event=push)](https://github.com/tevfik/gleann/actions/workflows/release.yml)

**A lightweight, brutally fast, and highly flexible AI/RAG workspace and autonomous agent framework built with Go. Inspired by the academic excellence of the Leann RAG backend, engineered for daily terminal use.**

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
- **Flexible Intelligence (Local or Cloud)**: Run LLMs 100% locally via llama.cpp for total privacy, or connect to any OpenAI-compatible API for high-reasoning tasks.
- **Advanced RAG (Faiss / HNSW & Kuzu Graph DB)**: Indexes documents and code semantically (vector) and relationally (graph), not just via simple keyword matching.
- **Smart Chunking (Tree-sitter)**: Intelligent AST-aware partitioning preserves the structural integrity of your code functions and classes.
- **Graph-Augmented Search**: Search results are enriched with callers/callees from the AST graph, giving LLMs structural code context alongside semantic matches.
- **Impact Analysis**: Blast radius analysis via BFS traversal — find all direct and transitive callers of any symbol and the files they belong to.
- **Model Context Protocol (MCP) Server**: A background service that bridges the gap between your local context and AI tools like Cursor or Claude Desktop.
- **Sleek and Fast Terminal Interface (TUI)**: A keyboard-centric, fluid interface that brings your documents and code to life directly in your shell.

## Documentation

Detailed technical documentation and guides:

- [Architecture & Design](docs/architecture.md)
- [FAISS Backend (Optional)](docs/faiss.md)
- [Tree-sitter Backend (Optional)](docs/treesitter.md)
- [AST Graph Indexer](docs/graph.md)
- [Benchmarks & Performance](docs/benchmarks.md)

## Installation

```bash
# From source
git clone https://github.com/tevfik/gleann.git
cd gleann

# Build CLI (includes TUI, REST server, MCP server)
go build -o gleann ./cmd/gleann/

# Run setup wizard
./gleann setup
```

Requires Go 1.24+.

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

### CLI

```bash
# Interactive setup wizard
gleann setup

# Build index from documents
gleann build my-docs --docs ./documents/

# Search
gleann search my-docs "what is HNSW?"

# Search with reranking
gleann search my-docs "what is HNSW?" --rerank

# Search with graph context (callers/callees enrichment)
gleann search my-code "handleSearch" --graph

# Rebuild an index from scratch (remove + build)
gleann rebuild my-code --docs ./src --graph

# Chat with an index
gleann chat my-docs

# Ask a question (single-shot)
gleann ask my-docs "Explain the architecture" --interactive

# List indexes
gleann list

# Launch TUI
gleann tui

# Build vector index + AST call graph simultaneously
gleann build my-code --docs ./src --graph

# Start MCP server (for AI editors)
gleann mcp
```

### Generic Plugin Architecture

Gleann supports external **Plugins** for parsing complex files via local HTTP APIs. Registry: `~/.gleann/plugins.json`.

* **[gleann-plugin-docs](https://github.com/tevfik/gleann-plugin-docs)**: PDF, Docx, Xlsx extraction via MarkItDown.
* **[gleann-plugin-sound](https://github.com/tevfik/gleann-plugin-sound)**: Audio/Video transcription via whisper.cpp.

## Roadmap

- [x] Interactive TUI
- [x] Two-stage reranker
- [x] MCP server (embedded)
- [x] Setup wizard + install
- [x] AST Graph Indexer (KuzuDB)
- [x] Graph-Augmented Search (callers/callees in search results)
- [x] Impact Analysis endpoint (BFS blast radius)
- [x] Rebuild command (convenience remove + build)

## License

MIT
