# MCP Server Guide & Platform Install

Gleann includes a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that exposes your indexed knowledge base to AI editors like Cursor, Windsurf, Claude Desktop, and others.

## One-command Platform Setup

The `gleann install` command auto-detects your AI coding platform and writes all required integration files (AGENTS.md, MCP config, platform-specific plugins) in one step.

```bash
# Auto-detect and install for all platforms found in the current directory
gleann install

# Install for a specific platform
gleann install --platform opencode
gleann install --platform claude
gleann install --platform cursor
gleann install --platform codex
gleann install --platform gemini
gleann install --platform claw      # OpenClaw
gleann install --platform aider
gleann install --platform copilot   # GitHub Copilot CLI

# List all supported platforms and detection status
gleann install --list

# Remove integration files
gleann install uninstall
gleann install uninstall --platform cursor
```

### What gets written per platform

| Platform | Files written | Integration type |
|----------|--------------|-----------------|
| **opencode** | `AGENTS.md` · `.opencode/plugins/gleann.js` · `.opencode/mcp.json` · `opencode.json` | `tool.execute.before` plugin hook |
| **claude** | `CLAUDE.md` · `~/.claude/settings.json` | `PreToolUse` hook (Glob/Grep/Read) |
| **cursor** | `.cursor/rules/gleann.mdc` · `.cursor/mcp.json` | `alwaysApply: true` rules file |
| **codex** | `AGENTS.md` · `.codex/hooks.json` | `PreToolUse` on Bash |
| **gemini** | `GEMINI.md` · `.gemini/settings.json` | `BeforeTool` hook |
| **claw** | `AGENTS.md` · `~/.openclaw/skills/gleann/SKILL.md` | Always-on skill |
| **aider** | `AGENTS.md` | Always-on AGENTS.md |
| **copilot** | `~/.copilot/skills/gleann/SKILL.md` | CLI skill |

### OpenCode in depth

OpenCode is the only platform that receives a JavaScript plugin (`.opencode/plugins/gleann.js`) which fires automatically before every `bash` tool call. If `GRAPH_REPORT.md` exists in the project root, the plugin injects the first 3 000 characters of its content into the context window so the AI sees god nodes and community structure before it starts searching source files.

To generate the report:

```bash
gleann graph report --index <name>   # writes GRAPH_REPORT.md
```

Register the MCP server in OpenCode for inline tool access:

```json5
// .opencode/mcp.json
{
  "mcpServers": {
    "gleann": { "type": "stdio", "command": "gleann", "args": ["mcp"] }
  }
}
```

`gleann install --platform opencode` writes both files automatically.

---

## Manual MCP Configuration

If you prefer to configure the MCP server manually (without `gleann install`):

## Editor Configuration

### Cursor / Windsurf

Add to your MCP settings (`.cursor/mcp.json` or similar):

```json
{
  "mcpServers": {
    "gleann": {
      "command": "gleann",
      "args": ["mcp"]
    }
  }
}
```

