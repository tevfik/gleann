# Environment Variables

Gleann can be configured via environment variables. These override values in `~/.gleann/config.json` but are overridden by CLI flags.

Useful for Docker, CI/CD, and environments where editing config files is impractical.

## Core Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_INDEX_DIR` | `index_dir` | `~/.gleann/indexes` | Root directory for all index data |
| `GLEANN_CONFIG_DIR` | — | `~/.gleann` | Directory that holds `config.json` and the other gleann state files (read by `configpath.GetConfigDir`); downloaded models live in `<config dir>/models` |
| `GLEANN_MEMORY_DIR` | — | `~/.gleann/memory` | Directory of the long-term memory database (`memory.db`) |
| `GLEANN_CONVERSATIONS_DIR` | — | `~/.gleann/conversations` | Directory of saved conversations |
| `GLEANN_PACKS_DIR` | — | `<index_dir>/packs` | Directory the server loads knowledge packs from at start-up |
| `GLEANN_THEME` | — | agenttui default | Colour theme of the agent chat TUI, by name (for example `opencode`, `dracula`, `nord`, `gruvbox`, `catppuccin`, `tokyo-night`) |
| `GLEANN_CHAT_UI` | — | agent chat | Set to `classic` to use the original chat interface (same as `--classic`) |
| `OLLAMA_HOST` | `ollama_host` | `http://localhost:11434` | Ollama server URL |
| `OLLAMA_API_KEY` | `ollama_api_key` | — | Optional Ollama API key, sent as `Authorization: Bearer <key>` (Ollama behind an auth proxy / hosted Ollama). Used only when `ollama_api_key` is not set |
| `GLEANN_MMAP` | `use_mmap` | unset | `1`/`true`: memory-map the vector index for every backend; `0`/`false`: load it into RAM. Unset: the per-backend keys decide (both default to memory-mapped) |
| `GLEANN_HNSW_DTYPE` | `hnsw_config.vector_dtype` | `f32` | Stored vector encoding for HNSW indexes: `f32` (32-bit float, default) or `f16` (16-bit half-precision float). Halves index file size and resident memory with distance evaluated on the fly |

## Authentication & Network

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_API_TOKEN` | — | — | Token the CLI/MCP clients send; on the server, a value of 16+ characters is also accepted as an admin token (Docker/CI) |
| `GLEANN_AUTH` | `auth` | `on` | `off` disables API authentication (loopback bind only) |
| `GLEANN_ALLOWED_HOSTS` | — | — | Extra `Host` names accepted on a loopback bind (comma separated) |
| `GLEANN_ALLOWED_ROOTS` | `allowed_roots` | home directory | Directories the REST server (and the MCP `gleann_sync` `docs_dir`) may index on a client's request (path-list separated: `:` on Unix, `;` on Windows) |
| `GLEANN_CORS_ORIGINS` | `cors_origins` | — | Browser origins allowed cross-origin (comma separated); none by default |
| — (config key) | `storage_mode` | `store` | `store` keeps vectors in the index (fast search); `recompute` keeps none and re-embeds on every search (slow). Manage with `gleann index storage` or the Index Manager TUI |

## LLM Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_LLM_PROVIDER` | `llm_provider` | `ollama` | LLM provider: `ollama`, `openai`, `anthropic` (read only by MCP server) |
| `GLEANN_LLM_MODEL` | `llm_model` | `nemotron-3-nano:4b` | LLM model for ask/chat (read only by MCP server) |
| `GLEANN_THINK` | — | off | `1` or `true` asks reasoning models to think; unset or anything else keeps thinking off |
| `GLEANN_REPEAT_PENALTY` | — | `1.1` | Repetition penalty of the chat (a positive number). With the embedded EIF runtime it defaults to `1.0` unless this or `GLEANN_REPETITION_PENALTY` is set |
| `GLEANN_REPETITION_PENALTY` | — | `1.1` | Alias for `GLEANN_REPEAT_PENALTY` (used when that one is unset) |
| `GLEANN_CONTEXT_TOKENS` | — | asked from the provider | Token window of the chat model, when gleann cannot learn it from Ollama or the OpenAI-compatible server; takes precedence over what the provider reports |
| `GLEANN_RERANK` | — | — | Set to `1` or `true` to rerank in the MCP server's search and ask tools even when the config has the reranker off (read only by MCP server) |
| `GLEANN_RERANK_MODEL` | — | — | Reranker model for the MCP server, used when the config names none (read only by MCP server) |
| `GLEANN_RERANK_PROVIDER` | — | config | Reranker provider for the MCP server, overriding the config (read only by MCP server) |
| `OPENAI_API_KEY` | `openai_api_key` | — | OpenAI API key |
| `OPENAI_BASE_URL` | `openai_base_url` | — | OpenAI-compatible API base URL, used when `openai_base_url` is not set. No default: with neither set the `openai` provider fails instead of using api.openai.com |

