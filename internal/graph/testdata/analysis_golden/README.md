# Analysis goldens of the former Kùzu path (RFC 004 A0)

These files freeze what gleann v1.9.x answered on the **Kùzu path** (a format-1
index built in a `-tags treesitter` binary, graph store `<name>_graph`) for the
small project in `../analysis_fixture/` (Go, Python, TypeScript and Rust files,
about 290 lines, cross-file calls, an interface, a test file). RFC 004 A1
(tree-sitter CALLS in `ExtractFile`) and A2 (analysis on the SQLite graph)
proved they changed nothing, or explained every difference (below).

**Kùzu was removed in RFC 004 A3.** The Kùzu-path tests that produced these
files are gone, so the files can no longer be regenerated: they are a frozen
reference. Only the files the SQLite path is still compared against are kept
(the Kùzu-only ones, `calls_go.json`, `cli_viz_graph.txt`,
`mcp_navigate_{clamp,clean,slug}.txt` and
`rest_query_{callees,callees_rust,callers_short}.json`, were deleted with
their tests).

The goldens were produced with the then-default format-1 store (the
`GLEANN_STORE_FORMAT` switch, removed in v1.20, unset) by
`indexing.BuildGraphIndex` on the fixture, which was then the graph step of
`gleann index build --graph` for a format-1 index.

## Compare

The SQLite-path tests build the fixture as a format-2 index
(`analysisgolden.BuildIndexFormat2`) and compare with these files (the
invariant part) and with `sqlite_lite/` (the full text, build without
tree-sitter; `-update` rewrites only `sqlite_lite/`):

```
go test ./cmd/gleann ./internal/mcp ./internal/server -run SQLite [-update]
go test -tags treesitter ./internal/indexing -run TestFormat2NonGoCallsMatchKuzuGolden
go test -run TestGraphDBGoldenSQLite ./pkg/gleann    # pkg/gleann/testdata/graph_golden
```

Shared helpers (fixture path, index build, normalisation, compare/update) are in
`internal/graph/analysisgolden`.

## Normalisation

Applied to every file: the fixture directory becomes `<FIXTURE>`, the index
directory `<INDEXDIR>`, a temporary output directory `<OUT>`; timestamps
`<TIMESTAMP>`; durations `(<DURATION>)`; `query_ms` is `0`; any number with five
or more decimals is rounded to four; JSON has sorted keys; result arrays whose
order is defined by the engine are sorted.

## Known non-determinism of the Kùzu path (frozen, not fixed)

`community.Graph.Edges()` returns each undirected neighbour pair in whichever
direction Go's map iteration yields first, and the Louvain pass also iterates
maps. As a result, on the same graph, from run to run:

- PageRank scores, the ranking and the top-N cut-off of `graph map` and
  `gleann_repo_map`;
- in/out degree, blast radius, score and risk level of `graph risk` and
  `gleann_risk_analysis`;
- the edge direction and order in `graph export` (GraphML, Cypher) and the viz data;
- the number of communities (20 to 23 were seen), their members, labels,
  cohesion, the modularity, the cross-community ("surprising") edges and
  everything in the report that derives from them

all change. The goldens therefore keep **only what is invariant**: node sets,
kinds and files, total degree (in+out), god nodes (their in/out come from the
directed loader and are stable), the node/edge counts and undirected edge
pairs. Everything else is intentionally not compared. A2 must reproduce exactly
the invariant content below; it may make the direction- and community-dependent
numbers deterministic (it should) and does not have to match any value of a
previous run. A difference in an invariant item is a behaviour change and has to
be explained in the commit that causes it.

## Files

