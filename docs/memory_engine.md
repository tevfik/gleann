# Memory Engine — Generic Knowledge Graph

gleann's **Memory Engine** transforms the system from a closed RAG box into a
generic knowledge graph backend that external AI agents (Cursor, Claude, custom
agents) can write to, read from, and traverse — without coupling to gleann's
internal RAG pipeline.

## Concepts

### Entity Node

A labeled property node stored in the memory database (`memory.sqlite`).

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | string | ✅ | Globally unique, stable key (UUID or slug) |
| `type` | string | ✅ | Semantic category (e.g. `requirement`, `concept`, `code_symbol`) |
| `content` | string | optional | Natural-language text → triggers HNSW vector embedding |
| `attributes` | object | optional | Arbitrary JSON metadata (stored as a JSON string) |

### RELATES_TO Edge

A directed, labeled, weighted relationship between two Entity nodes.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `from` | string | ✅ | Source entity ID |
| `to` | string | ✅ | Destination entity ID |
| `relation_type` | string | ✅ | Semantic edge label (e.g. `DEPENDS_ON`, `IMPLEMENTS`) |
| `weight` | float64 | optional | Numeric strength (default `1.0`) |
| `attributes` | object | optional | Arbitrary edge metadata |

> **Temporal attributes** — When edges are created via `InjectEntities()`,
> the engine **auto-injects** `created_at` (set once, on first insert) and
> `updated_at` (refreshed on every upsert) into the edge `attributes` JSON.
> Both values use RFC 3339 / UTC format (e.g. `"2025-07-15T12:00:00Z"`).
> You may supply additional attributes alongside these; they will be merged.

### Storage Layout

All Memory Engine stores live in the memory database, beside the long-term
memory blocks:

```
~/.gleann/memory/memory.sqlite      (or $GLEANN_MEMORY_DIR/memory.sqlite)
```

Each store is a namespace in that database: its name is the `ns` column of
every row. Stores are isolated from each other, from gleann's RAG indexes and
from the code graph. The Memory Engine works in every build, including the
`go install` build and Windows.