## API Keys (Rerankers & Providers)

| Variable | Default | Description |
|----------|---------|-------------|
| `ANTHROPIC_API_KEY` | — | Anthropic API key (LLM provider) |
| `GEMINI_API_KEY` | — | Google Gemini API key (embedding/LLM provider) |
| `GOOGLE_API_KEY` | — | Alias for `GEMINI_API_KEY` |
| `JINA_API_KEY` | — | Jina AI reranker API key |
| `COHERE_API_KEY` | — | Cohere reranker API key |
| `CO_API_KEY` | — | Alias for `COHERE_API_KEY` |
| `VOYAGE_API_KEY` | — | Voyage AI reranker API key |

## Rate Limiting & Timeout (Server Mode)

These variables configure the REST API server (`gleann serve`).

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_RATE_LIMIT` | `60` | Sustained request rate per IP (tokens/second) |
| `GLEANN_RATE_BURST` | `120` | Maximum burst capacity per IP |
| `GLEANN_TIMEOUT_ASK_S` | `300` | Timeout in seconds for `/ask` and `/v1/chat/completions` endpoints |
| `GLEANN_TIMEOUT_SEARCH_S` | `30` | Timeout in seconds for `/search` endpoints |
| `GLEANN_TIMEOUT_BUILD_S` | `600` | Timeout in seconds for `/build` endpoints; the HTTP write timeout follows it (`max(30 min, value + 5 min)`) |
| `GLEANN_TIMEOUT_DEFAULT_S` | `60` | Default timeout in seconds for all other endpoints |
| `GLEANN_MAX_BODY_BYTES` | `16777216` | Maximum POST/PUT/PATCH body size in bytes (16 MiB default; `0` disables the cap) |
| `GLEANN_WEBHOOK_ALLOW_PRIVATE` | _(unset)_ | Set to `1` to allow webhook URLs whose hostname resolves to loopback / link-local / private addresses (off by default to block SSRF) |

| `GLEANN_PROXY_CONTEXT_INTRO` | config `proxy_context_intro` | Replaces the sentence that opens the retrieved context in pass-through proxy mode; gleann always appends the instruction to answer in the language of the user's question |
| `GLEANN_PROXY_MODE` | config `proxy_mode` | `passthrough` forwards `/v1/chat/completions` and `POST /stop` to the LLM server unchanged (see the API reference); any other non-empty value turns it off, overriding the config |
| `GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE` | _(unset)_ | Set to `1` to let `POST /api/models/download` fetch from private, loopback or link-local addresses (off by default to block SSRF) |

Rate-limited requests receive `429 Too Many Requests` with a `Retry-After: 1` header. Timed-out requests receive `504 Gateway Timeout`. The `/health` and `/metrics` endpoints bypass rate limiting. SSE streaming endpoints bypass the timeout middleware.

## Searcher Cache & Memory Profile (Server & MCP)

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_SEARCHER_CACHE_BUDGET` | `searcher_cache_budget` | `0` (unlimited) | Memory budget for cached index searchers in `gleann serve` and the MCP server (e.g. `500MB`, `1GB`, `100MiB`, or bytes as an integer). When total resident size exceeds the budget, least-recently-used searchers are evicted |

## Background Tasks & Maintenance (Server Mode)

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_MAINTENANCE_ENABLED` | `true` | Enable background maintenance scheduler (`false` or `0` to disable) |
| `GLEANN_MAINTENANCE_INTERVAL_H` | `24` | Hours between maintenance runs (promotes medium→long, prunes expired blocks) |
| `GLEANN_AUTO_INDEX_DIRS` | — | Directories the server's auto-indexer watches for changes, as `name1:dir1,name2:dir2` (an entry without both parts is skipped); indexes with `auto_watch` set in their metadata are watched too |
| `GLEANN_TASK_EVICTION_AGE_H` | `24` | Hours before finished (completed/failed) background tasks are evicted from memory history |

## Sleep-Time Compute (Letta-inspired)

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_SLEEPTIME_ENABLED` | `false` | Enable sleep-time engine — background agent that reflects on conversations and extracts memories (`true` or `1` to enable) |
| `GLEANN_SLEEPTIME_INTERVAL` | `30m` | Go duration between reflection cycles (e.g. `15m`, `1h`) |
| `GLEANN_SLEEPTIME_MAX_CONVS` | `5` | Maximum recent conversations to process per cycle |