### Claude Desktop

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "gleann": {
      "command": "gleann",
      "args": ["mcp"]
    }
  }
}
```

## Available Tools

### RAG & Search Tools

| Tool | Description |
|------|-------------|
| `gleann_search` | Semantic vector search across indexes |
| `gleann_search_multi` | Fan-out search across multiple indexes |
| `gleann_list` | List all available indexes |
| `gleann_ask` | RAG Q&A with an index |
| `gleann_graph_neighbors` | Query callers/callees of a symbol |
| `gleann_document_links` | Get document structure links |
| `gleann_impact` | Blast radius analysis for a symbol |

### Progressive Disclosure Tools

Token-efficient two-phase search. First call `gleann_search_ids` to get compact
results (5–10× fewer tokens), then call `gleann_fetch` only for the passages you
actually need. Use `gleann_get` to resolve citation references at any time.

| Tool | Description |
|------|-------------|
| `gleann_search_ids` | Compact search returning ref/score/source/peek (no full text) |
| `gleann_fetch` | Hydrate full passage content for selected refs from `gleann_search_ids` |
| `gleann_get` | Citation lookup — resolve a persistent `"indexname:id"` reference |

### Session Tracking Tools

Capture what an AI agent does during a gleann session. Sessions are stored in
BBolt (TierShort for events, TierLong for summaries) and survive process restarts.

| Tool | Description |
|------|-------------|
| `gleann_session_start` | Begin a named work session |
| `gleann_session_end` | Close the session with an optional summary |
| `gleann_session_status` | Show active session name and event count |

### Memory Engine Tools

These tools allow autonomous agents to **directly manipulate** gleann's generic
Knowledge Graph, bypassing the RAG pipeline entirely. This is useful for injecting
structured knowledge, linking requirements to code, and exploring multi-hop
relationships.

| Tool | Description |
|------|-------------|
| `inject_knowledge_graph` | Atomically upsert Entity nodes and RELATES_TO edges (idempotent) |
| `delete_graph_entity` | Remove an entity and all its incident edges |
| `traverse_knowledge_graph` | Walk the graph N hops from a start node |

### Long-term Memory (Blocks) Tools

Persistent hierarchical memory (BBolt). Facts stored here are automatically
compiled into a `<memory_context>` system message for every LLM query. This
gives agents persistent knowledge across sessions.

| Tool | Description |
|------|-------------|
| `gleann_memory_remember` | Store a fact in long-term memory |
| `gleann_memory_forget` | Delete a memory block by ID or content match |
| `gleann_memory_search` | Full-text search across all memory tiers |
| `gleann_memory_list` | List memory blocks (optional tier filter) |
| `gleann_memory_context` | Show compiled memory context the LLM currently receives |

### Batch Query Tool

| Tool | Description |
|------|-------------|
| `gleann_batch_ask` | Run 1–10 questions concurrently against a single index, returning all answers in one response |

---

### gleann_search

Search an index with optional filters and graph context:

```json
{
  "index": "my-code",
  "query": "how does authentication work?",
  "top_k": 5,
  "graph_context": true,
  "filters": [
    {"field": "ext", "operator": "eq", "value": ".go"}
  ]
}
```

### gleann_graph_neighbors

Find callers and callees of a function:

```json
{
  "index": "my-code",
  "symbol": "main.handleSearch",
  "direction": "both",
  "depth": 2
}
```

### gleann_impact

Analyze the blast radius of changing a symbol:

```json
{
  "index": "my-code",
  "symbol": "pkg.Config",
  "max_depth": 3
}
```

### inject_knowledge_graph

Inject Entity nodes and RELATES_TO edges in a single atomic transaction.
Re-submitting the same payload is safe (MERGE semantics — no duplicates created).

```json
{
  "index": "project-memory",
  "nodes": [
    {
      "id": "req-001",
      "type": "requirement",
      "content": "User must be able to log in with email and password",
      "attributes": {"priority": "high", "sprint": 3}
    },
    {
      "id": "feat-jwt",
      "type": "code_symbol",
      "content": "JWT authentication handler"
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

**Node fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `id` | ✅ | Globally unique stable key (UUID or slug) |
| `type` | ✅ | Semantic category (e.g. `requirement`, `concept`, `code_symbol`) |
| `content` | optional | Natural-language text → triggers vector embedding |
| `attributes` | optional | Arbitrary JSON metadata |

**Edge fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `from` | ✅ | Source node ID |
| `to` | ✅ | Destination node ID |
| `relation_type` | ✅ | Edge label (e.g. `DEPENDS_ON`, `IMPLEMENTS`, `RELATED_TO`) |
| `weight` | optional | Numeric strength (default `1.0`) |
| `attributes` | optional | Arbitrary JSON metadata |

### delete_graph_entity

Remove an entity node and **all its incident RELATES_TO edges**:

```json
{
  "index": "project-memory",
  "id": "req-001"
}
```

### traverse_knowledge_graph

Walk the knowledge graph from a starting node up to `depth` hops.
Returns all reachable nodes and the edges connecting them.

```json
{
  "index": "project-memory",
  "start_id": "req-001",
  "depth": 3
}
```

**Example use case:** *"Which code symbols are linked to requirement req-001?"*
→ `start_id=req-001`, `depth=2`

The text result highlights the start node with `*` and lists edges as:
```
[*] id="req-001" type="requirement" content="User must be able to log in..."
[ ] id="feat-jwt" type="code_symbol"

=== Edges ===
  "req-001" -[IMPLEMENTED_BY]-> "feat-jwt" (weight=1.00)
```

---

### gleann_memory_remember

Store a fact in persistent long-term memory. Facts are injected into every
subsequent LLM query as part of the `<memory_context>` system message.

```json
{
  "content": "This project uses hexagonal architecture with ports and adapters",
  "tags": "architecture,convention"
}
```

### gleann_memory_forget

Delete a memory block by ID or by content match:

```json
{
  "query": "hexagonal architecture"
}
```

### gleann_memory_search

Search across all memory tiers:

```json
{
  "query": "architecture"
}
```

### gleann_memory_list

List all memory blocks, optionally filtered by tier:

```json
{
  "tier": "long"
}
```

### gleann_memory_context

Returns the compiled memory context string — exactly what the LLM currently
receives as a system message. No arguments required.

```json
{}
```

### gleann_batch_ask

Run multiple questions concurrently against a single index. Useful for agents
that need to explore a topic from several angles without sequential round-trips.

```json
{
  "questions": [
    "How does authentication work?",
    "What database is used?",
    "How are errors handled?"
  ],
  "index": "my-code",
  "top_k": 5,
  "concurrency": 3
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `questions` | array | *(required)* | 1–10 questions to answer |
| `index` | string | *(required)* | Target gleann index name |
| `top_k` | number | `5` | RAG context chunks per question |
| `concurrency` | number | `3` | Parallel question slots (max 5) |

**Response** (text): numbered Q/A pairs with per-question latency.

---

### gleann_search_ids

Token-efficient search. Returns a compact result list — no full passage text.

```json
{
  "index": "my-code",
  "query": "authentication handler",
  "top_k": 10
}
```

**Response fields per result:**

| Field | Description |
|-------|-------------|
| `ref` | Persistent citation key (`"indexname:42"`) — pass to `gleann_fetch` or `gleann_get` |
| `score` | Cosine similarity (0–1) |
| `source` | File path or document name |
| `type` | Chunk type (`text`, `code`, `heading`, …) |
| `peek` | First 120 characters of the passage |

Typical token savings: **5–10× vs `gleann_search`** because full passage text is omitted.

### gleann_fetch

Hydrate full passage content for one or more refs returned by `gleann_search_ids`.

```json
{
  "refs": ["my-code:42", "my-code:57"],
  "index": "my-code"
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `refs` | ✅ | Array of `"indexname:id"` strings from `gleann_search_ids` |
| `index` | optional | Override the index in refs (rarely needed) |

### gleann_get

Citation lookup. Resolve a single `ref` to its full passage at any point in a
conversation — useful for sourcing a claim made earlier.

```json
{
  "ref": "my-code:42"
}
```

Returns the full passage text, source path, chunk type, and score metadata.

---

### gleann_session_start

Begin a named work session. All subsequent `gleann_search` and `gleann_ask`
calls are automatically logged to this session.

```json
{
  "name": "auth-refactor",
  "description": "Auditing the JWT authentication module"
}
```

### gleann_session_end

Close the active session. Optionally record a summary to long-term memory.

```json
{
  "summary": "Reviewed JWT handler. Identified path-traversal risk in token parser."
}
```

Pass `"summary": ""` to close without recording.

### gleann_session_status

Show the active session name and the number of events logged so far.

```json
{}
```

---

## Resources

The MCP server also exposes resources:

| Resource | URI | Description |
|----------|-----|-------------|
| Index List | `gleann://indexes` | List of all indexes |
| File Content | `gleann://{index}/{file_path}` | Read a file from an index |

## Searcher Cache

The MCP server caches loaded searchers (up to 16) using LRU eviction. Frequently accessed indexes stay warm in memory for fast responses.

## Memory Engine Lifecycle

Each Memory Engine store is a separate KuzuDB database under `<index-dir>/<name>_memory/`.
The first request to an index opens the database; it stays open until the MCP server shuts down.

```
Agent                       gleann MCP server            KuzuDB
  |                               |                          |
  |── inject_knowledge_graph ─────>|                          |
  |   {nodes: [...], edges: [...]} |── BEGIN TRANSACTION ─────>|
  |                               |── MERGE Entity "req-001" ─>|
  |                               |── MERGE Entity "feat-jwt" ─>|
  |                               |── MERGE RELATES_TO ────── >|
  |                               |── COMMIT ─────────────── >|
  |<── "OK — injected 2 nodes..." |                          |
  |                               |                          |
  |── traverse_knowledge_graph ──>|                          |
  |   {start_id: "req-001", depth:2}── Cypher MATCH ─────── >|
  |                               |<── nodes + edges ──────── |
  |<── formatted text result ─────|                          |
```
