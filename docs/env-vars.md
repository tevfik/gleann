# Environment Variables

Gleann can be configured via environment variables. These override values in `~/.gleann/config.json` but are overridden by CLI flags.

Useful for Docker, CI/CD, and environments where editing config files is impractical.

## Core Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_INDEX_DIR` | `index_dir` | `~/.gleann/indexes` | Root directory for all index data |
| `OLLAMA_HOST` | `ollama_host` | `http://localhost:11434` | Ollama server URL |
| `OLLAMA_API_KEY` | `ollama_api_key` | — | Optional Ollama API key, sent as `Authorization: Bearer <key>` (Ollama behind an auth proxy / hosted Ollama). Used only when `ollama_api_key` is not set |

## Authentication & Network

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_API_TOKEN` | — | — | Token the CLI/MCP clients send; on the server, a value of 16+ characters is also accepted as an admin token (Docker/CI) |
| `GLEANN_AUTH` | `auth` | `on` | `off` disables API authentication (loopback bind only) |
| `GLEANN_ALLOWED_HOSTS` | — | — | Extra `Host` names accepted on a loopback bind (comma separated) |
| `GLEANN_CORS_ORIGINS` | `cors_origins` | — | Browser origins allowed cross-origin (comma separated); none by default |
| — (config key) | `storage_mode` | `store` | `store` keeps vectors in the index (fast search); `recompute` keeps none and re-embeds on every search (slow). Manage with `gleann index storage` or the Index Manager TUI |

## LLM Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_LLM_PROVIDER` | `llm_provider` | `ollama` | LLM provider: `ollama`, `openai`, `anthropic` (read only by MCP server) |
| `GLEANN_LLM_MODEL` | `llm_model` | `nemotron-3-nano:4b` | LLM model for ask/chat (read only by MCP server) |
| `OPENAI_API_KEY` | `openai_api_key` | — | OpenAI API key |
| `OPENAI_BASE_URL` | `openai_base_url` | — | OpenAI-compatible API base URL |

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
| `GLEANN_TIMEOUT_BUILD_S` | `600` | Timeout in seconds for `/build` endpoints |
| `GLEANN_TIMEOUT_DEFAULT_S` | `60` | Default timeout in seconds for all other endpoints |
| `GLEANN_MAX_BODY_BYTES` | `16777216` | Maximum POST/PUT/PATCH body size in bytes (16 MiB default; `0` disables the cap) |
| `GLEANN_WEBHOOK_ALLOW_PRIVATE` | _(unset)_ | Set to `1` to allow webhook URLs whose hostname resolves to loopback / link-local / private addresses (off by default to block SSRF) |

Rate-limited requests receive `429 Too Many Requests` with a `Retry-After: 1` header. Timed-out requests receive `504 Gateway Timeout`. The `/health` and `/metrics` endpoints bypass rate limiting. SSE streaming endpoints bypass the timeout middleware.

## Background Tasks & Maintenance (Server Mode)

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_MAINTENANCE_ENABLED` | `true` | Enable background maintenance scheduler (`false` or `0` to disable) |
| `GLEANN_MAINTENANCE_INTERVAL_H` | `24` | Hours between maintenance runs (promotes medium→long, prunes expired blocks) |
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

## MCP Server Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_TOOLS` | `core` | MCP tool profile (read only by MCP): `core` (essential 10 tools), `full` (all tools), or comma-separated list of tool names/aliases |

## A2A Protocol (Agent-to-Agent)

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_A2A_ENABLED` | `true` | Enable A2A protocol endpoints (`false` or `0` to disable) |
| `GLEANN_A2A_BASE_URL` | auto-detected | Base URL for the A2A Agent Card (e.g. `https://my-host:8080`) |

## Multimodal Processing

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_MULTIMODAL_MODEL` | auto-detected | Ollama model for multimodal processing during indexing and chat (e.g. `gemma4:e4b`, `llava`). Auto-detects from available Ollama models if not set. CLI flag: `--multimodal-model` |

**Indexing**: When set, `gleann index build` processes images/audio/video files through the multimodal model, converting them to text descriptions for search.

**Chat**: Use `gleann ask <index> <question> --attach <file>` to analyze media during RAG queries.

## Plugin Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_PLUGIN_OWNER` | — | Default GitHub owner for plugin downloads |

## CLI ↔ Server Coordination

When a `gleann serve` instance is running, the bbolt memory store holds an exclusive write lock that blocks other CLI invocations (`remember`, `forget`, `list`, `search`, `clear`, `add`, `stats`). To avoid this contention, those commands first probe a local server and, if reachable, route through its REST API instead of opening the bbolt file directly.

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_REMOTE_ADDR` | `http://localhost:8080` | Base URL of a running `gleann serve` instance. Set to `off` (or empty) to disable REST auto-routing and always open the local bbolt store |

When the auto-probe succeeds, affected commands print a `(via running server)` tag so it is obvious which path serviced the request.

## Graph Storage Resilience

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_KUZU_AUTOREPAIR` | `1` (enabled) | When KuzuDB fails to open an existing on-disk database, automatically rename the corrupt directory to `<path>.corrupted-<unix-ts>` and recreate a fresh database. Set to `0` to disable auto-quarantine and surface the original open error instead. **Note**: quarantined data is retained on disk for manual recovery; nothing is deleted |

## LLM Context Window

| Variable | Default | Description |
|----------|---------|-------------|
| `GLEANN_OLLAMA_NUM_CTX` | `32768` (only when `--no-limit` / `max_tokens=0`) | Force the `num_ctx` value sent to Ollama. When the user requests unbounded generation (`--no-limit`), Gleann widens the context window to avoid silent truncation of large RAG payloads. Set to a smaller value to cap VRAM usage; setting it in budgeted mode (`max_tokens>0`) also overrides the model default |

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