## Memory Block Limits

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_BLOCK_CHAR_LIMIT` | `0` (unlimited) | Default character limit for memory blocks. When set, new blocks are auto-truncated if they exceed this limit |
| `GLEANN_MEMORY_MAX_TOKENS` | `4000` | Cap on the memory injected into prompts and compiled context windows, in estimated tokens (~4 chars/token). `0` disables the cap. Over budget, whole blocks drop lowest-priority first (short, then medium, then long; older and less-accessed first). Also settable as `MaxContextTokens` on `memory.Manager` |

## MCP Server Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_TOOLS` | `core` | MCP tool profile (read only by MCP): `core` (essential tools; the graph tools only when a format-2 code graph can exist), `full` (all tools), or comma-separated list of tool names/aliases |

| `GLEANN_INDEX` | — | Index used when none is named: by the MCP tools, `gleann agents` and `gleann install` (before they fall back to the directory name) |
| `GLEANN_TAGS` | — | Comma-separated tags; indexes without one of them are hidden from the MCP server, the multi-index search and the server's `GET /api/indexes` (this cannot be bypassed by a client query parameter) |
| `GLEANN_MCP_CLEAN_NAMES` | — | Set to `1` to strip the `gleann_` prefix from tool names (same as `--clean-names`) |
| `GLEANN_MCP_STRIP_PREFIX` | — | Alias for `GLEANN_MCP_CLEAN_NAMES` (`1`) |

## A2A Protocol (Agent-to-Agent)

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_A2A_ENABLED` | `true` | Enable A2A protocol endpoints (`false` or `0` to disable) |
| `GLEANN_A2A_BASE_URL` | auto-detected | Base URL for the A2A Agent Card (e.g. `https://my-host:8080`) |

## Multimodal Processing

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_MULTIMODAL_MODEL` | auto-detected | Ollama model for multimodal processing during indexing and chat (e.g. `gemma4:e4b`, `llava`). Auto-detects from available Ollama models if not set. CLI flag: `--multimodal-model` |

| `GLEANN_MULTIMODAL_LANG` | `en` | Language of the multimodal prompts and descriptions: `tr` for Turkish, anything else is English |
| `GLEANN_MULTIMODAL_WORKERS` | `4` | Files processed in parallel when indexing a directory (1–64; other values keep the default) |
| `GLEANN_MULTIMODAL_CACHE` | on | `0`, `off` or `false` disables the on-disk cache of generated descriptions |
| `GLEANN_MULTIMODAL_CACHE_DIR` | `~/.gleann/cache/multimodal` | Directory of that cache |
| `GLEANN_EXIFTOOL` | off | Set to `1` to add `exiftool` output to a file's metadata (needs `exiftool` on `PATH`) |
| `GLEANN_PDF_MARKER_ONLY` | off | Set to `1` to extract PDF text with the marker plugin only, without the page-by-page vision pass |
| `GLEANN_AUDIO_PLUGIN_URL` | registered plugin | Base URL of the audio transcription plugin; `off` or `none` disables it. When unset, gleann takes the URL of a registered audio plugin and sets this variable for the process |
| `GLEANN_VISION_PLUGIN_URL` | registered plugin | Base URL of the vision plugin; `off` disables it. When unset, gleann takes the URL of a registered vision plugin |
| `GLEANN_AUDIO_PLUGIN_TOKEN` | plugin token | Bearer token sent to the audio plugin at `GLEANN_AUDIO_PLUGIN_URL`. gleann sets it for the process from the plugin's stored token (or `token` in plugins.json) when it resolves the audio plugin |
| `GLEANN_VISION_PLUGIN_TOKEN` | plugin token | Bearer token sent to the vision plugin at `GLEANN_VISION_PLUGIN_URL`, set the same way |
| `GLEANN_MARKER_PLUGIN_URL` | `http://localhost:8766` | Base URL of the marker (PDF to text) plugin |
| `GLEANN_DOCLING_VLM` | — | Set to `1` by gleann (unless you set it) when the granite-docling model `~/.gleann/models/granite-docling/granite_docling_bitnet_dense.eifm` exists; gleann itself does not read it afterwards |

**Indexing**: When set, `gleann index build` processes images/audio/video files through the multimodal model, converting them to text descriptions for search.

**Chat**: Use `gleann ask <index> <question> --attach <file>` to analyze media during RAG queries.

## Plugin Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_PLUGIN_OWNER` | — | Default GitHub owner for plugin downloads |
| `GLEANN_GITHUB_API` | `https://api.github.com` | Root of the GitHub REST API used to look up plugin releases (GitHub Enterprise, a mirror or a test server) |
| `GLEANN_LAUNCHER` | — | Set by the self-extracting launcher of the full build to its own path; `gleann update` replaces that file and `gleann install` installs it. Do not set it by hand |

## Benchmarks

