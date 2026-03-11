# Configuration Guide

Gleann uses a layered configuration system. Settings are loaded in this order (later sources override earlier ones):

1. **Built-in defaults** (in source code)
2. **Config file** (`~/.gleann/config.json`, created by `gleann setup`)
3. **CLI flags** (highest priority)

## Setup Wizard

The easiest way to configure Gleann:

```bash
gleann setup
```

This interactive TUI guides you through:
- Embedding provider selection (Ollama, OpenAI, etc.)
- Model selection
- Index directory
- Plugin installation

## Config File

Location: `~/.gleann/config.json`

```json
{
  "embedding_provider": "ollama",
  "embedding_model": "bge-m3",
  "ollama_host": "http://localhost:11434",
  "index_dir": "~/.gleann/indexes",
  "llm_provider": "ollama",
  "llm_model": "llama3.2",
  "quiet": false,
  "word_wrap": 0,
  "roles": {
    "devops": ["You are a DevOps expert. Focus on CI/CD, containerization, and infrastructure."],
    "security": ["You are a security auditor. Identify vulnerabilities and suggest fixes."]
  },
  "format_text": {
    "json": "Respond ONLY with valid JSON. No markdown, no explanation.",
    "csv": "Respond with CSV data. Use commas as separators, include a header row."
  }
}
```

### Fields

| Field | Default | Description |
|-------|---------|-------------|
| `embedding_provider` | `ollama` | Provider for embeddings: `ollama`, `openai` |
| `embedding_model` | `bge-m3` | Embedding model name |
| `ollama_host` | `http://localhost:11434` | Ollama server URL |
| `openai_key` | — | OpenAI API key (if using openai provider) |
| `openai_base_url` | — | Custom OpenAI-compatible API base URL |
| `index_dir` | `~/.gleann/indexes` | Where indexes are stored |
| `llm_provider` | `ollama` | Provider for ask/chat: `ollama`, `openai`, `anthropic` |
| `llm_model` | `llama3.2` | LLM model for ask/chat commands |
| `quiet` | `false` | Suppress status messages globally |
| `word_wrap` | `0` | Wrap output at N columns (0 = terminal width) |
| `roles` | — | Custom named roles (map of name → system prompt lines) |
| `format_text` | — | Custom format instructions (map of format name → instruction) |

## CLI Flags

CLI flags override both defaults and config file values.

### Embedding Options

```bash
--model <model>         # Embedding model (default: bge-m3)
--provider <provider>   # Embedding provider: ollama, openai
--host <url>            # Ollama host URL
--batch-size <n>        # Embedding batch size
--concurrency <n>       # Max concurrent embedding batches
```

### Search Options

```bash
--top-k <n>             # Number of results (default: 10)
--metric <metric>       # Distance metric: l2, cosine, ip
--json                  # Output as JSON
--rerank                # Enable two-stage reranking
--rerank-model <model>  # Reranker model
--hybrid                # Hybrid search (vector + BM25)
--graph                 # Enrich results with graph context
--ef-search <n>         # HNSW ef_search parameter
```

### Build Options

```bash
--index-dir <dir>       # Index storage directory
--chunk-size <n>        # Chunk size in tokens (default: 512)
--chunk-overlap <n>     # Chunk overlap in tokens (default: 50)
--graph                 # Build AST code graph
--prune                 # Prune unchanged files
--no-mmap               # Disable memory-mapped access
```

### Ask & Chat Options

```bash
--interactive           # Interactive multi-turn chat mode
--continue <id>         # Continue a previous conversation
--continue-last         # Continue the most recent conversation
--title <title>         # Set conversation title
--role <role>            # Use a named role (code, shell, explain, summarize, or custom)
--format <fmt>           # Output format: json, markdown, raw
--raw                    # Output raw text (no formatting); auto-enabled when piped
--quiet                  # Suppress status messages (for scripting)
--word-wrap <n>          # Wrap output at N columns (default: terminal width)
```

### Conversation Management

```bash
gleann conversations --list                # List all saved conversations
gleann conversations --show <id>           # Show a specific conversation
gleann conversations --show-last           # Show the most recent conversation
gleann conversations --delete <id> [id...] # Delete specific conversations
gleann conversations --delete-older-than 30d  # Delete old conversations
```

## Recommended Models

### Embeddings

| Model | Dimensions | Quality | Speed |
|-------|-----------|---------|-------|
| `bge-m3` | 1024 | Excellent | Fast |
| `nomic-embed-text` | 768 | Good | Very fast |
| `mxbai-embed-large` | 1024 | Excellent | Moderate |

### LLM (for ask/chat)

| Model | VRAM | Quality | Notes |
|-------|------|---------|-------|
| `llama3.2` | 4GB | Good | Fast, small |
| `phi-4:14b` | 10GB | Excellent | Best balance |
| `qwen2.5:32b` | 20GB+ | Outstanding | If you have the VRAM |
