# MCP Server Guide

Gleann includes a built-in [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) server that exposes your indexed knowledge base to AI editors like Cursor, Windsurf, Claude Desktop, and others.

## Quick Start

```bash
gleann mcp
```

This starts an MCP server over stdio. Configure your AI editor to use it.

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