Read by `gleann benchmark` and the benchmark runner; they only add retrieval strategies or output.

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_BENCH_RERANK` | — | `1` adds reranked strategies to the benchmark, using the saved reranker settings |
| `GLEANN_BENCH_RERANK_MODEL` | saved setting | Reranker model for those strategies |
| `GLEANN_BENCH_RERANK_PROVIDER` | saved setting | Reranker provider for those strategies |
| `GLEANN_BENCH_RERANK_URL` | — | Base URL of a reranking server (for example `llama-server --reranking`); selects the Jina-compatible provider |
| `GLEANN_BENCH_RERANK_POOL` | `20` | Candidates sent to the reranker |
| `GLEANN_BENCH_RERANK_CHARS` | `500` | Characters of each candidate the reranker sees (`0` or more) |
| `GLEANN_BENCH_EXPAND_MODEL` | — | Ollama model for HyDE query expansion; adds HyDE strategies (and HyDE plus rerank when reranking is on) |
| `GLEANN_BENCH_PERTASK` | — | File to write one JSON line per (task, strategy) to, for paired significance tests |

## CLI ↔ Server Coordination

The memory store (`memory.sqlite`) can be opened by several processes at once, so the CLI does not need a running server. When a local `gleann serve` instance answers, the memory commands (`remember`, `forget`, `list`, `search`, `clear`, `add`, `stats`) still route through its REST API, so that a deployment can keep every write in one server process.

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_REMOTE_ADDR` | `http://localhost:8081` | Base URL of a running `gleann serve` instance. Set to `off` (or empty) to disable REST auto-routing and always open the local `memory.sqlite` |

When the auto-probe succeeds, affected commands print a `(via running server)` tag so it is obvious which path serviced the request.

## Index Format and Incremental Updates

Every index is format 2: one SQLite database `<index_dir>/<name>/<name>.db` with the passages, the persisted FTS5 lexical index (AND-first queries; in indexes of 10,000 passages or more, query words found in over 20% of the passages are dropped), the manifest and the code graph. `GLEANN_STORE_FORMAT`, `GLEANN_LEXICAL` and `GLEANN_LEXICAL_OPTS` were removed in v1.20: a value still set is ignored and gleann prints a one-line notice. Indexes built by gleann v1.9 or earlier are converted with `gleann index migrate <name>` (or rebuilt with `gleann index rebuild <name>`).

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_DELTA_MIN` / `GLEANN_DELTA_SHARE` | `4096` / `0.02` | Format-2 incremental updates (RFC 005): a sync keeps new vectors in a small delta file until it holds more than `max(GLEANN_DELTA_MIN, GLEANN_DELTA_SHARE x live passages)` entries; the sync that crosses the limit rebuilds the main vector file from the stored vectors (a merge, no re-embedding) |
| `GLEANN_DEAD_SHARE` / `GLEANN_DEAD_SHARE_PRUNED` / `GLEANN_DEAD_MIN` | `0.05` / `0.05` / `256` | Share of dead vector slots (removed or replaced passages) that triggers the same rebuild with dense slots, and the number of dead slots below which it never does; an explicit share sets the minimum to 0 unless `GLEANN_DEAD_MIN` is given |

## LLM Context Window

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_OLLAMA_NUM_CTX` | `32768` (only when `--no-limit` / `max_tokens=0`) | Force the `num_ctx` value sent to Ollama. When the user requests unbounded generation (`--no-limit`), Gleann widens the context window to avoid silent truncation of large RAG payloads. Set to a smaller value to cap VRAM usage; setting it in budgeted mode (`max_tokens>0`) also overrides the model default |
| `GLEANN_GRAPH_EXACT` | — | on | `0`/`false`/`off`/`no` turns off exact `go/types` call resolution in format-2 full graph builds (syntactic `go/ast` only) |

## Docker Example

```yaml
# docker-compose.yml
services:
  gleann:
    image: gleann:latest
    environment:
      GLEANN_INDEX_DIR: /data/indexes
      OLLAMA_HOST: http://ollama:11434
    volumes:
      - gleann-data:/data/indexes
```

## CI/CD Example

```bash
# GitHub Actions
env:
  OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
  OPENAI_BASE_URL: https://api.openai.com/v1
  GLEANN_INDEX_DIR: /tmp/gleann-indexes

# Build and search in CI
gleann setup --bootstrap
gleann index build docs --docs ./docs/
gleann search docs "API breaking changes" --json
```

## Shell Configuration

Add to your `~/.bashrc`, `~/.zshrc`, or `~/.config/fish/config.fish`:

```bash
# Use a remote Ollama server
export OLLAMA_HOST=http://gpu-server.local:11434

# Use OpenAI API key
export OPENAI_API_KEY=sk-...

# Custom index directory
export GLEANN_INDEX_DIR=/data/gleann/indexes
```

## Precedence

Settings are resolved in this order (highest priority first):

1. **CLI flags** — `gleann ask --model gpt-4 ...`
2. **Environment variables** — `OLLAMA_HOST=http://... gleann ask ...`
3. **Config file** — `~/.gleann/config.json`
4. **Built-in defaults** — Defined in source code
