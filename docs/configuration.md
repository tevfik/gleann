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

## Config Validation

To verify the structure and validity of your config file and perform connection diagnostics, run:

```bash
gleann config validate
```

This command parses `~/.gleann/config.json`, displays active providers/models/directories, and runs checks to:
- Verify that the configured `index_dir` exists and is writeable.
- Verify connectivity to the Ollama service (if selected as provider) and print hints if unreachable or if it rejects the request with HTTP 401/403 (missing `ollama_api_key`).

## Ollama Behind an Auth Proxy / Hosted Ollama

Plain local Ollama needs no key. If your Ollama endpoint requires authentication
(reverse proxy with bearer auth, hosted/OpenAI-compatible Ollama), configure an
optional key; gleann then sends `Authorization: Bearer <key>` on every Ollama request
(chat, streaming chat, embeddings, reranker, `/api/tags` model listing, multimodal,
model pulls, `gleann doctor`):

```bash
gleann config set ollama_host https://ollama.example.com
gleann config set ollama_api_key <key>     # aliases: ollama-api-key, ollama_key
export OLLAMA_API_KEY=<key>                # alternative: environment variable fallback
gleann ask docs "question" --ollama-api-key <key>   # one-off override
gleann setup --bootstrap --host https://ollama.example.com --ollama-api-key <key>
```

Precedence: `--ollama-api-key` flag → `ollama_api_key` in `~/.gleann/config.json` →
`OLLAMA_API_KEY`. The key is chosen per provider: the embedding and LLM providers are
resolved independently, `ollama` uses `ollama_api_key`, `openai` uses `openai_api_key`,
so an OpenAI key is never sent to Ollama (and vice versa). `--api-key` still overrides the
key for whichever LLM provider is active.

The setup wizard (`gleann setup`) asks for this key in an optional, masked step right after
the Ollama URL (press Enter to skip), and the Web UI exposes it in the API Key field when
the provider is `ollama`. `gleann config show` masks all API keys.

## Config File

Location: `~/.gleann/config.json`

```json
{
  "embedding_provider": "ollama",
  "embedding_model": "bge-m3",
  "ollama_host": "http://localhost:11434",
  "index_dir": "~/.gleann/indexes",
  "backend": "diskann",
  "llm_provider": "ollama",
  "llm_model": "nemotron-3-nano:4b",
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
| `embedding_provider` | `ollama` | Provider for embeddings: `ollama`, `openai`, `mock` (for testing) |
| `embedding_model` | `bge-m3` | Embedding model name |
| `ollama_host` | `http://localhost:11434` | Ollama server URL |
| `ollama_api_key` | — | Optional API key for Ollama, sent as `Authorization: Bearer <key>` on every Ollama request (chat, embeddings, reranker, model listing, multimodal). Only needed when Ollama sits behind an auth proxy or is a hosted Ollama endpoint. Falls back to the `OLLAMA_API_KEY` environment variable. Never sent to OpenAI/Anthropic |
| `openai_api_key` | — | OpenAI API key (if using openai provider). Never sent to Ollama |
| `openai_base_url` | — | Custom OpenAI-compatible API base URL |
| `index_dir` | `~/.gleann/indexes` | Root directory for all index data. Each named index `<name>` creates `<name>/` (RAG), `<name>_graph/` (code graph), and `<name>_memory/` (Memory Engine) sub-directories here |
| `backend` | `diskann` | Vector search backend: `diskann`, `hnsw`, `faiss`, `faiss-hybrid` |
| `llm_provider` | `ollama` | Provider for ask/chat: `ollama`, `openai`, `anthropic` |
| `llm_model` | `nemotron-3-nano:4b` | LLM model for ask/chat commands |
| `quiet` | `false` | Suppress status messages globally |
| `word_wrap` | `0` | Wrap output at N columns (0 = terminal width) |
| `roles` | — | Custom named roles (map of name → system prompt lines) |
| `format_text` | — | Custom format instructions (map of format name → instruction) |

## CLI Flags

CLI flags override both defaults and config file values.

### Embedding Options

```bash
--model <model>         # Embedding model (default: bge-m3)
--provider <provider>   # Embedding provider: ollama, openai, mock
--host <url>            # Ollama host URL
--ollama-api-key <key>  # Optional Ollama API key (auth proxy / hosted Ollama; env: OLLAMA_API_KEY)
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
--rerank-pool <n>       # Candidates sent to the reranker (default: max(4*top-k, 50))
--rerank-url <url>      # Rerank through a llama-server --reranking endpoint (/v1/rerank), e.g. http://127.0.0.1:8791
--hybrid                # Hybrid search (vector + BM25)
--graph                 # Enrich results with AST and/or hierarchical document context
--ef-search <n>         # HNSW ef_search parameter
```

### Reranking with a local cross-encoder

`--rerank` re-scores the top candidates with a cross-encoder. With the `eif`
embedding provider, point it at a `llama-server` started in reranking mode:

```bash
# bge-reranker-v2-m3 (GGUF). Add -ngl 99 for a GPU build; use -ngl 0 on CPU.
llama-server -m bge-reranker-v2-m3-f16.gguf --reranking --port 8791 \
  -np 1 -c 8192 -b 8192 -ub 8192 --host 127.0.0.1

gleann search my-docs "query" --top-k 10 --rerank --rerank-url http://127.0.0.1:8791
```

With `--rerank` gleann fetches `max(top-k * 4, 50)` candidates (change with
`--rerank-pool <n>`) and sends their full text to the reranker, then returns
the best `top-k`.

Measured on a 25-PDF document corpus (109 questions, digital and scanned PDFs,
hybrid retrieval; answer found at rank N):

| | R@1 | R@5 | R@10 | rerank time / query |
|---|---|---|---|---|
| hybrid, no rerank | 45.9 | 65.1 | 73.4 | – |
| + bge-reranker-v2-m3 F16, RTX 4090 Laptop, 50 candidates | 65.1 | 86.2 | 89.0 | ~1.1 s |

On CPU (16 threads, idle machine) 20 candidates take about 5 s with F16 and
4.8 s with a Q8_0 quantisation (same quality; Q8_0 did not help on GPU). Cost
is roughly linear in the number of candidates (~0.25 s each), so on CPU use
`--rerank-pool 20` (about 5 s) or leave reranking off when latency matters.
Questions and gold answers were generated with an LLM, so treat the absolute
numbers as indicative.

On code (gleann's own Go source, 100 generated tasks per kind, file-level rank,
granite-30m embeddings) reranking is not a clear win: natural-language
descriptions R@1 68 -> 70, doc-comment queries 92 -> 100, but identifier-style
queries drop from 92 to 65, so prefer it for documents and for prose queries.

### Build Options

```bash
--index-dir <dir>       # Index storage directory
--backend <backend>     # Vector backend: diskann (default), hnsw, faiss, faiss-hybrid
--chunk-size <n>        # Chunk size in tokens (default: 512)
--chunk-overlap <n>     # Chunk overlap in tokens (default: 50)
--graph                 # Build AST code graph
--prune                 # Prune unchanged files
--no-mmap               # Disable memory-mapped access

# DiskANN & Product Quantization (PQ) Options
--pq-dim <n>            # PQ sub-vector dimension (default: 32)
--pq-centroids <k>      # Centroids per sub-vector (default: 256)
--search-l <n>          # Search list size for Vamana graph (default: 64)
--search-pq-rerank      # Enable PQ pre-filtering with exact reranking
--diskann-r <n>         # Vamana max out-degree (default: 64)
--diskann-l <n>         # Vamana build search list size (default: 100)
--diskann-alpha <f>     # Vamana graph pruning parameter alpha (default: 1.2)
```

### Ask & Chat Options

```bash
--interactive           # Interactive multi-turn chat mode
--continue <id>         # Continue a previous conversation
--continue-last         # Continue the most recent conversation
--title <title>         # Set conversation title
--role <role>           # Use a named role (code, shell, explain, summarize, or custom)
--agent                 # Enable ReAct Agent mode (with access to KuzuDB and full documents)
--graph                 # Inject AST and structural document context into the LLM prompt
--format <fmt>          # Output format: json, markdown, raw
--raw                   # Output raw text (no formatting); auto-enabled when piped
--quiet                 # Suppress status messages (for scripting)
--word-wrap <n>         # Wrap output at N columns (default: terminal width)
```

### Conversation Management

```bash
gleann conversations --list                # List all saved conversations
gleann conversations --show <id>           # Show a specific conversation
gleann conversations --show-last           # Show the most recent conversation
gleann conversations --delete <id> [id...] # Delete specific conversations
gleann conversations --delete-older-than 30d  # Delete old conversations
```

## Vector Backends

Gleann supports four vector search backends, selectable via config (`backend` field) or `gleann setup`:

| Backend | Default | CGo Required | Description |
|---------|---------|-------------|-------------|
| `diskann` | **Yes** | No | Pure-Go Vamana graph with PQ prefiltering. Disk-resident search using ~2.7x less RAM than HNSW for large datasets. Recommended for production and large corpora. |
| `hnsw` | No | No | Pure-Go HNSW. Fast in-memory search, good for smaller datasets (<100k vectors). |
| `faiss` | No | Yes | C FAISS via CGo. SIMD-accelerated with IVF_FLAT, IVF_PQ, and IVF_SQ8 index types. Best throughput with GPU/AVX2 hardware. |
| `faiss-hybrid` | No | Build only | FAISS builds the index (requires CGo at build time), but search is pure Go. No CGo needed at query time. |

### DiskANN Configuration

DiskANN parameters can be tuned in the config file under `diskann_config`:

```json
{
  "backend": "diskann",
  "diskann_config": {
    "r": 64,
    "l": 100,
    "alpha": 1.2,
    "pq_dim": 32,
    "pq_train_size": 10000,
    "l_search": 100
  }
}
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `r` | `64` | Max out-degree of the Vamana graph |
| `l` | `100` | Candidate list size during build |
| `alpha` | `1.2` | Pruning parameter (>1.0 = denser graph, higher recall) |
| `pq_dim` | `32` | Number of PQ sub-quantizers for prefiltering |
| `pq_train_size` | `10000` | Vectors sampled for PQ codebook training |
| `l_search` | `100` | Candidate list size during search (higher = better recall, slower) |

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
| `nemotron-3-nano:4b` | 4GB | Good | Fast, default |
| `phi-4:14b` | 10GB | Excellent | Best balance |
| `qwen2.5:32b` | 20GB+ | Outstanding | If you have the VRAM |

## Server Mode Settings

When running `gleann serve`, the following features are configurable via environment variables:

### Rate Limiting

Per-IP token-bucket rate limiter. Requests exceeding the limit receive `429 Too Many Requests`.

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_RATE_LIMIT` | `60` | Sustained rate (tokens/second per IP) |
| `GLEANN_RATE_BURST` | `120` | Burst capacity per IP |

The `/health` and `/metrics` endpoints are exempt from rate limiting.

### Request Timeouts

Each request gets a context deadline based on its endpoint. Timed-out requests return `504 Gateway Timeout`. SSE streams bypass timeouts.

| Variable | Default | Applies to |
|----------|---------|-----------|
| `GLEANN_TIMEOUT_ASK_S` | `300` (5 min) | `/ask`, `/v1/chat/completions` |
| `GLEANN_TIMEOUT_SEARCH_S` | `30` | `/search` |
| `GLEANN_TIMEOUT_BUILD_S` | `600` (10 min) | `/build` |
| `GLEANN_TIMEOUT_DEFAULT_S` | `60` | All other endpoints |

### Background Maintenance & Tasks

A background scheduler periodically promotes medium-term memory blocks to long-term, prunes expired entries, and cleans up completed/failed background tasks.

| Variable | Config Key | Default | Description |
|----------|------------|---------|-------------|
| `GLEANN_MAINTENANCE_ENABLED` | `maintenance_enabled` | `true` | Set to `false` or `0` to disable maintenance |
| `GLEANN_MAINTENANCE_INTERVAL_H` | `maintenance_interval_h` | `24` | Hours between maintenance runs |
| `GLEANN_TASK_EVICTION_AGE_H` | `task_eviction_age_hours` | `24` | Hours before finished background tasks are evicted from memory history |

### Sleep-Time Compute (Letta-inspired)

When enabled, a background "sleep-time" agent periodically reflects on recent conversations, extracts key facts, and stores them as long-term memory blocks — without blocking the interactive flow.

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_SLEEPTIME_ENABLED` | `false` | Set to `true` or `1` to enable |
| `GLEANN_SLEEPTIME_INTERVAL` | `30m` | Go duration between reflection cycles |
| `GLEANN_SLEEPTIME_MAX_CONVS` | `5` | Max recent conversations per cycle |

The engine uses the server's configured LLM (model/provider) to summarize conversations and extract memories tagged with `sleep_time` source.

### Memory Block Limits & Scoping

Blocks can have a per-block character limit (`char_limit`) and a scope for isolation.

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_BLOCK_CHAR_LIMIT` | `0` (unlimited) | Default char limit applied to new blocks |

**Scopes:** Blocks can be isolated to a specific context (e.g. conversation ID) by setting the `scope` field. When querying with `?scope=xxx`, only global blocks (scope="") and blocks matching the requested scope are returned. This enables conversation-level memory isolation similar to Letta's conversation model.

### Retry Logic

LLM and embedding API calls automatically retry on transient errors (503, 502, 429, connection refused, timeouts). Default: 3 attempts with exponential backoff (1s → 2s → 4s). Non-retryable errors (400, 401, 404) fail immediately.

### A2A Protocol

Config file settings for the Agent-to-Agent protocol. These can also be set via environment variables.

| Config Key | Env Var | Default | Description |
|-----------|---------|---------|-------------|
| `a2a_enabled` | `GLEANN_A2A_ENABLED` | `true` | Enable A2A endpoints |
| `a2a_base_url` | `GLEANN_A2A_BASE_URL` | auto | Base URL for Agent Card |

See [A2A Protocol](a2a.md) for full documentation.

### Multimodal Processing

| Config Key | Env Var | Default | Description |
|-----------|---------|---------|-------------|
| `multimodal_model` | `GLEANN_MULTIMODAL_MODEL` | auto-detected | Ollama model for media processing |

See [Multimodal Processing](multimodal.md) for supported models and media types.

### Index Governance & MCP Scoping

| Env Var | Default | Description |
|---------|---------|-------------|
| `GLEANN_TAGS` | — | Comma-separated list of tags to expose over MCP. When set, only indexes possessing one of these tags are accessible by AI agents. |
| `GLEANN_AUTO_INDEX_DIRS` | — | Comma-separated pairs `name:dir` to automatically watch and index in the background. |