| File | Produced by | Content kept | How A2 compares |
|---|---|---|---|
| `calls_nongo.json` | the former `analysisgolden.TestKuzuCallsBaseline`: for every Python/TypeScript/Rust symbol of the Kùzu graph, `GraphDB.Callees` and `GraphDB.Callers` (kind, file, line of each) | exact | A1: the SQLite graph (`gleann.GraphDB`) must give the same callers/callees for the same FQNs. Known expected differences: the Kùzu rows with `file: null` (built-ins and member calls such as `py.strip`, `ts.map`, `rs.new`) are phantom symbols without a source anchor, which AGENTS.md section 2 forbids; A1 may tag them external or drop them, and must say which. Calls made by methods are attributed to the class (`py.Registry` calls `py.clean`; `ts.Square` calls `ts.slug`); Kùzu also lists callers by base name, see the `main` note below. Top-level Python code is the symbol `py.<script>` |
| `cli_map.txt` | `gleann graph map --index fixture --top 1000` (`cmdGraphMap`) | files and the symbols listed per file, sorted, no scores | same set of files and symbols |
| `cli_risk.txt` | `gleann graph risk --index fixture --top 1000` | symbol, kind, total degree | same symbols, kinds, in+out |
| `cli_risk_by_file.txt` | `gleann graph risk --by-file --top 1000` | file names | same files |
| `cli_communities.txt` | `gleann graph communities` | the counts (85 nodes, 94 edges) and the god nodes (kind, name, in/out/total) | same counts, same god nodes |
| `cli_export.graphml` | `gleann graph export --format graphml` | nodes in full (label, kind, file), edges as sorted undirected pairs written as comments | same nodes and the same undirected edge pairs |
| `cli_export_cypher.txt` | `gleann graph export --format cypher` | node statements in full, edges as sorted undirected pairs | same |
| `cli_report.md` | `gleann graph report --docs <fixture> --output GRAPH_REPORT.md` | headings, Source line, Nodes/Edges/God Nodes counts and the god-node rows (rank dropped, sorted) | same; the community sections are present but their content is not compared |
| `mcp_communities.txt` | MCP `gleann_communities` (raw JSON part) | node/edge counts and god nodes | same |
| `mcp_risk_analysis.txt`, `mcp_risk_analysis_by_file.txt` | MCP `gleann_risk_analysis` with `top=1000` (and `by_file`) | as the CLI risk files | same |
| `mcp_repo_map.txt` | MCP `gleann_repo_map` with `top_k=1000`, `max_tokens=1000000` | files and the `kind name` lines under each, sorted | same |
| `mcp_navigate_*.txt` | MCP `gleann_navigate_symbol` for `validate` (Go), `main` (four matches in four languages) and an unknown symbol | exact, with the caller/callee lines and the matches sorted | same text |
| `rest_stats.json`, `rest_stats_absent.json` | `GET /api/graph/{name}` (existing graph; an index without a graph) | exact | A2: `symbol_count` 75, `file_count` 10, `calls_count` 45, `declares_count` 49 must hold for the same data; `db_path` will change. Kùzu reports `available: true` for an absent index because `Open` creates an empty database; A2 may answer differently and must say so |
| `rest_query_{callers,symbols_in_file,symbols_in_file_go,impact,impact_default_depth}.json` | `POST /api/graph/{name}/query` | exact, response arrays sorted | same status and body |
| `rest_query_toc.json` | `POST .../query` `toc` on a code file | exact (a 500 `no Document found`; the fixture has no plugin documents) | same, or a 404 (differences explained) |
| `rest_query_unknown.json` | unknown query type | exact | same |
| `rest_query_cypher.json` | `POST .../query` `cypher` | exact | raw Cypher is retired by A2: the expected difference is a 4xx/410 and the removed UI option |

Observed behaviours worth knowing when comparing (all frozen in the files above):

- `Callees(fqn)` and `Callers(fqn)` use a three-step name resolution (exact FQN,
  qualified suffix, base name; the first non-empty step wins). A symbol with no
  edges of its own therefore returns the union over every symbol with the same
  base name: the callees of `rs.main` in `mcp_navigate_main.txt` are those of the
  other three `main` functions, and every `main` has `py.<script>` as caller
  because the Python script calls `main()`. The SQLite `GraphDB` implements the
  same rule (pkg/gleann parity goldens), so this should carry over unchanged.
- The graph used by viz/map/risk/export (symbols and CALLS only, 75 nodes, 45
  edges) is not the graph used by communities/report (symbols, files and
  DECLARES, 85 nodes, 94 edges); the CALLS-only loader of A2 keeps that split.
- `SymbolSearch` has no ordering and is capped at 50 rows.

## calls_nongo.json versus the format-2 graph (RFC 004 A1)

`TestFormat2NonGoCallsMatchKuzuGolden` (`internal/indexing`, treesitter) builds
the fixture as a format-2 index with `indexer.ExtractFile` (tree-sitter CALLS)
and compares the CALLS edges of the Python, TypeScript and Rust symbols with
`calls_nongo.json`. Comparison rule: an edge is the triple (caller file, caller's
top-level declaration, callee base name), and symbols named `main` are skipped
as sources (the base-name union above). With that rule the two edge sets are
equal; the declarations (kind, file, start line) of every anchored symbol are
equal too. The raw answers differ in these documented ways:

- **Method attribution.** Kùzu charges a call inside a Python/TypeScript method to
  the class (`py.Registry` calls `py.clean`); the SQLite graph charges it to the
  innermost declaration, the method (`py.Registry::add` calls `py.clean`). The
  method is the more precise caller and exists as a symbol; callers of the class
  are still found through the method. Ties (a one-line class, C++ inline
  methods) go to the later chunk, the method.