Older releases kept each store in a KuzuDB database `<index-dir>/<name>_memory`.
gleann no longer reads it; see [Upgrading entities from v1.9](#upgrading-entities-from-v19).

## Schema

```sql
CREATE TABLE entities (ns TEXT NOT NULL, id TEXT NOT NULL, type TEXT NOT NULL, content TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}', PRIMARY KEY (ns, id)) WITHOUT ROWID;
CREATE TABLE relations (ns TEXT NOT NULL, src TEXT NOT NULL, dst TEXT NOT NULL, relation_type TEXT NOT NULL,
  weight REAL NOT NULL DEFAULT 1.0, attributes TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (ns, src, dst, relation_type)) WITHOUT ROWID;
CREATE INDEX relations_dst ON relations(ns, dst);
CREATE TABLE entity_archive (archive_id TEXT PRIMARY KEY, ns TEXT NOT NULL, id TEXT NOT NULL,
  version_id TEXT NOT NULL, type TEXT, content TEXT, attributes TEXT, valid_to TEXT) WITHOUT ROWID;
```

The tables are added by a named schema step the first time the memory
database is opened.

## REST API

Start the server with `gleann serve`, then use the four Memory Engine endpoints:

### POST `/api/memory/{name}/inject`

Atomically upsert a batch of nodes and edges. Every row is an upsert, so the
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
`VectorSyncer` after the database transaction commits. This keeps the entity
searchable via `gleann_search` in addition to graph traversal.

**Interface:**
```go
type VectorSyncer interface {
    AddContent(ctx context.Context, nodeID, content string, attrs map[string]any) error
    DeleteContent(ctx context.Context, nodeID string) error
}
```

A syncer that can write several entities in one index publication also
implements the optional extension below. `InjectEntities` then calls
`AddContents` once for the whole inject request instead of `AddContent` per
entity (one publication per request, RFC 005):

```go
type VectorBatchSyncer interface {
    AddContents(ctx context.Context, contents []VectorContent) error
}
```

The built-in `memsync.LeannSyncer` implements both; a syncer that implements
only `VectorSyncer` keeps the per-entity path. In both paths an entity repeated
within one inject request is published with its **last** content only, matching
the last-wins database upsert; if that last occurrence has empty content, the
entity is not published at all (an earlier text from the same request is never
resurrected). Clearing an entity in a later request still leaves its previously
published passage in the index, as before.

Pass `nil` when constructing a `MemoryService` to disable vector sync entirely
(graph-only mode). The REST layer (`internal/server/memory_syncer.go`,
`initMemorySyncer`) and the MCP layer (`internal/mcp/memory_syncer.go`,
`wireMemorySyncer`) wire a `memsync.LeannSyncer` into every memory pool
whenever an embedding model is configured; the syncer is `nil` only when no
embedding model is available or the builder cannot be created.

## Transaction Safety

`InjectEntities` writes every node, archived version and edge in one SQLite
transaction (`BEGIN IMMEDIATE`). If any statement fails, nothing is committed.
An edge whose endpoints do not exist is skipped, not an error. Before a node is
updated, its previous version is copied to `entity_archive`.

Vector sync runs **after** the commit, so a sync failure leaves the graph
durable while the vector store may be temporarily out of sync — retry the
injection to reconcile.

## Concurrency

SQLite lets several processes (the MCP server, `gleann serve`) write the same
memory database; their transactions are serialised by the database. Within one
process each store holds a mutex across a write and its vector sync, so the
vector index sees updates of one entity in commit order. Reads (`Traverse`) do
not take the mutex.

`Traverse` is a recursive SQL query that follows edges in their direction up to
the requested depth (at most 10). It visits each entity at most once per depth,
so cycles and densely linked entities stay cheap.

## Code Map

| File | Role |
|------|------|
| `pkg/gleann/types.go` | `MemoryGraphNode`, `MemoryGraphEdge`, `GraphInjectionPayload`, `AttributesToJSON`, `JSONToAttributes` |
| `pkg/memory/sqlite.go` | `entities`, `relations`, `entity_archive` tables (schema step `entity_graph`) |
| `pkg/memory/entity_service.go` | `EntityService`: `InjectEntities`, `DeleteEntity`, `DeleteEdge`, `Traverse` |
| `pkg/gleann/interfaces.go` | `MemoryService` and `VectorSyncer` interfaces |
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

---

## Hierarchical Project Scope (path-style memory)

Scopes can be **path-style** (`org/project/repo` or `myorg/backend/auth`).
When you query for a deep scope, the manager automatically pulls in **every
ancestor scope plus global memories**:

| Block scope                       | Visible when querying `myorg/backend/auth` |
|-----------------------------------|---------------------------------------------------|
| `""` (global)                     | ✅ |
| `myorg`                           | ✅ |
| `myorg/backend`                   | ✅ |
| `myorg/backend/auth`              | ✅ |
| `myorg/frontend`                  | ❌ (sibling branch) |
| `different-project`               | ❌ |

**Asymmetric inheritance** — children inherit parent context, but a parent
query never pulls in descendant blocks (so a project-wide review is not
polluted by feature-specific notes).

**Conflict marking follows the same visibility rule.** When a new block is
written, contradiction checks run only against blocks the writer can see:
global blocks, the writer's own scope and its path ancestors. A write never
lowers the validity of a block in a scope the writer cannot see — a global
write conflicts only with global blocks, and an `org` write never flags
blocks stored under `org/repo`. Sibling scopes never see each other's blocks.

### Example

```bash
# Add a project-wide convention (visible to every sub-scope below it)
gleann memory remember "We use conventional commits" --scope "myorg"

# Add a feature-specific note
gleann memory remember "Issue #42 needs Turkish translation" \
  --scope "myorg/backend/auth"

# Build context for the feature scope -> sees BOTH facts
gleann memory context --scope "myorg/backend/auth"
```

Scoped writes go through `memory remember --scope`: `memory add <tier>` takes
only `--tag` and `--label`, and `memory context` filters by `--scope`,
`--command`, `--path` and `--symbol` (it has no `--query` flag).

Flat (single-segment) scopes still behave exactly as before — the change
is fully backwards-compatible. The hierarchy is implemented at the filter
layer (`scopeAncestors`) so no schema migration is needed.

### Automatic repository scope

`gleann ask` (including `--interactive` and `--agent`), the chat TUI, its agent
view, and the MCP `memory_context`, `memory_search`, `memory_list` and
`memory_remember` tools default to the current repository's scope. The scope is
detected from the process working directory: the normalized git remote origin
(`github.com/org/repo`), else the repository root name, else the directory
basename, else `"global"` for filesystem roots. Global blocks and path-style
ancestors stay visible; sibling repositories do not. `gleann memory context`
without `--scope` does the same (the SessionStart hook installed by
`gleann install claude` relies on it, so a session sees only its own
repository's memory); outside a repository it shows the global blocks and the
blocks scoped to the directory name. `gleann memory context --scope all` shows
every scope. `GET /api/blocks/context` without a scope remains global for API
compatibility.

---

## Memory Compaction & Bayesian Pruning (`gleann memory compact`)

As conversations and autonomous agent interactions accumulate, memory blocks may become outdated, contradictory, or superseded. Gleann employs a **Bayesian Validity Score** to model confidence:

- **Confirmations**: Incremented when a fact is observed again without contradiction.
- **Conflicts**: Incremented when an agent observes opposing statements.
- **Recency Decay**: Temporal half-life decay (90-day half-life) discounts very old, unreferenced **short- and medium-term** memories.

**Long-term facts never decay or expire by age.** A `long` block keeps its confidence at any age — an unconfirmed fact holds its 0.5 prior after 130 days, a year, or a decade — and is never reported stale by age. The long tier stays changeable, not immortal: facts can still be edited or merged (`gleann memory add` of the same fact confirms it), deleted (`gleann memory forget`), marked suspect when referenced code changes, or drop below the prune threshold when contradicted (`conflicts` outweigh `confirms`). An explicitly set `expires_at` is honored in every tier, including long; it is caller intent, not age. Short- and medium-term lifetime policy (per-fact TTL, user setting, fixed default) is drafted in [RFC 009](plans/009-memory-tier-lifetime.md).

### Automated Context Filtering & Compaction

1. **At Retrieval Time**: Memories with a validity score $< 0.20$ are automatically omitted during `BuildContext()` / `BuildScopedContext()`, preventing hallucinations and context pollution. Long-term blocks reach that threshold only through conflicts or an explicit expiry, never through age.
2. **On-Demand Compaction**: Run `gleann memory compact` to permanently purge blocks below a threshold:

```bash
# Compact memory by removing unreliable and decayed blocks (default: min-validity 0.2)
gleann memory compact

# Custom threshold compaction
gleann memory compact --min-validity 0.35
```

The operation works seamlessly through the active `gleann serve` REST endpoint (`POST /api/blocks/compact`) to prevent database lock collisions.

---

## Memory Files: Markdown Export & Import (`gleann memory export`, `gleann memory import`)

Memory blocks and conversation digests can be exported to and imported from individual Markdown files with YAML front matter:

```bash
# Export all memory blocks and summaries to a directory
gleann memory export --dir ./memory-backup

# Import memory blocks and summaries from a directory
gleann memory import --dir ./memory-backup
```

### File Structure & Front Matter

- **Memory Blocks**: Each memory block is saved as an individual `.md` file in `--dir` with a deterministic, Windows-safe filename (`<id>-<hash>.md`: the readable ID, sanitized and shortened to 64 characters, plus 8 hex characters of a hash of the exact ID, so IDs that differ only in case or in characters Windows forbids never share a file). The YAML front matter carries every Block field (`id`, `tier`, `label`, `source`, `tags`, `metadata`, `created_at`, `updated_at`, `expires_at`, `char_limit`, `scope`, `repo`, `paths`, `symbols`, `commit_hash`, `suspect`, `stale_reason`, `confirms`, `conflicts`, `access_count`, `last_accessed_at`), while the markdown body holds the block content.
- **Conversation Summaries**: Stored in a `summaries/` subdirectory within `--dir` (`summaries/<timestamp>_<conv_id>-<hash>.md`), with summary metadata in YAML front matter and the summary text as body.
- **Lossless & Idempotent**: Re-importing unchanged files changes nothing. When an existing block with the same ID conflicts, the version with the newer `updated_at` timestamp is kept, and the conflict count is reported.

---

## Sleep-Time Compute & Memory Reflection (`gleann memory reflect`)

The sleep-time compute engine periodically reflects on recent conversations, summarizes discussions, extracts reusable knowledge, and updates long-term memory without blocking interactive flows.

### Reflection as Diff & Change Sets

Reflection writes memories through the standard manager path (`RememberBlock`), applying deduplication, character limits, validity scoring, and contradiction checks. Original memory blocks are never deleted or overwritten during reflection. Each cycle records a change set in SQLite:
- **Run ID**: Unique identifier for the reflection run (e.g. `reflect-<timestamp>`).
- **Timestamp**: Time of execution.
- **Blocks Added**: IDs of newly created memory blocks.
- **Blocks Superseded**: IDs of existing memory blocks contradicted by new information.
- **Blocks Confirmed**: the confirmations the run made to pre-existing blocks (each records the block ID and its prior confirmation count and timestamp), so a revert can undo exactly those confirmations.

The cursor tracking processed conversations (`sleeptime_last_run`) is persisted in `memory_meta` across server restarts, alongside a per-conversation progress record (`sleeptime_conv_progress`: attempts made and the version last resolved). Conversations are processed oldest-first (by update time); each cycle handles at most `GLEANN_SLEEPTIME_MAX_CONVS` of them. A conversation is eligible when it is at or after the cursor, when it has a retry still pending, or when it was updated since it was last resolved — so a failed conversation tied with the cursor timestamp, or older than it after the clock went backwards, is still retried instead of stranded. A conversation whose summarization or extraction fails is retried on later cycles and abandoned after **3 attempts in total**; the attempt count is persisted, so repeated `gleann memory reflect` runs (each a fresh engine) cannot reset it, and a permanently failing conversation cannot block the queue forever. A failure does **not** stop the rest of the cycle: the other conversations are still processed, because retry eligibility is tracked per conversation rather than gated on the cursor. A duplicate of an existing block only confirms that block — reflection never replaces its text. Only one reflection cycle runs at a time: a concurrent `reflect` call (REST, CLI or the background cycle) returns an "already running" answer instead of racing on shared state.

### Commands

```bash
# Run one reflection cycle immediately
gleann memory reflect

# Preview changes without modifying the database (dry-run)
gleann memory reflect --dry-run

# Revert a reflection run by ID (removes added blocks and undoes the run's
# confirmations; leaves original blocks otherwise untouched)
gleann memory reflect --revert <run-id>
```

## Failure Memory

Agents tend to repeat a failure in the next session after they found the fix.
A failure memory records the command, the error and the fix, with the files
and symbols involved:

```bash
gleann memory failure add --command "go test ./pkg/x" \
  --error "pkg/x/a_test.go:12: undefined: helper" \
  --fix "build with -tags integration" --path pkg/x/a_test.go
gleann memory failure list
gleann memory context --command "go test ./pkg/x" --path pkg/x/a_test.go
```

A failure is a long-term block with label `failure`; the command, the
normalized error signature and the fix are in its metadata. The signature
drops file paths, line and column numbers, hex addresses and timestamps, and
the block ID is a hash of the normalized command and the signature, so the
same failure recorded again confirms the existing block (`confirms` goes up,
paths and symbols are merged) instead of adding a second one.

Failures are not part of the regular memory tiers in the context window. They
are added in their own `failure_memory` section when the caller names the work:
a command (matched after normalization, as a prefix in either direction) or
files (matched by path or base name). The section counts against the
injection budget and ranks like long-term memory. MCP: `memory_failure_add`
records one; `memory_context` accepts `command` and `paths`.

## Upgrading entities from v1.9

gleann v1.9.x kept the entities of `inject_knowledge_graph` in a KuzuDB store
`<index-dir>/<name>_memory`. With the Kùzu binding v1.9 uses (go-kuzu 0.11)
that store is a single file (plus `.wal`/`.shadow` while a server runs), not a
directory; stores created before the temporal archive existed lack the
`Entity_Archive` table. The entities cannot be rebuilt from sources, so they
are carried over in two steps. gleann does not read Kùzu itself.

1. Export, with the separate tool (own Go module with the Kùzu binding, CGo).
   Download `kuzu-memory-export-<version>-<os>-<arch>.tar.gz` from the
   release assets (linux-amd64, linux-arm64, darwin-arm64; Kùzu had no Windows
   build in gleann). The archive holds the binary and the Kùzu library
   (`libkuzu.so` / `libkuzu.dylib`); keep them in the same folder. Or build it
   with `make kuzu-memory-export`, which writes `bin/kuzu-memory-export`:

   ```bash
   kuzu-memory-export -o gleann-memory-export ~/.gleann/indexes/myproj_memory
   kuzu-memory-export -o gleann-memory-export ~/.gleann/indexes   # every *_memory store in the folder
   ```

   The tool opens the store read-only (a store with a pending write-ahead log is
   copied to a temporary folder first), never writes to your files, and writes
   `<name>.kuzu-export.json` per store: `{format, version, namespace, entities,
   relations, archive}` with every attribute and timestamp as stored.
2. Import into `memory.sqlite`:

   ```bash
   gleann memory import-graph gleann-memory-export/myproj.kuzu-export.json   # or the folder
   ```

   One transaction per namespace, `INSERT OR IGNORE` row by row: entity ids,
   attributes, edge timestamps and archive rows (with their `valid_to`) are kept
   as exported, rows that already exist in SQLite win, and nothing is re-archived
   or re-stamped. An import marker `graph_import:<name>` (file path and SHA-256)
   is stored in `memory_meta`; importing the same file again changes nothing.
   Archive ids are stored as `<name>/<archive_id>`, like the ones gleann creates.

Until a store is imported, the server and the MCP server log once per process
the two commands for it, and `gleann doctor` lists unimported stores. The old
`<name>_memory` file stays where it is; delete it when you are done.

`traverse_knowledge_graph` on the imported entities returns what v1.9.0 returned
for the same store, compared as sets (v1.9.0 does not order its results). One
v1.9.0 answer is not reproduced because it was wrong: for the stored edge
`ghost -RELATED_TO-> unicode` v1.9.0's traverse reported `ghost -> note1 WROTE`.
The store holds the correct edge, and the import and SQLite traverse give it.
