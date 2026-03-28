# Environment Variables

Gleann can be configured via environment variables. These override values in `~/.gleann/config.json` but are overridden by CLI flags.

Useful for Docker, CI/CD, and environments where editing config files is impractical.

## Core Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `GLEANN_INDEX_DIR` | `index_dir` | `~/.gleann/indexes` | Root directory for all index data |
| `OLLAMA_HOST` | `ollama_host` | `http://localhost:11434` | Ollama server URL |

## Embedding Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `EMBEDDING_PROVIDER` | `embedding_provider` | `ollama` | Embedding provider: `ollama`, `openai` |
| `EMBEDDING_MODEL` | `embedding_model` | `bge-m3` | Embedding model name |
| `EMBEDDING_BATCH_SIZE` | `batch_size` | `32` | Batch size for embedding computation |
| `EMBEDDING_CONCURRENCY` | `concurrency` | `1` | Max concurrent embedding batches |

## LLM Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `LLM_PROVIDER` | `llm_provider` | `ollama` | LLM provider: `ollama`, `openai`, `anthropic` |
| `LLM_MODEL` | `llm_model` | `llama3.2` | LLM model for ask/chat |
| `OPENAI_API_KEY` | `openai_key` | — | OpenAI API key |
| `OPENAI_BASE_URL` | `openai_base_url` | — | OpenAI-compatible API base URL |

## Search Settings

| Variable | Config Key | Default | Description |
|----------|-----------|---------|-------------|
| `SEARCH_TOP_K` | `search.top_k` | `10` | Default number of search results |
| `SEARCH_RERANK` | `search.reranker` | `false` | Enable two-stage reranking |
| `SEARCH_RERANK_MODEL` | `search.reranker_model` | — | Reranker model name |

## Docker Example

```yaml
# docker-compose.yml
services:
  gleann:
    image: gleann:latest
    environment:
      GLEANN_INDEX_DIR: /data/indexes
      OLLAMA_HOST: http://ollama:11434
      EMBEDDING_PROVIDER: ollama
      EMBEDDING_MODEL: bge-m3
      LLM_PROVIDER: ollama
      LLM_MODEL: llama3.2
    volumes:
      - gleann-data:/data/indexes
```

## CI/CD Example

```bash
# GitHub Actions
env:
  EMBEDDING_PROVIDER: openai
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

# Use OpenAI for embeddings
export EMBEDDING_PROVIDER=openai
export OPENAI_API_KEY=sk-...

# Custom index directory
export GLEANN_INDEX_DIR=/data/gleann/indexes
```

## Precedence

Settings are resolved in this order (highest priority first):

1. **CLI flags** — `gleann ask --model gpt-4 ...`
2. **Environment variables** — `LLM_MODEL=gpt-4 gleann ask ...`
3. **Config file** — `~/.gleann/config.json`
4. **Built-in defaults** — Defined in source code
