# AST Graph Indexer

gleann can build a property graph of your codebase **in parallel** with the vector embedding index, using KuzuDB as the storage engine and tree-sitter (or `go/ast`) as the parser.

## Usage

```bash
# Build the vector index AND the call graph at the same time
gleann build my-code --docs ./src --graph

# Query direct and transitive dependencies (outgoing CALLS)
gleann graph deps "github.com/tevfik/gleann/pkg/gleann.MyFunc" --index my-code

# Query callers (incoming CALLS)
gleann graph callers "github.com/tevfik/gleann/pkg/gleann.MyFunc" --index my-code
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

When `gleann mcp` is running, AI editors can call two new tools:

- **`gleann_graph_deps`** — returns what a given symbol depends on  
- **`gleann_graph_callers`** — returns who calls a given symbol

The AI can autonomously trace code execution paths without leaving the editor.

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
