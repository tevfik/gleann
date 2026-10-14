# gleann

[![CI](https://github.com/tevfik/gleann/actions/workflows/ci.yml/badge.svg)](https://github.com/tevfik/gleann/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/tevfik/gleann)](https://goreportcard.com/report/github.com/tevfik/gleann)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Reference](https://pkg.go.dev/badge/github.com/tevfik/gleann.svg)](https://pkg.go.dev/github.com/tevfik/gleann)

**High-performance local AI workspace, codebase intelligence, and persistent cross-session memory for AI coding agents and terminal power users.**

Single Go binary (~34MB). Zero Python virtual environments, zero Docker required. Everything runs locally on your machine.

---

![Gleann terminal demo — index AI docs, semantic search, RAG Q&A, long-term memory, MCP setup](scripts/demo/gleann-demo.gif)

---

## ⚡ Quick Start (60 Seconds)

### 1. Install Gleann

```bash
# Go install (Recommended)
go install github.com/tevfik/gleann/cmd/gleann@latest

# Or One-Liner (Linux & macOS)
curl -sSfL https://raw.githubusercontent.com/tevfik/gleann/main/scripts/install.sh | sh
```

### 2. Index Your Codebase or Documents

```bash
# Index a codebase with AST call-graph intelligence
gleann index build my-code --docs ./src --graph

# Index documents, PDFs, or Markdown notes
gleann index build my-docs --docs ./documents
```

### 3. Connect to Your AI Editors & Agents

```bash
# Auto-configure all detected AI tools (Cursor, Claude Code, Windsurf, Zed, etc.)
gleann install
```

Now ask questions, search semantically, or let your AI editor use Gleann over MCP:

```bash
# Semantic search
gleann search my-code "auth middleware rate limiting"

# RAG Q&A directly in your terminal
gleann ask my-code "How are database migrations handled?"
```

---

## 🎯 What Do You Want to Do?

Choose your workflow to see how Gleann accelerates your engineering:

### 🤖 1. Supercharge Your AI Agent / Editor (MCP & A2A)
Connect Gleann directly to **Claude Code**, **Cursor**, **Windsurf**, **Cline**, **Zed**, **JetBrains**, or **Neovim**.
- **1-Click Setup**: Run `gleann install` to automatically register the Model Context Protocol (MCP) server across 17+ AI platforms.
- **Context Field Theory (Φ Scoring)**: Dynamic re-ranking prioritizes recency, frequency, and code proximity.
- **Shell Output Compression**: 95+ regex patterns collapse verbose Git, Go, NPM, and Docker logs before they reach the LLM context window.
- **Token Gain Tracking**: Monitor cumulative token savings across sessions with `gleann_gain`.
- 👉 Read the [MCP Setup Guide](docs/mcp.md) & [Agent Integration](docs/a2a.md).

---

### 🧠 2. Deep Code Intelligence & Blast Radius
Vector search alone misses function calls, callers, and dependencies. Gleann combines vector search with an AST code graph in KùzuDB.
- **AST Chunking**: Tree-sitter partitions functions, structs, and interfaces cleanly at syntax boundaries.
- **Call-Graph Traversal**: Discover callers and callees with `gleann graph callers <symbol>`.
- **Blast Radius Analysis**: Trace upstream impact before making breaking changes with `gleann graph explain <symbol>`.
- 👉 Read the [Code Graph Guide](docs/graph.md) & [Tree-sitter Details](docs/treesitter.md).

---

### 📄 3. Technical Documents, PDFs & Tables
Extract complex multi-page documents, PDFs, and spreadsheets without losing table structure.
- **Table-Aware Chunker**: Preserves Markdown tables atomically or splits rows while replicating headers across sub-chunks.
- **Docling BitNet Vision (eif-runtime)**: In-process IBM Docling BitNet 1.58-bit VLM for scanned/raster PDFs and diagrams.
- **Pure-Go Fast Path**: Digital PDFs and DOCX files extract in milliseconds without Python overhead.
- 👉 Read the [Hierarchical GraphRAG Guide](docs/hierarchical_graphrag.md) & [Plugin Architecture](docs/plugins.md).

---

### 💾 4. Persistent Long-Term Memory (Across Sessions)
Give your AI coding assistant institutional memory that survives terminal restarts.
- **Tiered Memory**: Short (session state), Medium (sprints/TODOs), and Long (permanent architectural decisions).
- **Auto-Injection**: Relevant memories are automatically injected into every prompt.
- **CLI Commands**:
  ```bash
  gleann memory remember "Database migrations must always use snake_case" --tag convention
  gleann memory search "architecture"
  gleann memory context
  ```
- 👉 Read the [Memory Engine Guide](docs/memory_engine.md).

---

### 🖥️ 5. Terminal RAG & Interactive Chat
Work directly from your shell without leaving the keyboard.
- **Interactive Chat**: Multi-turn conversations with glamour markdown streaming (`gleann chat my-code`).
- **Pipe Support**: Send compiler logs or diffs directly into Gleann (`git diff | gleann ask my-code "Review changes"`).
- **Multi-Index Fan-Out**: Query documentation and source code simultaneously (`gleann ask docs,code "How does auth work?"`).
- 👉 Read the [Cookbook](docs/cookbook.md).

---

## 🧩 Supported AI Tools & Platforms

Running `gleann install` auto-detects and installs configuration for:

| Platform | Integration Type | Supported Features |
|---|---|---|
| **Claude Code** | PreToolUse Hook + CLAUDE.md | MCP Tools + Auto Memory Injection |
| **Cursor** | `.cursor/rules` + MCP Config | Semantic Code Search + Full Documents |
| **Windsurf** | `.windsurf/rules` + MCP Config | Graph Traversal + Impact Analysis |
| **Cline / Roo Code** | `.clinerules` + MCP Config | File Maps + AST Breadcrumbs |
| **Zed** | `settings.json` MCP | Fast Codebase Retrieval |
| **Neovim** | Avante / CodeCompanion MCP | In-editor RAG & Context |
| **JetBrains** | `.junie/guidelines.md` + MCP | Multi-Index Knowledge Retrieval |
| **Gemini CLI / Codex** | Agent Hooks + Config | Shell Compression + Token Gain |
| **Aider / OpenClaw** | Skills + System Prompt | Long-Term Memory Recall |

---

## 🛠️ Essential Command Reference

### Index Management
```bash
gleann index build my-code --docs ./src --graph   # Index source code with call graph
gleann index build my-docs --docs ./docs          # Index technical documentation
gleann index list                                 # List all indexes, status, and tags
gleann index watch my-code --docs ./src --graph   # Auto-sync index on file changes
```

### Search & RAG
```bash
gleann search my-code "connection pool timeout"           # Fast semantic search
gleann search my-code "connection pool timeout" --rerank  # Search with cross-encoder reranking
gleann ask my-code "Explain how auth tokens are verified" # RAG Q&A with local/cloud LLM
gleann ask docs,code "How to configure SSL?"              # Multi-index federated query
```

### Code Graph & Impact
```bash
gleann graph callers "HandleRequest" --index my-code      # Find all callers of a symbol
gleann graph deps "HandleRequest" --index my-code         # Downstream dependencies
gleann graph explain "HandleRequest" --index my-code      # Full blast radius context
```

### Memory & MCP
```bash
gleann memory remember "JWT secrets are rotated every 90 days"
gleann memory search "rotation"
gleann install                 # Auto-configure AI editors
gleann mcp                     # Launch stdio MCP server
```

---

## 📚 Documentation Deep Dives

- **[Getting Started Guide](docs/getting-started.md)** — Step-by-step onboarding walkthrough
- **[Cookbook & Recipes](docs/cookbook.md)** — Practical examples for real-world codebases
- **[Architecture & Internals](docs/architecture.md)** — In-depth engine mechanics & data flow
- **[Configuration & Models](docs/configuration.md)** — Model recommendations (Ollama, llama.cpp, Cloud)
- **[Hierarchical GraphRAG](docs/hierarchical_graphrag.md)** — Multi-level document structure & KùzuDB
- **[Plugin System](docs/plugins.md)** — Custom extractors for audio, video, and documents
- **[REST API Reference](docs/api.md)** — OpenAPI endpoints & curl examples
- **[Troubleshooting](docs/troubleshooting.md)** — FAQ and common resolutions

---

## 🤝 Contributing & License

Contributions are welcome! Please check [CONTRIBUTING.md](CONTRIBUTING.md) for development setup.

Licensed under the [MIT License](LICENSE).
