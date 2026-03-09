# AST Graph Indexer

gleann can build a property graph of your codebase **in parallel** with the vector embedding index, using KuzuDB as the storage engine and tree-sitter (or `go/ast`) as the parser.

## Usage

```bash
# Build the vector index AND the call graph at the same time
gleann build my-code --docs ./src --graph

# Rebuild from scratch (remove + build)
gleann rebuild my-code --docs ./src --graph

# Query direct and transitive dependencies (outgoing CALLS)
gleann graph deps "github.com/tevfik/gleann/pkg/gleann.MyFunc" --index my-code

# Query callers (incoming CALLS)
gleann graph callers "github.com/tevfik/gleann/pkg/gleann.MyFunc" --index my-code

# Search with graph context (callers/callees enrichment in results)
gleann search my-code "handleSearch" --graph
```

## Graph Schema

| Node | Properties |
|------|------------|
| `CodeFile` | `path`, `lang` |
| `Symbol` | `fqn`, `name`, `kind`, `file`, `line` |

| Edge | From → To | Meaning |
|------|----------|---------|
| `DECLARES` | `CodeFile → Symbol` | File contains symbol |
| `CALLS` | `Symbol → Symbol` | Function calls another |
| `IMPLEMENTS` | `Symbol → Symbol` | Struct implements interface |

## MCP Integration (Yaver / Cursor / Claude Code)

When `gleann mcp` is running, AI editors can call these graph tools:

- **`gleann_graph_deps`** — returns what a given symbol depends on  
- **`gleann_graph_callers`** — returns who calls a given symbol
- **`gleann_impact`** — blast radius analysis: direct callers, transitive callers, affected files

The AI can autonomously trace code execution paths without leaving the editor.

## Graph-Augmented Search

When searching with `--graph` (CLI) or `graph_context: true` (API/MCP), each search result is enriched with structural context from the AST graph:

- **Symbols in the same file**: Declarations found in the matched file
- **Callers**: Functions/methods that call each symbol
- **Callees**: Functions/methods called by each symbol

This gives LLMs both semantic (vector) and structural (graph) context in a single query. Only symbols with at least one relationship are included (max 5 per file).

### API Usage

```bash
# REST API
curl -X POST http://localhost:8080/search -d '{
  "index": "my-code",
  "query": "handleSearch",
  "graph_context": true
}'

# MCP tool call
{"tool": "gleann_search", "arguments": {"index": "my-code", "query": "handleSearch", "graph_context": true}}
```

## Impact Analysis

Find the blast radius of changing any symbol — all direct callers, transitive callers (via BFS), and affected files.

```bash
# REST API
curl -X POST http://localhost:8080/graph -d '{
  "index": "my-code",
  "query_type": "impact",
  "symbol": "github.com/tevfik/gleann/pkg/gleann.Search",
  "max_depth": 5
}'

# MCP tool call
{"tool": "gleann_impact", "arguments": {"index": "my-code", "symbol": "...", "max_depth": 5}}
```

Response includes:
- **Direct Callers**: Functions that directly call the target symbol
- **Transitive Callers**: All callers up to N hops away (BFS traversal)
- **Affected Files**: Files containing any of the callers
- **Depth**: Maximum traversal depth used

## Supported Languages

| Language | Declaration | Call Extraction |
|----------|-------------|------------------|
| Go | `go/ast` native | `go/ast` call expressions |
| Python | tree-sitter `function_definition`, `class_definition` | `call` nodes |
| JS / TS | tree-sitter `function_declaration`, `class_declaration` | `call_expression` nodes |
| C / C++ | tree-sitter `function_definition`, `class_specifier` | `call_expression` nodes |
| Rust | tree-sitter `function_item`, `impl_item`, `struct_item` | `call_expression` nodes |
| Java / C# | tree-sitter `method_declaration`, `class_declaration` | `call_expression` nodes |
| Ruby | tree-sitter `method`, `class` | `call` nodes |
| PHP | tree-sitter `function_definition`, `class_declaration` | `function_call_expression` nodes |

## Graph Index Performance

Graph indexing was optimized using **KuzuDB CSV Bulk Load** (`COPY FROM`) instead of individual Cypher statements:

| Method | 1500-file Go repo | Notes |
|--------|------------------|---------|
| Single statements (MERGE) | 8 min 31 sec | One transaction per symbol |
| Batch statements (tx) | ~2 min | Chunked transactions |
| **CSV Bulk Load (COPY FROM)** | **84 ms** | Current approach |

FK integrity is enforced by filtering CALLS edges — only symbol-to-symbol references within the indexed codebase are stored (cross-package / stdlib calls are discarded).
