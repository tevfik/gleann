# Memory Engine — Generic Knowledge Graph

gleann's **Memory Engine** transforms the system from a closed RAG box into a
generic knowledge graph backend that external AI agents (Yaver, Claude, custom
agents) can write to, read from, and traverse — without coupling to gleann's
internal RAG pipeline.

## Concepts

### Entity Node

A labeled property node stored in KuzuDB.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | ✅ | Globally unique, stable key (UUID or slug) |
| `type` | string | ✅ | Semantic category (e.g. `requirement`, `concept`, `code_symbol`) |
| `content` | string | optional | Natural-language text → triggers HNSW vector embedding |
| `attributes` | object | optional | Arbitrary JSON metadata (serialised as a JSON string in KuzuDB) |

### RELATES_TO Edge

A directed, labeled, weighted relationship between two Entity nodes.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `from` | string | ✅ | Source entity ID |
| `to` | string | ✅ | Destination entity ID |
| `relation_type` | string | ✅ | Semantic edge label (e.g. `DEPENDS_ON`, `IMPLEMENTS`) |
| `weight` | float64 | optional | Numeric strength (default `1.0`) |
| `attributes` | object | optional | Arbitrary edge metadata |

### Storage Layout

Each Memory Engine store is an **independent KuzuDB database** stored at:

```
<index-dir>/<store-name>_memory/
```

This means the Memory Engine is fully **isolated** from gleann's RAG indexes
and from the AST/document code graph (`<store-name>_graph/`).

## KuzuDB Schema

```cypher
CREATE NODE TABLE IF NOT EXISTS Entity (
    id         STRING,
    type       STRING,
    content    STRING,
    attributes STRING,   -- compact JSON, e.g. {"priority":"high"}
    PRIMARY KEY (id)
)

CREATE REL TABLE IF NOT EXISTS RELATES_TO (
    FROM Entity TO Entity,
    relation_type STRING,
    weight        DOUBLE,
    attributes    STRING   -- compact JSON
)
```

The schema is created lazily on first access via `kgraph.Open()`.  Because
`IF NOT EXISTS` is used, the schema init is re-entrant and safe to call on
an already-initialized database.

## REST API

Start the server with `gleann serve`, then use the four Memory Engine endpoints:

### POST `/api/memory/{name}/inject`

Atomically upsert a batch of nodes and edges. Uses Cypher `MERGE` so the
operation is **idempotent** — re-submitting the same payload is safe.

**Request body — `GraphInjectionPayload`:**

```jsonc
{
  "nodes": [
    {
      "id": "req-001",
      "type": "requirement",
      "content": "User must log in with email and password",
      "attributes": {"priority": "high", "sprint": 3}
    }
  ],
  "edges": [
    {
      "from": "req-001",
      "to": "feat-jwt",
      "relation_type": "IMPLEMENTED_BY",
      "weight": 1.0
    }
  ]
}
```

**Response:**
```json
{"ok": true, "nodes_sent": 1, "edges_sent": 1}
```

> **Note:** Edges whose endpoints do not yet exist in the graph are silently
> skipped (`MATCH … MERGE` on a zero-result `MATCH` is a no-op).

### DELETE `/api/memory/{name}/nodes/{id}`

Remove the entity identified by `{id}` together with **all its incident
RELATES_TO edges** (`DETACH DELETE` semantics). Deleting a non-existent node
is a no-op and returns `200 OK`.

```bash
curl -X DELETE http://localhost:8080/api/memory/project/nodes/req-001
```

**Response:**
```json
{"ok": true, "deleted_id": "req-001"}
```

### DELETE `/api/memory/{name}/edges`

Remove a single RELATES_TO relationship. Other edges between the same node pair
under different `relation_type` values are not affected.

```bash
curl -X DELETE http://localhost:8080/api/memory/project/edges \
  -H 'Content-Type: application/json' \
  -d '{"from": "req-001", "to": "feat-jwt", "relation_type": "IMPLEMENTED_BY"}'
```

### POST `/api/memory/{name}/traverse`

Walk RELATES_TO edges starting from `start_id` up to `depth` hops (default 1,
max 10). Returns all reachable nodes and the intra-subgraph edges.

```bash
curl -X POST http://localhost:8080/api/memory/project/traverse \
  -H 'Content-Type: application/json' \
  -d '{"start_id": "req-001", "depth": 2}'
```

**Response — `TraverseResponse`:**
```json
{
  "nodes": [
    {"id": "req-001", "type": "requirement", "content": "..."},
    {"id": "feat-jwt",  "type": "code_symbol"}
  ],
  "edges": [
    {"from": "req-001", "to": "feat-jwt", "relation_type": "IMPLEMENTED_BY", "weight": 1}
  ],
  "count": 2
}
```

## MCP Tools

Configure gleann as an MCP server in your AI editor. Three Memory Engine tools
are available alongside the RAG tools.

### `inject_knowledge_graph`

Inject nodes and edges. Idempotent (`MERGE` semantics).

```json
{
  "index": "project",
  "nodes": [
    {"id": "req-001", "type": "requirement", "content": "Login via email"},
    {"id": "feat-jwt", "type": "code",       "content": "JWT handler"}
  ],
  "edges": [
    {"from": "req-001", "to": "feat-jwt", "relation_type": "IMPLEMENTED_BY"}
  ]
}
```