- **Method callees.** The extractor names a callee by its bare identifier in the
  caller's directory (`py.add`), as Kùzu did. Kùzu kept a phantom symbol
  `py.add`; the SQLite graph has no phantoms, so the write path binds such an
  edge to the method(s) of that name declared in the same package
  (`py.Registry::add`, confidence `inferred`; several candidates `ambiguous`,
  at most 8; a method of the caller's own class wins). Without a method the edge
  keeps its bare target and is stored external.
- **Built-ins and member calls** (`py.strip`, `ts.map`, `rs.new`): Kùzu's
  file-less phantom symbols become `dst_external` edges, not rows
  (AGENTS.md section 2.2); `Callees` of the caller does not list them.
- **Module-level calls.** Kùzu's `py.<script>` has no anchor (line 0). The
  synthetic caller now is a real declaration anchored at line 1, column 1,
  offset 0 and ending on the file's last line, and is named per file
  (`py.<script:app_py>`, Name `<script>`) because the files of one directory share
  a package prefix and a shared FQN would make the first file own it and drop the
  other files' top-level calls.
- **Base-name union** of `Callers`/`Callees` for names that exist in several
  languages (`main`) is query behaviour, unchanged (the SQLite `GraphDB` has the
  same rule).

## pkg/gleann/testdata/graph_golden

Separate from this directory: `basic.json`, `resolution.json`,
`selfrecursion.json`, `multiseed.json` hold the answers of the former Kùzu
`GraphDB` for the fixtures in `pkg/gleann/graphdb_golden_test.go` (all `GraphDB`
methods with a call graph). `TestGraphDBGoldenSQLite` (every build) checks the
format-2 SQLite `GraphDB` against them; they can no longer be regenerated. The
only normalisation is that an empty `ShortestPath`
result is `[]` for both backends (Kùzu returned `nil`).

## SQLite path (RFC 004 A2)

A2 moves the analysis to the format-2 SQLite graph. The files above stay the
Kùzu reference. The SQLite answers are frozen in `sqlite_lite/` (the build
without tree-sitter: Go symbols and CALLS from `go/ast`, other languages as
declarations only, since A1 is not in). With `-tags treesitter` the
`sqlite_lite` goldens are not checked: that build extracts more, and A1 changes
the non-Go CALLS again. Run them with
`go test ./cmd/gleann ./internal/mcp ./internal/server -run SQLite [-update]`
(and `./internal/graph/community`).

Determinism: `community.Graph` now records directed edges in insertion order and
every pass iterates sorted keys, so communities, PageRank, risk, repo map,
export and viz are identical on every run (tests run each five times). The
`sqlite_lite` files therefore keep the full text, not only the invariants.

Comparison with the Kùzu goldens (the invariants above), with every difference:

| Item | Result |
|---|---|
| Go symbols, kinds, files | same set, except below |
| Phantom nodes (`fmt.Print`, `store.len/append/make`, `sort.Strings`, `testing.T`, file `<nil>`) | absent in SQLite: AGENTS.md forbids symbols without a source anchor |
| Go method calls (`main -> Service.Report`, `main -> Service.Save`, `run -> Service.Load`, `Report -> Load`) | present only in SQLite (the `go/ast` resolver finds them; the Kùzu extractor did not) |
| Go declaration line | Kùzu anchors at the doc comment (`service.go:20`), format 2 at the `func` line (`:21`); `mcp_navigate_validate` is compared ignoring the line, and asserts `:21` |
| Python/TypeScript/Rust symbols | from the format-2 extractor, so no `<script>`, `gap_` or `block` nodes; no CALLS edges without tree-sitter (A1), so `py.clean` has no callers and `rest_query_impact*` lacks `py.<script>` |
| `cli_communities`, MCP communities | counts differ (41 symbols + 10 file nodes = 51 nodes, 13 CALLS edges in the loader); the Go god nodes (`service/service.go`, `store/store.go`) are the same |
| `Graph.Edges()` | now truly directed: a mutual call yields two edges (before, one undirected pair, in a random direction) |
| `rest_stats` | `symbol_count` 41, `file_count` 10 (files with symbols), `calls_count` 21, `declares_count` 41; `db_path` is `<INDEXDIR>/fixture/fixture.db` |
| `rest_stats_absent` | 404 (Kùzu: 200 `available: true`, because `Open` created an empty database); also updated in the Kùzu golden |
| `rest_query_toc` | 404 on a code file in both paths (A0 froze a 500); Kùzu golden updated |
| `rest_query_cypher` | 400 `raw Cypher was removed; use callers/callees/symbols_in_file/impact` on format 2 (the Kùzu golden still records the old answer) |
| `rest_query_unknown` | message no longer lists `cypher`; Kùzu golden updated |
| `impact` arrays | `[]` instead of `null` when empty |
