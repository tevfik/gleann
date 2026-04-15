# Getting Started with Gleann

Get from zero to your first AI-powered search in under 5 minutes.

## Fastest path: `gleann quickstart`

Already have gleann installed and Ollama running? Run one command inside any
project directory and gleann indexes it, then prints your MCP connection config:

```bash
cd ~/my-project
gleann quickstart
```

gleann autodetects your saved embedding config, builds the index, and outputs
the exact snippet to paste into your `claude_desktop_config.json` or Cursor MCP
settings. No flags required — every option can be overridden if needed:

```bash
gleann quickstart --docs ./docs --name my-project --graph
```

For first-time setup continue with the steps below.

---

## Prerequisites

| Requirement | Why | Install |
|-------------|-----|---------|
| **Go 1.24+** | Build gleann | [go.dev/dl](https://go.dev/dl/) |
| **Ollama** | Local LLM + embeddings | [ollama.com/download](https://ollama.com/download) |

## Step 1: Install Gleann

```bash
# Option A: One-liner install (Linux/macOS)
curl -sSL https://raw.githubusercontent.com/tevfik/gleann/main/scripts/install.sh | sh

# Option B: Build from source
git clone https://github.com/tevfik/gleann.git
cd gleann
go build -o gleann ./cmd/gleann/
sudo mv gleann /usr/local/bin/   # or: mv gleann ~/.local/bin/
```

## Step 2: Start Ollama & Pull Models

```bash
# Start Ollama (if not already running)
ollama serve &

# Pull the embedding model (~1.5 GB)
ollama pull bge-m3

# Pull a chat model (~2 GB)
ollama pull llama3.2:3b-instruct
```

## Step 3: Configure Gleann

```bash
# Quick auto-config (detects Ollama, picks best models)
gleann setup --bootstrap

# Or: interactive wizard with TUI
gleann setup
```

## Step 4: Index Your Documents

```bash
# Index a folder of documents
gleann index build my-docs --docs ./my-documents/

# Index source code (with AST graph for call analysis)
gleann index build my-code --docs ./src/ --graph
```

**Expected output:**
```
📂 Scanning ./my-documents/...
📝 Found 42 files (156 chunks)
🔢 Computing embeddings... [████████████████████] 100% (156/156)
✅ Index "my-docs" built (156 chunks, 0.8s)
```

## Step 5: Search & Ask

```bash
# Search for relevant passages
gleann search my-docs "how does authentication work?"

# Ask a question (RAG-powered answer)
gleann ask my-docs "Explain the authentication flow"

# Interactive chat
gleann chat my-docs
```

**Expected output (gleann ask):**
```
Based on the documentation, the authentication flow works as follows:

1. Client sends credentials to /api/auth/login
2. Server validates against the user store
3. A JWT token is issued with 24h expiry
...

Sources:
  [1] docs/auth.md (score: 0.92)
  [2] docs/api-reference.md (score: 0.87)
```

### Power-user search options

```bash
# Multi-index search — comma-separate any number of indexes
gleann search code,docs "rate limiter implementation"

# Search all indexes at once
gleann search --all "deployment pipeline"

# Enable cross-encoder reranking for higher precision (requires bge-reranker)
gleann search my-docs "authentication" --rerank
gleann search code,docs "cache invalidation" --rerank --rerank-model bge-reranker-v2-m3
```

## Step 6: Code Intelligence (AST Graph)

If you built your index with `--graph`, you can traverse the call graph:

```bash
# What does this function call?
gleann graph deps    myFunc --index my-code

# Who calls this function?
gleann graph callers myFunc --index my-code

# Full context: callers, callees, blast radius
gleann graph explain myFunc --index my-code

# Find symbols by name or pattern
gleann graph query   "handler" --index my-code

# Shortest dependency path between two symbols
gleann graph path    ServiceA ServiceB --index my-code

# Generate a Markdown report (god nodes, communities) → GRAPH_REPORT.md
gleann graph report  --index my-code

# Interactive HTML visualization
gleann graph viz     --index my-code
```

## Step 7: Long-term Memory

Gleann maintains persistent tiered memory that survives across sessions:

```bash
# Store a permanent fact (long-tier, default)
gleann memory remember "This codebase uses hexagonal architecture"

# Store sprint-scoped info (medium-tier)
gleann memory add medium "Sprint 14: focus on latency improvements"

# Session-only note (short-tier)
gleann memory add short "Current task: refactoring auth module"

# Recall everything
gleann memory list
gleann memory search "architecture"

# Housekeeping
gleann memory summarize --last   # compress last conversation into memory
gleann memory prune --age 90d    # remove entries older than 90 days
gleann memory stats
```

## Step 8: Integrate with AI Editors

Install gleann into your AI coding platform in one command:

```bash
# Auto-detect your platform (OpenCode, Claude Code, Cursor, Codex, etc.)
gleann install

# Or target a specific platform
gleann install --platform opencode
gleann install --platform claude
gleann install --platform cursor

# See all supported platforms
gleann install --list
```

This writes `AGENTS.md`, MCP config, and platform-specific hooks automatically.

## Step 9: Explore Further

```bash
# Launch the visual TUI
gleann tui

# Start the REST API + OpenAI-compatible proxy
gleann serve

# Use gleann as an OpenAI-compatible backend from any client
# (after gleann serve is running)
python3 -c "
from openai import OpenAI
client = OpenAI(base_url='http://localhost:8080/v1', api_key='none')
r = client.chat.completions.create(
    model='gleann/my-docs',
    messages=[{'role':'user','content':'How does auth work?'}]
)
print(r.choices[0].message.content)
"

# Auto-rebuild index whenever files change (incremental — only re-embeds changed files)
gleann index watch my-code --docs ./src/

# Check system health
gleann doctor

# Enable shell completions (bash/zsh/fish)
source <(gleann completion bash)
```

## Quick Reference

| Task | Command |
|------|---------|
| Setup | `gleann setup` |
| Build index | `gleann index build <name> --docs <dir>` |
| Build with graph | `gleann index build <name> --docs <dir> --graph` |
| Auto-rebuild on change | `gleann index watch <name> --docs <dir>` (incremental) |
| Search | `gleann search <name> <query>` |
| Multi-index search | `gleann search name1,name2 <query>` |
| Search all indexes | `gleann search --all <query>` |
| Search + rerank | `gleann search <name> <query> --rerank` |
| Ask (RAG) | `gleann ask <name> <question>` |
| Chat | `gleann chat <name>` |
| Graph explain | `gleann graph explain <symbol> --index <name>` |
| Graph report | `gleann graph report --index <name>` |
| Memory store | `gleann memory remember "<fact>"` |
| Memory recall | `gleann memory list` |
| Install for AI editor | `gleann install [--platform <name>]` |
| List indexes | `gleann index list` |
| TUI | `gleann tui` |
| API server | `gleann serve` |
| Health check | `gleann doctor` |
| Help | `gleann help` |

## Next Steps

- [Configuration Guide](configuration.md) — Fine-tune models, providers, and search parameters
- [Platform Install](mcp.md) — Auto-configure for OpenCode, Claude Code, Cursor, and more
- [Plugin System](plugins.md) — Add PDF, DOCX, audio support
- [Plugin Installation Guide](plugin-install-guide.md) — Step-by-step plugin setup
- [Graph Intelligence](graph.md) — Deep dive into AST call graph features
- [Long-term Memory](memory_engine.md) — Tiered memory, sleep-time engine, rotation
- [Cookbook](cookbook.md) — Real-world usage recipes
- [Environment Variables](env-vars.md) — Override config for Docker/CI
- [REST API Reference](api.md) — Build integrations (includes OpenAI proxy + A2A)
- [MCP Server](mcp.md) — Connect to AI editors (Cursor, Claude Desktop, VS Code)
- [Troubleshooting](troubleshooting.md) — Common issues and fixes