### `delete_graph_entity`

Remove an entity and all its incident edges.

```json
{"index": "project", "id": "req-001"}
```

### `traverse_knowledge_graph`

Explore the graph from a starting node.

```json
{"index": "project", "start_id": "req-001", "depth": 3}
```

**Example agent query:** *"Which code symbols implement requirement req-001?"*

```
Traversal from "req-001" (depth 3) — 3 nodes, 2 edges

=== Nodes ===
  [*] id="req-001" type="requirement" content="Login via email..."
  [ ] id="feat-jwt"  type="code" content="JWT handler"
  [ ] id="impl-db"   type="code" content="DB session store"

=== Edges ===
  "req-001" -[IMPLEMENTED_BY]-> "feat-jwt" (weight=1.00)
  "req-001" -[IMPLEMENTED_BY]-> "impl-db"  (weight=1.00)
```

## Vector Synchronisation

When an Entity node has a non-empty `content` field, gleann calls an optional
`VectorSyncer` after the KuzuDB transaction commits. This keeps the entity
searchable via `gleann_search` in addition to graph traversal.

**Interface:**
```go
type VectorSyncer interface {
    AddContent(ctx context.Context, nodeID, content string, attrs map[string]any) error
    DeleteContent(ctx context.Context, nodeID string) error
}
```

Pass `nil` when constructing a `MemoryService` to disable vector sync entirely
(graph-only mode). The REST and MCP layers currently use `nil` (no syncer) by
default; a future release will wire in a `LeannBuilder`-backed syncer.

## Transaction Safety

`InjectEntities` wraps all node and edge upserts in a single manual KuzuDB
transaction:

```
BEGIN TRANSACTION
  MERGE (e:Entity {id: "req-001"}) SET ...
  MERGE (e:Entity {id: "feat-jwt"}) SET ...
  MERGE (a)-[r:RELATES_TO {relation_type: "IMPLEMENTED_BY"}]->(b) SET ...
COMMIT          ← on success
ROLLBACK        ← on any error (automatic via ExecTxOn)
```

If the transaction is interrupted mid-way, no partial data is committed.
Vector sync runs **after** the commit, so a sync failure leaves the graph
durable while the vector store may be temporarily out of sync — retry the
injection to reconcile.

## Concurrency

`MemoryService` embeds a `sync.Mutex` that serialises all write operations.
KuzuDB supports only one write transaction at a time on a database; the mutex
prevents concurrent callers from racing into a transaction.

Read operations (Traverse) open an independent connection via `db.NewConn()`,
so reads proceed concurrently with each other but are blocked by in-progress
writes from the mutex.

## Code Map

| File | Role |
|------|------|
| `pkg/gleann/types.go` | `MemoryGraphNode`, `MemoryGraphEdge`, `GraphInjectionPayload`, `AttributesToJSON`, `JSONToAttributes` |
| `internal/graph/kuzu/db.go` | `Entity` and `RELATES_TO` DDL added to `initSchema()` |
| `internal/graph/kuzu/memory_service.go` | `VectorSyncer`, `MemoryService`, `InjectEntities`, `DeleteEntity`, `DeleteEdge`, `Traverse` |
| `internal/server/memory_handler.go` | HTTP handlers + `memoryPool`, routes registered in `server.go` |
| `internal/server/openapi.go` | OpenAPI 3.0 schemas and path specs for the four endpoints |
| `internal/mcp/memory_tools.go` | `inject_knowledge_graph`, `delete_graph_entity`, `traverse_knowledge_graph` MCP tools + `mcpMemoryPool` |

## Example: Requirement Traceability

Link product requirements to code symbols and then ask the agent:
*"Which files do I need to touch to change requirement req-login?"*

```bash
# 1. Inject requirements and code nodes
curl -X POST localhost:8080/api/memory/myproject/inject -d '{
  "nodes": [
    {"id": "req-login",  "type": "requirement", "content": "User login with OAuth"},
    {"id": "req-signup", "type": "requirement", "content": "User registration"},
    {"id": "sym-oauth",  "type": "code", "content": "OAuthHandler in auth/handler.go"},
    {"id": "sym-user",   "type": "code", "content": "UserRepo in db/user.go"}
  ],
  "edges": [
    {"from": "req-login",  "to": "sym-oauth", "relation_type": "IMPLEMENTED_BY"},
    {"from": "req-login",  "to": "sym-user",  "relation_type": "IMPLEMENTED_BY"},
    {"from": "req-signup", "to": "sym-user",  "relation_type": "IMPLEMENTED_BY"}
  ]
}'

# 2. Traverse from req-login (depth 1 → direct impacted symbols)
curl -X POST localhost:8080/api/memory/myproject/traverse \
  -d '{"start_id": "req-login", "depth": 1}'

# → Returns sym-oauth and sym-user with their IMPLEMENTED_BY edges
```

Or via `traverse_knowledge_graph` MCP tool directly inside Cursor / Claude:

```json
{
  "index": "myproject",
  "start_id": "req-login",
  "depth": 2
}
```
