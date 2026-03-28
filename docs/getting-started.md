# Getting Started with Gleann

Get from zero to your first AI-powered search in under 5 minutes.

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

## Step 6: Explore Further

```bash
# Launch the visual TUI
gleann tui

# Start the REST API server
gleann serve

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
| Search | `gleann search <name> <query>` |
| Ask (RAG) | `gleann ask <name> <question>` |
| Chat | `gleann chat <name>` |
| List indexes | `gleann index list` |
| TUI | `gleann tui` |
| API server | `gleann serve` |
| Health check | `gleann doctor` |
| Help | `gleann help` |

## Next Steps

- [Configuration Guide](configuration.md) — Fine-tune models, providers, and search parameters
- [Plugin System](plugins.md) — Add PDF, DOCX, audio support
- [Plugin Installation Guide](plugin-install-guide.md) — Step-by-step plugin setup
- [Cookbook](cookbook.md) — Real-world usage recipes
- [Environment Variables](env-vars.md) — Override config for Docker/CI
- [REST API Reference](api.md) — Build integrations
- [MCP Server](mcp.md) — Connect to AI editors (Cursor, Claude Desktop, VS Code)
- [Troubleshooting](troubleshooting.md) — Common issues and fixes
