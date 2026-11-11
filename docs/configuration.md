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
| `rerank_enabled` | `false` | Rerank search results (CLI `search`/`ask`, REST, MCP, chat). `--rerank` turns it on for one command, `--no-rerank` off |
| `rerank_provider` | `auto` | Reranker engine: `auto`, `eif` (built-in bitllm.c cross-encoder), `ollama`, `llamacpp`, `jina`, `cohere`, `voyage`. `auto` uses the built-in engine when the build has it and the model is a local GGUF, otherwise the embedding provider |
| `rerank_model` | built-in: `ms-marco-minilm-l6` for mostly-code results when downloaded, else `mmarco-mMiniLMv2-L12-H384-v1-Q8_0.gguf`; server: `bge-reranker-v2-m3` | Reranker model: a catalog id, a GGUF in `~/.gleann/models` or a path for the built-in engine, a model name for a server |
| `expand` / `expand_model` | `false` / chat model | HyDE query expansion by default (see Query expansion); `--no-expand` turns it off for one command |
| `storage_mode` | `store` | Vector storage of new indexes: `store` or `recompute` (see below) |
| `proxy_mode` | — | `passthrough` makes `POST /v1/chat/completions` forward the client's request unchanged (plus the retrieved context) and stream the LLM server's reply back byte for byte; empty uses gleann's chat client. `GLEANN_PROXY_MODE` overrides it. See [REST API](api.md) |
| `auth` | `on` | REST API authentication: `on` (token required) or `off` (accepted only on a loopback address). `GLEANN_AUTH` overrides it |
| `cors_origins` | — | Origins whose web pages may call the API, e.g. `["https://app.example.com"]`. `GLEANN_CORS_ORIGINS` adds more |
| `rate_limit` / `rate_burst` | `60` / `120` | Requests per second / burst per client address (loopback clients are not limited). `GLEANN_RATE_LIMIT` / `GLEANN_RATE_BURST` override them |

Every field above is also editable from `gleann setup` → All Settings and from the web UI (System → Settings); both are built from the same list, so they offer the same values and checks.
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
--rerank                # Enable two-stage reranking (on by default when rerank_enabled is saved)
--no-rerank             # Do not rerank, even when rerank_enabled is saved
--rerank-model <model>  # Reranker model (overrides rerank_model)
--rerank-provider <p>   # Reranker engine (overrides rerank_provider): auto, eif, ollama, llamacpp, jina, cohere, voyage
--rerank-pool <n>       # Candidates sent to the reranker (default: 20 for the built-in reranker, max(4*top-k, 50) for a server)
--rerank-url <url>      # Rerank through a llama-server --reranking endpoint (/v1/rerank), e.g. http://127.0.0.1:8791
--no-hybrid             # Disable BM25 hybrid scoring (vector only)
--hybrid-alpha <float>  # BM25 hybrid weight (default: 0.7)
--graph                 # Enrich results with AST and/or hierarchical document context
--kind <code|docs|all>  # Filter search results by content kind
--one-per-file          # Return at most one result per file
--explain               # Show why each result matched: most important segments
                        # (occlusion, 8 segments ≈ 70 ms/result) and BM25 term weights
