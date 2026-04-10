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

The KuzuDB property graph models both source code ASTs and the structural layout of documents. For a deeper dive into document structures, see [Hierarchical GraphRAG](hierarchical_graphrag.md).

| Node | Properties |
|------|------------|
| `Folder` | `vpath`, `name` |
| `Document` | `vpath`, `rpath`, `name`, `hash`, `summary` |
| `Heading` | `id`, `name`, `level` |
| `CodeFile` | `path`, `lang` |
| `Symbol` | `fqn`, `name`, `kind`, `file`, `line` |
| `DocChunk`, `Chunk` | `id`, `content` |

| Edge | From → To | Meaning |
|------|----------|---------|
| `CONTAINS_DOC` | `Folder → Document` | Folder contains a document |
| `HAS_HEADING` | `Document → Heading` | Document contains a top-level heading |
| `CHILD_HEADING` | `Heading → Heading` | H1 contains H2, etc. |
| `HAS_CHUNK_DOC` | `Document → DocChunk` | Document contains text chunks (if no headings) |
| `HAS_CHUNK_HEADING` | `Heading → Chunk` | Heading contains text chunks |
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
| **Kotlin** | tree-sitter `function_declaration`, `class_declaration` | `call_expression` nodes |
| **Scala** | tree-sitter `function_definition`, `class_definition` | `call_expression` nodes |
| **Swift** | tree-sitter `function_declaration`, `struct_declaration` | `call_expression` nodes |
| **Lua** | tree-sitter `function_declaration`, `local_function_declaration` | `function_call` nodes |
| **Elixir** | tree-sitter `call` | `call` nodes |

## Community Detection (Louvain)

gleann can detect communities (clusters) in your code graph using the Louvain algorithm:

```bash
# Run community detection and print results
gleann graph communities --index my-code

# Generate interactive HTML visualization
gleann graph viz --index my-code
gleann graph viz --index my-code --output project_graph.html

# Generate Markdown report with communities, god nodes, surprising edges
gleann graph report --index my-code
gleann graph report --index my-code --output GRAPH_REPORT.md
```

### What the analysis reveals

- **Communities**: Groups of tightly-connected symbols (functions, types) that form natural modules
- **God Nodes**: High-degree hubs — symbols that many others depend on (coupling hotspots)
- **Surprising Edges**: Cross-community connections that may indicate leaky abstractions
- **Modularity (Q)**: Quantitative measure of code modularity (Q > 0.4 = well-modularized)

### Edge Confidence

Each CALLS edge carries an implicit confidence label:

| Confidence | Meaning |
|------------|---------|
| `extracted` | Deterministic, AST-resolved call (tree-sitter or go/ast) |
| `inferred` | Heuristic-based resolution (e.g. methods via type inference) |
| `ambiguous` | Unresolved — callee couldn't be mapped to a known FQN |

## Token Reduction Benchmark

Measure how much context compression the RAG pipeline achieves:

```bash
gleann benchmark --index my-code --docs ./src/
gleann benchmark --index my-docs --docs ./documents/ --top-k 20
```

Output: `Token Reduction: 62.1x` — raw corpus tokens / RAG context tokens.

## Graph Index Performance

Graph indexing was optimized using **KuzuDB CSV Bulk Load** (`COPY FROM`) instead of individual Cypher statements:

| Method | 1500-file Go repo | Notes |
|--------|------------------|---------|
| Single statements (MERGE) | 8 min 31 sec | One transaction per symbol |
| Batch statements (tx) | ~2 min | Chunked transactions |
| **CSV Bulk Load (COPY FROM)** | **84 ms** | Current approach |

FK integrity is enforced by filtering CALLS edges — only symbol-to-symbol references within the indexed codebase are stored (cross-package / stdlib calls are discarded).
