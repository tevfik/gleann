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

| Tool | Description |
|------|-------------|
| `gleann_search` | Semantic vector search across indexes |
| `gleann_list` | List all available indexes |
| `gleann_ask` | RAG Q&A with an index |
| `gleann_graph_neighbors` | Query callers/callees of a symbol |
| `gleann_document_links` | Get document structure links |
| `gleann_impact` | Blast radius analysis for a symbol |

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

## Resources

The MCP server also exposes resources:

| Resource | URI | Description |
|----------|-----|-------------|
| Index List | `gleann://indexes` | List of all indexes |
| File Content | `gleann://{index}/{file_path}` | Read a file from an index |

## Searcher Cache

The MCP server caches loaded searchers (up to 16) using LRU eviction. Frequently accessed indexes stay warm in memory for fast responses.
