# AST Graph Indexer

gleann can build a property graph of your codebase **in parallel** with the vector embedding index, stored in the index's own SQLite database and parsed with `go/ast` (Go) and tree-sitter (other languages, `-tags treesitter`).

**Every index keeps the graph in the index database** (`<name>/<name>.db`, tables `symbols`, `graph_nodes`, `graph_edges`, `graph_labels`, `doc_nodes`, `doc_explains`, and a read-only `edges` view in the older text shape; RFC 003 step 7, RFC 004 E) and build and serve it in every build, including the pure-Go `CGO_ENABLED=0` binary: `index build --graph`, `index sync` and `graph deps|callers|explain|path|query|toc` work there, and no other graph store is created. Without tree-sitter the graph is extracted with `go/ast`: Go files get CALLS, IMPLEMENTS and REFERENCES edges, other languages contribute their declarations only. The analysis commands (`graph viz|report|communities|export|wiki|hook|map|risk`), the MCP tools `gleann_communities`, `gleann_risk_analysis`, `gleann_repo_map`, `gleann_navigate_symbol` and the REST/A2A graph endpoints run on this SQLite graph in every build. **Kùzu was removed (v1.20, RFC 004):** an index built by gleann v1.9 or earlier is refused by every command but `gleann index migrate <name>`, which converts it and rebuilds the code graph from the index's source directory when the old index had one. An old `<name>_graph` directory is never read; `gleann index migrate <name> --cleanup` and `gleann index remove` delete it. Community detection, PageRank, risk, the repo map, export and viz are deterministic: the same graph gives the same output on every run. Raw Cypher (`POST /api/graph/{name}/query` with `cypher`, and the UI option) is removed.

## Usage

```bash
# Build the vector index AND the call graph at the same time
gleann index build my-code --docs ./src --graph

# Rebuild from scratch (remove + build)
gleann index rebuild my-code --docs ./src --graph

# Query direct and transitive dependencies (outgoing CALLS)
gleann graph deps "github.com/tevfik/gleann/pkg/gleann.MyFunc" --index my-code

# Query callers (incoming CALLS)
gleann graph callers "github.com/tevfik/gleann/pkg/gleann.MyFunc" --index my-code

# Search with graph context (callers/callees enrichment in results)
gleann search my-code "handleSearch" --graph
```

## Graph Schema

The property graph models both source code ASTs and the structural layout of documents. For a deeper dive into document structures, see [Hierarchical GraphRAG](hierarchical_graphrag.md).

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

## MCP Integration (Windsurf / Cursor / Claude Code)

When `gleann mcp` is running, AI editors can call these graph tools:

- **`gleann_graph_neighbors`** — inspect incoming or outgoing edges (callers, callees, declarations)
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
curl -X POST http://localhost:8080/api/graph/my-code/query \
  -H "Content-Type: application/json" \
  -d '{
    "query": "impact",
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
| Go | `go/ast` native | `go/types` (exact) in format-2 full builds when the discovered Go modules type-check; otherwise `go/ast` call expressions |
| Python | tree-sitter `function_definition`, `class_definition` | `call` nodes |
| JS / TS | tree-sitter `function_declaration`, `class_declaration` | `call_expression` nodes |
| C / C++ | tree-sitter `function_definition`, `class_specifier` | `call_expression` nodes |
| Rust | tree-sitter `function_item`, `impl_item`, `struct_item` | `call_expression` nodes |
| Java / C# | tree-sitter `method_declaration`, `class_declaration` | `call_expression` nodes |
| **Ruby** | tree-sitter `method`, `class` | `call` nodes (with or without parentheses) |
| PHP | tree-sitter `function_definition`, `class_declaration` | `function_call_expression` nodes |
| **Kotlin** | tree-sitter `function_declaration`, `class_declaration`, `object_declaration`, `property_declaration` | `call_expression` nodes (bare and `a.b()` navigation calls) |
| **Scala** | tree-sitter `function_definition`, `class_definition`, `object_definition` | `call_expression` nodes |
| **Swift** | tree-sitter `function_declaration`, `struct_declaration` | `call_expression` nodes |
| **Lua** | tree-sitter `function_statement` | `function_call` nodes (bare, dotted, colon, string/table argument) |
| **Elixir** | tree-sitter `call` nodes of `def`/`defp`/`defmodule`/`defmacro`/... | `call` nodes; definition/directive macros are filtered out |
| **Svelte** | JavaScript/TypeScript of each inline `<script>` block | `call_expression` nodes of that block; template markup is not scanned |

