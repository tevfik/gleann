# Gleann REST API Reference

Gleann exposes a REST API when running in server mode (`gleann serve`). The API provides endpoints for index management, semantic search, RAG-based Q&A, and code graph queries.

## Quick Start

```bash
# Start the server
gleann serve --port 8080

# Open API documentation in browser
open http://localhost:8080/api/docs

# Download the OpenAPI spec
curl http://localhost:8080/api/openapi.json
```

## Interactive Documentation

When the server is running, interactive Swagger UI documentation is available at:

- **Swagger UI**: `GET /api/docs`
- **OpenAPI 3.0 JSON**: `GET /api/openapi.json`

## Endpoints

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |

### Index Management

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/indexes` | List all indexes |
| GET | `/api/indexes/{name}` | Get index metadata |
| POST | `/api/indexes/{name}/build` | Build index from texts/items |
| DELETE | `/api/indexes/{name}` | Delete an index |

### Search & RAG

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/indexes/{name}/search` | Semantic/hybrid search |
| POST | `/api/indexes/{name}/ask` | RAG-based Q&A |
| POST | `/api/search` | Multi-index search |

### Code Graph (requires treesitter build)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/graph/{name}` | Graph statistics |
| POST | `/api/graph/{name}/query` | Query the code graph |
| POST | `/api/graph/{name}/index` | Trigger AST graph indexing |

### Webhooks

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/webhooks` | List registered webhooks |
| POST | `/api/webhooks` | Register a webhook |
| DELETE | `/api/webhooks` | Delete a webhook by URL |

### Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/metrics` | Prometheus-compatible metrics |

### Conversations

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/conversations` | List saved conversations |
| GET | `/api/conversations/{id}` | Get conversation by ID |
| DELETE | `/api/conversations/{id}` | Delete a conversation |

## Examples

### Search

```bash
curl -X POST http://localhost:8080/api/indexes/my-code/search \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "how does authentication work",
    "top_k": 5,
    "hybrid_alpha": 0.7,
    "graph_context": true
  }'
```

### Ask (RAG)

```bash
curl -X POST http://localhost:8080/api/indexes/my-code/ask \
  -H 'Content-Type: application/json' \
  -d '{
    "question": "How is the user session managed?",
    "top_k": 10
  }'
```

**Ask request fields**:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `question` | string | *(required)* | Question to answer using RAG |
| `top_k` | integer | 10 | Number of context passages to retrieve |
| `llm_model` | string | — | LLM model name |
| `llm_provider` | string | — | LLM provider (`ollama`, `openai`, `anthropic`) |
| `system_prompt` | string | — | Custom system prompt for the LLM |
| `role` | string | — | Named role (e.g. `code`, `shell`). Resolves to a system prompt from the role registry. |
| `conversation_id` | string | — | Continue an existing conversation by ID. Restores message history. |
| `stream` | boolean | false | Enable SSE streaming |

### Ask with SSE Streaming

Stream tokens in real-time via Server-Sent Events:

```bash
curl -N -X POST http://localhost:8080/api/indexes/my-code/ask \
  -H 'Content-Type: application/json' \
  -d '{
    "question": "Explain the authentication flow",
    "stream": true
  }'
```

Or use the query parameter:

```bash
curl -N -X POST 'http://localhost:8080/api/indexes/my-code/ask?stream=true' \
  -H 'Content-Type: application/json' \
  -d '{"question": "Explain the authentication flow"}'
```

**Response format** (`Content-Type: text/event-stream`):

```
data: {"token":"The"}

data: {"token":" authentication"}

data: {"token":" flow"}

data: {"token":" works by..."}

data: [DONE]
```

Each `data:` line contains a JSON object with a `token` field. The stream ends with `data: [DONE]`. If an error occurs mid-stream, it sends `data: {"error": "..."}` before `[DONE]`.

**JavaScript example**:

```javascript
const response = await fetch('/api/indexes/my-code/ask', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ question: 'How does auth work?', stream: true })
});

const reader = response.body.getReader();
const decoder = new TextDecoder();

while (true) {
  const { done, value } = await reader.read();
  if (done) break;
  const text = decoder.decode(value);
  for (const line of text.split('\n')) {
    if (line.startsWith('data: ') && line !== 'data: [DONE]') {
      const { token } = JSON.parse(line.slice(6));
      process.stdout.write(token); // or append to UI
    }
  }
}
```

### Graph Query — Callers

```bash
curl -X POST http://localhost:8080/api/graph/my-code/query \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "callers",
    "symbol": "github.com/foo/bar.HandleLogin"
  }'
```

### Graph Query — Impact Analysis

```bash
curl -X POST http://localhost:8080/api/graph/my-code/query \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "impact",
    "symbol": "github.com/foo/bar.UserService",
    "max_depth": 3
  }'
```

### Build Index

```bash
curl -X POST http://localhost:8080/api/indexes/test-index/build \
  -H 'Content-Type: application/json' \
  -d '{
    "texts": ["Hello world", "foo bar baz"],
    "metadata": {"source": "test"}
  }'
```

### Multi-Index Search

Search across multiple indexes simultaneously. Results are merged by score:

```bash
# Search specific indexes
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "authentication flow",
    "indexes": ["backend-code", "frontend-code", "docs"],
    "top_k": 10
  }'