--include-tests         # Include test files in search results
--ef-search <n>         # HNSW ef_search parameter
--expand                # HyDE query expansion (see below; also for `ask`)
--no-expand             # Do not expand, even when `expand` is saved
--expand-model <model>  # LLM for --expand (default: expand_model, else llm_model)
```

### Query expansion (HyDE)

Natural-language questions ("where are search results deduplicated?") share
few words with the code that answers them. With `--expand` the LLM first
writes a short hypothetical code snippet for the question, in the dominant
language of the index; gleann then embeds and BM25-matches the question
plus that snippet. Identifier-like queries (`LeannSearcher`, `parse_args`,
`func NewServer`) are searched as typed. Snippets are cached per query.

```bash
gleann search my-repo "how are results reranked" --expand
gleann ask my-repo "how does sync detect deleted files?" --expand --expand-model qwen3.5:9b
```

REST: `"expand": true` in `POST /api/indexes/{name}/search` and `.../ask`; MCP: `expand`
on `gleann_search`. Save `expand: true` / `expand_model` in the search
settings to turn it on by default.

Measured with `gleann bench` on 121 natural-language questions about the
gleann code base (hybrid retrieval with nomic-embed-text, top-10 chunks; the
LLM on a GPU):

| | R@1 | R@10 | MRR | added latency |
|---|---|---|---|---|
| hybrid | 0.34 | 0.71 | 0.49 | – |
| + expand, qwen3.5:9b | 0.49 | 0.81 | 0.63 | ~1.5–3 s |
| + expand + rerank (ms-marco-minilm-l6) | 0.47 | 0.81 | 0.61 | + ~0.4 s |
| + expand, llama3.2:3b | – | – | 0.52 | ~1 s |

The model matters: use a ~9B model for `expand_model` (the default chat
model, llama3.2:3b, gains little). On queries phrased like doc comments
("returns the …") expansion alone loses a little (MRR −0.05); with
`--rerank` no query set lost (391 tasks). Expansion costs one LLM call per
new query, so it suits `ask`, agents and "how/where" questions more than
interactive keyword lookups; on a CPU-only machine the LLM call usually
costs more than it is worth. Details: docs/retrieval-experiments.md.

### Reranking with a local cross-encoder

`--rerank` (or `rerank_enabled`) re-scores the top candidates with a
cross-encoder. Builds with the `eif` tag (the `gleann-full` and `gleann-eif`
releases) run the cross-encoder in process with bitllm.c, so no server is
needed:

```bash
gleann models download mmarco-mminilmv2-l12     # recommended: multilingual, ~0.4 s/query for 20 candidates on CPU
gleann models download bge-reranker-v2-m3       # most accurate, ~3 s/query on CPU (up to 13 s on long passages)
gleann models download ms-marco-minilm-l6       # English code and docs: fastest, best on the code benchmark
gleann search my-docs "query" --rerank          # uses rerank_model; when none is set: ms-marco-minilm-l6 for code, mmarco otherwise
```

Measured on MTEB TurHistQuadRetrieval (Turkish) on an ARM CPU, hybrid
retrieval with multilingual-e5-small, 20 candidates: nDCG@10 0.436 → 0.493
with mmarco, MRR@10 0.57 → 0.72 (full report: the gleann-rag-test benchmark).
On English source code (gleann's own benchmark, 391 tasks, 20 candidates of
500 characters) ms-marco-minilm-l6 raised question MRR by 0.07 and mMiniLMv2
by 0.01. Identifier queries (`LeannSearcher`, `parse_args`, `func NewServer`)
are left in first-stage order, since passage rerankers demote exact symbol
hits. The published GGUF of ms-marco-MiniLM-L6 lacks the model's pooler
layer, which makes its scores nearly constant; `gleann models download`
adds it from the original checkpoint (~600 KB). See
docs/retrieval-experiments.md.

Pick the model and the engine in the settings (`gleann setup` → All Settings,
or System → Settings in the web UI); both list the recommended models first.

Ollama has no cross-encoder API, so `rerank_provider: ollama` returns an error
explaining this (earlier versions silently compared embeddings of the reranker
model, which scored worse than no reranking). A real cross-encoder server can
be used through llama.cpp, started in reranking mode (`rerank_provider:
llamacpp` with `rerank_url`, or `--rerank-url`):

```bash
# bge-reranker-v2-m3 (GGUF). Add -ngl 99 for a GPU build; use -ngl 0 on CPU.
llama-server -m bge-reranker-v2-m3-f16.gguf --reranking --port 8791 \
  -np 1 -c 8192 -b 8192 -ub 8192 --host 127.0.0.1

gleann search my-docs "query" --top-k 10 --rerank --rerank-url http://127.0.0.1:8791
```

With `--rerank` gleann fetches `max(top-k * 4, 50)` candidates. The built-in
engine scores the best 20 of them, using the first 500 characters of each.
Server rerankers score all of them in full. Change the count with
`--rerank-pool <n>` (`rerank_pool` in REST search). gleann then returns the
best `top-k`.

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

### Vector storage

HNSW indexes keep their vectors by default (`storage_mode: "store"`), so a search embeds only the query. `recompute` keeps just the graph and re-embeds the visited nodes on every search: the index is much smaller, a search is one to two orders of magnitude slower (about 6 s for 3k passages, about 10 s for 6k with a local Ollama). DiskANN always stores vectors.

```bash
gleann index build docs --docs ./docs --storage recompute   # one build
gleann index storage                                        # table of all indexes: mode and size
gleann index storage docs --set store                       # convert in place (uses the embedding cache, rolls back on failure)
```

`storage_mode` in `~/.gleann/config.json` sets the default for new indexes (Settings → Vector storage in the TUI and the web UI). In the TUI Index Manager, `s` switches the highlighted index and `g` toggles the default; in the web UI the Indexes page shows each index's mode with a switch button (`POST /api/indexes/{name}/storage {"mode":"store"|"recompute"}`, runs as a background task).

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
--cite                  # After the answer, list the sources each sentence came from
                        # (embedding match of answer sentences to context passages,
                        # no prompt change; ~0.1 s). REST: {"cite": true} → "citations"
--quiet                 # Suppress status messages (for scripting)
--word-wrap <n>         # Wrap output at N columns (default: terminal width)
```

### Conversation Management

```bash
gleann chat --list                # List all saved conversations
gleann chat --show <id>           # Show a specific conversation
gleann chat --show-last           # Show the most recent conversation
gleann chat --delete <id> [id...] # Delete specific conversations
gleann chat --delete-older-than 30d  # Delete old conversations
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