All languages other than Go need a `-tags treesitter` build for call extraction; a build without it gives their declarations only, printing a notice at graph build/sync and across query surfaces: CLI commands (`explain`, `callers`, `deps`, `path`, `query`), the MCP tools `gleann_graph_neighbors` and `gleann_impact`, the REST query endpoint `POST /api/graph/{name}/query` (its `callees`, `callers` and `impact` responses carry the optional `notice` field), and A2A code analysis skills for non-Go symbols (`use a build with tree-sitter: the 'full' release artifact`). A call is charged to the innermost declaration that contains it, so a Kotlin, Lua, Scala or Elixir method is the caller rather than the file's `<script:file>` symbol; only genuinely module-level calls use `<script:file>`. A receiverless Ruby call without arguments or parentheses (`save`) parses as a plain identifier and yields no edge. See [treesitter.md](treesitter.md#call-graph-limits).

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
| `exact` | Type-checked with `go/types` (format-2 builds and syncs of a Go module, see below) |

### Exact Go resolution

A full graph build of a format-2 index whose source tree contains Go modules loads every one of them -- a root module, nested submodules (`sub/go.mod`, which `./...` excludes from the root module) and the modules linked by a `go.work` under the root -- and resolves every call exactly: interface calls to the interface method, promoted methods to their declaring type, aliased and dot imports, generic calls, method values; a shadowed local function is not mistaken for the package-level one. Each module is loaded from its own directory and every file is prefixed with the module path that really owns it, so a symbol of `sub/go.mod` is `example.com/sub.Fn`, never the root module path plus `sub`. A `go.work` governs only the modules its `use` directives name: a standalone submodule in the same tree (a tool or plugin with its own `go.mod` that the workspace does not use) is loaded on its own with `GOWORK=off`, and an empty, comment-only or malformed `go.work` -- one the Go tool itself refuses to parse, say a raw (backtick) string in `use` -- is ignored, because Go rejects `./...` in a module a workspace does not list. The `go.work` file need not sit above the modules it uses: a workspace kept in a subdirectory that lists `../a` governs `a` too, as the go command honours from any listed module; when several workspaces list one module, the deepest workspace containing it wins, and among the others that list it the nearest in the tree wins. IMPLEMENTS edges are added by method set (`T` or `*T` implements `I`), not only by embedding. These edges carry the confidence `exact`.

