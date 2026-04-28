# gleann HTTP API Audit (October 2025)

This audit captures the actual HTTP endpoint surface of the gleann server.
It is generated from `internal/server/server.go` and the mounted handler
groups (background tasks, unified memory, A2A protocol).

## Summary

- **Direct endpoints registered on root mux**: 33
- **Mounted router groups**: 5 endpoints (3 task + 2 unified-memory)
- **A2A protocol endpoints (mounted via `s.mountA2A`)**: 3
- **Total HTTP routes**: **41**

## Direct Endpoints

| # | Method | Path | Handler |
|---|--------|------|---------|
| 1 | GET | `/health` | handleHealth |
| 2 | GET | `/api/indexes` | handleListIndexes |
| 3 | GET | `/api/indexes/{name}` | handleGetIndex |
| 4 | POST | `/api/indexes/{name}/search` | handleSearch |
| 5 | POST | `/api/indexes/{name}/ask` | handleAsk |
| 6 | POST | `/api/indexes/{name}/build` | handleBuild |
| 7 | DELETE | `/api/indexes/{name}` | handleDeleteIndex |
| 8 | POST | `/api/search` | handleMultiSearch |
| 9 | GET | `/api/webhooks` | handleListWebhooks |
| 10 | POST | `/api/webhooks` | handleRegisterWebhook |
| 11 | DELETE | `/api/webhooks` | handleDeleteWebhook |
| 12 | GET | `/metrics` | handleMetrics |
| 13 | GET | `/api/conversations` | handleListConversations |
| 14 | GET | `/api/conversations/{id}` | handleGetConversation |
| 15 | DELETE | `/api/conversations/{id}` | handleDeleteConversation |
| 16 | GET | `/api/graph/{name}` | handleGraphStats |
| 17 | POST | `/api/graph/{name}/query` | handleGraphQuery |
| 18 | POST | `/api/graph/{name}/index` | handleGraphIndex |
| 19 | POST | `/api/memory/{name}/inject` | handleMemoryInject |
| 20 | DELETE | `/api/memory/{name}/nodes/{id}` | handleMemoryDeleteNode |
| 21 | DELETE | `/api/memory/{name}/edges` | handleMemoryDeleteEdge |
| 22 | POST | `/api/memory/{name}/traverse` | handleMemoryTraverse |
| 23 | GET | `/api/blocks/search` | handleSearchBlocks |
| 24 | GET | `/api/blocks/context` | handleBlockContext |
| 25 | GET | `/api/blocks/stats` | handleBlockStats |
| 26 | GET | `/api/blocks` | handleListBlocks |
| 27 | POST | `/api/blocks` | handleAddBlock |
| 28 | DELETE | `/api/blocks/{id}` | handleDeleteBlock |
| 29 | DELETE | `/api/blocks` | handleClearBlocks |
| 30 | GET | `/v1/models` | handleListModels |
| 31 | POST | `/v1/chat/completions` | handleChatCompletions |
| 32 | GET | `/api/openapi.json` | handleOpenAPISpec |
| 33 | GET | `/api/docs` | handleSwaggerUI |

## Mounted Groups

### Background Tasks (`mountBackgroundTasks`)

| Method | Path | Handler |
|--------|------|---------|
| GET | `/api/tasks` | handleListTasks |
| GET | `/api/tasks/{id}` | handleGetTask |
| DELETE | `/api/tasks` | handleCleanupTasks |

### Unified Memory (`mountUnifiedMemory`)

| Method | Path | Handler |
|--------|------|---------|
| POST | `/api/memory/ingest` | handleUnifiedIngest |
| POST | `/api/memory/recall` | handleUnifiedRecall |

### A2A Protocol (`mountA2A`)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/.well-known/agent-card.json` | Agent discovery |
| POST | `/a2a/v1/message:send` | Send message |
| GET | `/a2a/v1/tasks/{id}` | Get A2A task status |

## Memory Backends

The server fronts **three** distinct memory subsystems:

1. **Hierarchical memory blocks** — BBolt-backed, three-tier (short / medium /
   long), exposed under `/api/blocks/*`. Implemented in `pkg/memory`.
2. **Memory Engine** — Generic Entity / RELATES_TO knowledge graph backed by
   KuzuDB, exposed under `/api/memory/{name}/*`. Implemented in
   `internal/server/memory_handler.go` + KuzuDB driver.
3. **AST code graph** — KuzuDB-backed code graph (functions, classes, calls,
   imports) exposed under `/api/graph/{name}/*`. Implemented in
   `internal/server/graph_handler.go`.

Naming is **distinct** between these subsystems:
- "blocks" → BBolt hierarchical
- "memory engine" / "memory" → KuzuDB knowledge graph
- "graph" → KuzuDB code graph

No naming collisions exist; see `pkg/memory/` and `internal/graph/kuzu/`.

## Event Bus (Wired October 2025)

The server initializes a non-nil `eventbus.Bus` (Watermill GoChannel-backed)
in `NewServer` and exposes it via `Server.Bus()`. The following lifecycle
events are now published:

| Topic | Published From | Payload |
|-------|----------------|---------|
| `index.started` | handleBuild | index, count |
| `index.completed` | handleBuild | index, count, build_ms |
| `index.failed` | handleBuild | index, error |
| `search.completed` | handleSearch | index, query, top_k, results, query_ms |

Subscribers can register via `s.Bus().Subscribe(ctx, topic)` and receive
`*message.Message` values; payloads are JSON-encoded `map[string]any` and may
be decoded with `eventbus.DecodePayload`. The bus is closed automatically
during `Server.Stop`.