# Search ALL available indexes
curl -X POST http://localhost:8080/api/search \
  -H 'Content-Type: application/json' \
  -d '{
    "query": "authentication flow",
    "top_k": 10
  }'
```

**Response**:

```json
{
  "results": [
    {
      "index": "backend-code",
      "text": "func HandleLogin...",
      "score": 0.92,
      "metadata": {"source": "auth/handler.go"},
      "graph_context": {
        "symbols": [{"name": "HandleLogin", "kind": "function"}]
      }
    },
    {
      "index": "docs",
      "text": "Authentication uses JWT tokens...",
      "score": 0.88,
      "metadata": {"source": "auth.md"},
      "document_context": {
        "vpath": "auth.md",
        "name": "Authentication Overview",
        "summary": "This document explains the JWT flow..."
      }
    }
  ],
  "count": 2,
  "query_ms": 45
}
```

**CLI multi-search**:

```bash
# Search specific indexes (comma-separated)
gleann search backend-code,frontend-code "authentication"

# Search all indexes
gleann search --all dummy "authentication"
```

**CLI multi-index ask** (conversations work across multiple indexes):

```bash
# Ask across multiple indexes
gleann ask docs,backend-code "How does authentication work?"

# Pipe input with multi-index
cat auth.go | gleann ask backend,frontend "Review this auth handler"

# Continue a multi-index conversation
gleann ask docs,code --continue-last "What about the error handling?"

# Use a role
gleann ask my-code "Explain this module" --role explain --format markdown
```

### Webhooks

Register a webhook to receive POST notifications for events:

```bash
# Register a webhook
curl -X POST http://localhost:8080/api/webhooks \
  -H 'Content-Type: application/json' \
  -d '{
    "url": "https://your-server.com/gleann-hook",
    "events": ["build_complete", "index_deleted"],
    "secret": "optional-hmac-secret"
  }'

# List registered webhooks
curl http://localhost:8080/api/webhooks

# Delete a webhook
curl -X DELETE http://localhost:8080/api/webhooks \
  -H 'Content-Type: application/json' \
  -d '{"url": "https://your-server.com/gleann-hook"}'
```

**Webhook payload** (POST to your URL):

```json
{
  "event": "build_complete",
  "index": "my-code",
  "count": 1250,
  "buildMs": 3200,
  "timestamp": "2026-03-10T14:30:00Z"
}
```

If a `secret` is configured, payloads include an `X-Gleann-Signature` header with HMAC-SHA256 signature: `sha256=<hex>`.

**Supported events**: `build_complete`, `index_deleted`, `*` (all events).

### Metrics

Prometheus-compatible metrics endpoint:

```bash
curl http://localhost:8080/metrics
```

**Response** (text/plain, Prometheus exposition format):

```
# HELP gleann_up Whether the gleann server is running.
# TYPE gleann_up gauge
gleann_up 1

# HELP gleann_search_requests_total Total search requests.
# TYPE gleann_search_requests_total counter
gleann_search_requests_total 42

# HELP gleann_search_latency_avg_ms Average search latency in milliseconds.
# TYPE gleann_search_latency_avg_ms gauge
gleann_search_latency_avg_ms 23.50

# HELP gleann_multi_search_requests_total Total multi-index search requests.
# TYPE gleann_multi_search_requests_total counter
gleann_multi_search_requests_total 5

# HELP gleann_cached_searchers Number of cached searcher instances.
# TYPE gleann_cached_searchers gauge
gleann_cached_searchers 3
```

**Available metrics**: `gleann_up`, `gleann_uptime_seconds`, `gleann_search_requests_total`, `gleann_search_errors_total`, `gleann_search_latency_avg_ms`, `gleann_multi_search_requests_total`, `gleann_build_requests_total`, `gleann_build_errors_total`, `gleann_build_latency_avg_ms`, `gleann_ask_requests_total`, `gleann_delete_requests_total`, `gleann_webhooks_fired_total`, `gleann_cached_searchers`.

**Grafana / Prometheus integration**: Point your Prometheus scraper at `http://<host>:8080/metrics`.

### Conversations

Manage saved conversation history:

```bash
# List all conversations
curl http://localhost:8080/api/conversations

# Get a specific conversation by ID (full or prefix)
curl http://localhost:8080/api/conversations/a1b2c3d4

# Delete a conversation
curl -X DELETE http://localhost:8080/api/conversations/a1b2c3d4

# Ask with a role
curl -X POST http://localhost:8080/api/indexes/my-code/ask \
  -H 'Content-Type: application/json' \
  -d '{"question": "Review this code", "role": "code"}'

# Continue an existing conversation
curl -X POST http://localhost:8080/api/indexes/my-code/ask \
  -H 'Content-Type: application/json' \
  -d '{"question": "What about error handling?", "conversation_id": "a1b2c3d4..."}'
```

## CORS

All endpoints include CORS headers for cross-origin access:

```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET, POST, DELETE, OPTIONS
Access-Control-Allow-Headers: Content-Type, Authorization
```

## Error Responses

All errors return JSON with a single `error` field:

```json
{
  "error": "index \"foo\" not found: open .../foo/meta.json: no such file or directory"
}
```

Common HTTP status codes:

| Code | Meaning |
|------|---------|
| 200 | Success |
| 400 | Bad request (missing required fields) |
| 404 | Index or graph not found |
| 500 | Internal server error |
| 503 | Feature unavailable (e.g., graph without treesitter build tag) |