It needs a Go toolchain on the machine; the load never downloads modules or edits `go.mod` (`GOPROXY=off`, `-mod=readonly`, or `-mod=vendor` for a module that vendors its dependencies -- chosen per module, from that module's own directory; a `go.work` workspace uses its workspace vendor directory when it has one; `GOPRIVATE`/`GONOPROXY`/`GONOSUMDB` are overridden so a private-looking module path cannot trigger a direct fetch, and the `-tags` of your `GOFLAGS` are kept) and is bounded to two minutes per module. Outside a module, or for a package with type errors, the syntactic resolver is used for those files. When Go files exist but no `go.mod`/`go.work` was loaded the build prints a warning on stderr (never on stdout) that calls through a receiver, variable or interface are missing; a `.go` file under `testdata`, `vendor`, a hidden or a `_`-prefixed directory, or whose own name starts with `_` or `.`, is no package of the tree (the Go tool skips those directories and those file names), so such a non-Go repository with Go fixtures neither warns nor starts a load. Files of a cgo package are mapped back to their original sources (the type-checker sees the cgo-generated files in the build cache), so their symbols keep repo paths and the module prefix. `GOTOOLCHAIN` is taken from your environment: an installed `go` older than the `go` line of `go.mod` runs the toolchain the module asks for when it is in the module cache, as `go build` does. When the load fails as a whole (no toolchain on the machine, or the one the module needs is not available offline) the build prints a warning and uses the syntactic resolver, which records no call made through a receiver, a variable or an interface. `GLEANN_GRAPH_EXACT=0` turns it off, for builds and syncs. On gleann's own tree (20 sampled functions) the exact graph finds callers with precision 0.993 / recall 1.000 against 0.992 / 0.948 for the syntactic resolver; the full graph build takes 3.8 s instead of 1.8 s.

An incremental sync (`gleann index sync`, watch mode) resolves the Go files it re-extracts in the same way, but type-checks only the packages that contain them: the changed files, the files whose call target the change removed, and the packages declaring the interfaces of the stored IMPLEMENTS edges (so a method-set edge is re-judged, not carried blind). Each changed directory is routed to the module that owns it, including a nested submodule, and the module's real path prefixes the synced symbols. Deleted files are retracted before changed files are applied, so a rename within a package keeps its symbols and edges. The packages they import are read from compiler export data (`go list -export`, served by the Go build cache) instead of being type-checked from source. On gleann's own tree the graph after a one-file sync equals the graph of a full build of the edited tree (same sha256 over all edges and symbols), and the graph step of the sync takes 0.3 to 1.2 s instead of 0.1 s with the same peak memory; type-checking the whole module in every sync would take 2.6 s and 1.6 GB. The first sync after the Go build cache was emptied compiles the dependencies of the changed package (11 to 18 s here). The load is bounded to one minute, and what it compiled before the bound stays in the cache for the next sync. A sync that re-extracts no non-test Go file starts no load. When the load fails, or a changed package does not type-check, the sync prints a warning and extracts those files with the syntactic resolver; they are marked dirty and re-resolved by the next syncs until their package type-checks again (a file excluded by build tags is not retried: its package type-checked without it). The first sync also retracts graph rows an older gleann stored for non-code files (README, LICENSE, `go.mod`, …); later syncs skip that scan.

Three things are only as fresh as the last full build: an unchanged file whose calls resolve differently after an edit elsewhere (when the edit removed the target, the file is re-extracted); a changed file's IMPLEMENTS edges to interfaces declared in packages that did not type-check during the sync, which are carried over rather than derived again; and a *new* type's IMPLEMENTS edges to interfaces of packages that are neither loaded nor imported by the changed files (the sync extends its load from the stored edges, which a new type does not have yet).

## Token Reduction Benchmark

Measure how much context compression the RAG pipeline achieves:

```bash
gleann benchmark --index my-code --docs ./src/
gleann benchmark --index my-docs --docs ./documents/ --top-k 20
```

Output: `Token Reduction: 62.1x` — raw corpus tokens / RAG context tokens.

## Graph Index Performance

A full build replaces the graph in one bulk load (`graphstore.ReplaceAll`); an incremental sync applies one `FileDelta` per changed file in a transaction (`ApplyFileDelta`). Calls to symbols outside the indexed codebase (cross-module, stdlib) are kept as external edges (`dst_external`), never as symbol rows. Edge endpoints are integer node ids (a declared symbol's `symbols.id`, or a `graph_nodes` row for an external target), edge kinds and confidences are interned in `graph_labels`, and `graph_edges` is a `WITHOUT ROWID` table keyed by `(src, dst, kind)`; `graph_edges_dst` serves the caller walks. The database runs in WAL mode (one writer, many readers; `journal_mode=DELETE` on a network file system); builds and syncs of an index — including a standalone `gleann graph build` / `POST /api/graph/{name}/index` — are serialised across processes by the index lock `<IndexDir>/<name>.lock`. A standalone full graph build reads the source documents and rebuilds the document graph (`doc_nodes`/`doc_explains`) from them; when the source tree yields no documents, the existing document graph is left untouched.

Synthetic graph with 1M CALLS edges, `bench/graphdb`, cores 0-3, p50:

| | SQLite |
|---|---:|
| bulk load | 3.77 s |
| disk | 75.3 MiB |
| callers, depth 3 | 0.71 ms |
| impact, depth 3 | 0.73 ms |
| symbol search | 1.47 ms |
| shortest path | 2.61 ms |
| one-file update | 102 ms |

At 100K edges the bulk load takes 339 ms and the graph 8.0 MiB. A full graph build of this repository (12,224 symbols, 35,620 edges) takes 0.85 s. The comparison with the former Kùzu store is in [decisions/kuzu-to-sqlite.md](decisions/kuzu-to-sqlite.md); the harness is described in [benchmarks.md](benchmarks.md) (`make bench-graphdb`).

## Risk Scoring

Quantifies how risky it is to change a given symbol by combining three graph metrics into a composite score [0, 1]:

| Metric | Weight | How it's computed |
|--------|--------|-------------------|
| Centrality | 35% | PageRank score (normalized) |
| Coupling | 30% | Fan-in × Fan-out (normalized) |
| Blast Radius | 35% | BFS traversal weighted by callers' PageRank |

```bash
# CLI
gleann graph risk --index my-code --top 20

# API
curl -X POST http://localhost:8080/graph -d '{
  "index": "my-code",
  "query_type": "risk",
  "top_k": 20
}'
```

Each symbol gets a risk level: `critical` (≥0.8), `high` (≥0.6), `medium` (≥0.3), `low` (<0.3).

`FileRiskSummary` aggregates per-symbol scores to file level (max score per file), useful for identifying the riskiest files in the codebase.

**Configuration:**

```go
cfg := community.DefaultRiskConfig()
cfg.CentralityWeight  = 0.35
cfg.CouplingWeight    = 0.30
cfg.BlastRadiusWeight = 0.35
cfg.MaxBlastDepth     = 3   // BFS depth for blast radius
cfg.DampingFactor     = 0.85
cfg.Iterations        = 30
```

## Repository Map (Repo Map)

Generates a compact, token-budgeted text summary of the most important symbols in the codebase, ranked by PageRank. Designed for LLM context injection — gives the model a high-level overview of the codebase structure without consuming the full context window.

```bash
# CLI
gleann graph map --index my-code --top-k 30 --max-tokens 2000
```

Output example:
```
## Repository Map (by importance)

### pkg/gleann/search.go
- function `Search`
- function `HybridSearch`

### internal/graphstore/sqlite.go
- function `New`
- method `Callers`
```

**Configuration:**

```go
cfg := community.DefaultRepoMapConfig()
cfg.TopK      = 30    // Top symbols to include
cfg.MaxTokens = 2000  // Token budget (approximate)
```

The map is grouped by file and sorted by total PageRank score, providing a natural "table of contents" for any codebase.

## Incremental Graph Update

When files change, gleann can incrementally update both the vector index and the graph without a full rebuild:

```bash
# Watch mode: auto-detect changes and incrementally update
gleann index watch my-code --docs ./src --graph --interval 5
```

The incremental pipeline:
1. Detects changed/deleted files via hash comparison (vault tracker)
2. Removes old passages and graph entries for changed sources
3. Re-chunks and re-indexes only the affected files; the code graph takes code source files only, and Go files are re-resolved with `go/types` (see [Exact Go resolution](#exact-go-resolution))
4. Falls back to a full rebuild only when the index store is missing, unreadable or damaged, or cannot apply the delta (`ErrRebuildRequired`). A transient failure — embedder or plugin error, unreadable source — is returned with the index untouched (no rebuild, no rollback copy); fix the cause and the next sync retries the delta. Backends that cannot apply a delta internally degrade to an in-place full update instead of surfacing a rebuild error.

This enables near-instant index updates during active development.

## Smart Context Injection

Combines repo map + graph-augmented search for optimal LLM context:

1. **Repo map** provides high-level codebase overview (top-K symbols by PageRank)
2. **Graph search** provides structural context (callers/callees for each result)
3. **Vector search** provides semantic context (relevant passages)

The three are combined within a token budget, prioritizing the most relevant information. Used automatically when `--graph` is enabled for search and chat.
