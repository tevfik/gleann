# Analysis goldens of the Kùzu path (RFC 004 A0)

These files freeze what gleann v1.9.x answers on the **Kùzu path** (a format-1
index built in a `-tags treesitter` binary, graph store `<name>_graph`) for the
small project in `../analysis_fixture/` (Go, Python, TypeScript and Rust files,
about 290 lines, cross-file calls, an interface, a test file). Later RFC 004
work (A1 tree-sitter CALLS in `ExtractFile`, A2 analysis on the SQLite graph,
A3 Kùzu removal, C memory) must prove it changed nothing, or explain every
difference.

The goldens were produced with `GLEANN_STORE_FORMAT` unset (format 1) by
`indexing.BuildGraphIndex` on the fixture, which is the graph step of
`gleann index build --graph` for a format-1 index. There is no `<name>.meta.json`,
so the Kùzu code path is taken.

## Regenerate / compare

Every test below rebuilds the graph and compares with the files; add `-update`
to rewrite them (only when a change is intended and explained).

```
go test -tags treesitter ./internal/graph/analysisgolden ./cmd/gleann ./internal/mcp ./internal/server \
    -run 'TestKuzuCallsBaseline|TestGraphAnalysisGolden|TestGraphToolsGolden|TestGraphRESTGolden' [-update]
go test -tags treesitter -run TestGraphDBGoldenKuzu ./pkg/gleann [-update]   # pkg/gleann/testdata/graph_golden
```

Shared helpers (fixture path, index build, normalisation, compare/update) are in
`internal/graph/analysisgolden`. The tests were run repeatedly to check that they
are reproducible.

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
| `calls_nongo.json` | `analysisgolden.TestKuzuCallsBaseline`: for every Python/TypeScript/Rust symbol of the Kùzu graph, `GraphDB.Callees` and `GraphDB.Callers` (kind, file, line of each) | exact | A1: the SQLite graph (`gleann.GraphDB`) must give the same callers/callees for the same FQNs. Known expected differences: the Kùzu rows with `file: null` (built-ins and member calls such as `py.strip`, `ts.map`, `rs.new`) are phantom symbols without a source anchor, which AGENTS.md section 2 forbids; A1 may tag them external or drop them, and must say which. Calls made by methods are attributed to the class (`py.Registry` calls `py.clean`; `ts.Square` calls `ts.slug`); Kùzu also lists callers by base name, see the `main` note below. Top-level Python code is the symbol `py.<script>` |
| `calls_go.json` | same test, Go packages (go/ast extractor) | exact | A2 reference for Go; no Go interface dispatch edges exist (`Service.Save -> Store.Put` is absent), IMPLEMENTS edges are 0 |
| `cli_map.txt` | `gleann graph map --index fixture --top 1000` (`cmdGraphMap`) | files and the symbols listed per file, sorted, no scores | same set of files and symbols |
| `cli_risk.txt` | `gleann graph risk --index fixture --top 1000` | symbol, kind, total degree | same symbols, kinds, in+out |
| `cli_risk_by_file.txt` | `gleann graph risk --by-file --top 1000` | file names | same files |
| `cli_communities.txt` | `gleann graph communities` | the counts (85 nodes, 94 edges) and the god nodes (kind, name, in/out/total) | same counts, same god nodes |
| `cli_export.graphml` | `gleann graph export --format graphml` | nodes in full (label, kind, file), edges as sorted undirected pairs written as comments | same nodes and the same undirected edge pairs |
| `cli_export_cypher.txt` | `gleann graph export --format cypher` | node statements in full, edges as sorted undirected pairs | same |
| `cli_report.md` | `gleann graph report --docs <fixture> --output GRAPH_REPORT.md` | headings, Source line, Nodes/Edges/God Nodes counts and the god-node rows (rank dropped, sorted) | same; the community sections are present but their content is not compared |
| `cli_viz_graph.txt` | `gleann graph viz --output x.html`; the graph data embedded in the page (`nodesData`, `edgesData`, `godNodes`, `physicsEnabled`) | nodes (id, label, shape, size, colour), undirected edge pairs, god nodes | same; `group` (community id), edge colour/width and the tooltip are not compared |
| `mcp_communities.txt` | MCP `gleann_communities` (raw JSON part) | node/edge counts and god nodes | same |
| `mcp_risk_analysis.txt`, `mcp_risk_analysis_by_file.txt` | MCP `gleann_risk_analysis` with `top=1000` (and `by_file`) | as the CLI risk files | same |
| `mcp_repo_map.txt` | MCP `gleann_repo_map` with `top_k=1000`, `max_tokens=1000000` | files and the `kind name` lines under each, sorted | same |
| `mcp_navigate_*.txt` | MCP `gleann_navigate_symbol` for `validate` (Go), `clean` (depth 2), `slug` (depth 3), `clamp` (Rust), `main` (four matches in four languages) and an unknown symbol | exact, with the caller/callee lines and the matches sorted | same text |
| `rest_stats.json`, `rest_stats_absent.json` | `GET /api/graph/{name}` (existing graph; an index without a graph) | exact | A2: `symbol_count` 75, `file_count` 10, `calls_count` 45, `declares_count` 49 must hold for the same data; `db_path` will change. Kùzu reports `available: true` for an absent index because `Open` creates an empty database; A2 may answer differently and must say so |
| `rest_query_{callers,callers_short,callees,callees_rust,symbols_in_file,symbols_in_file_go,impact,impact_default_depth}.json` | `POST /api/graph/{name}/query` | exact, response arrays sorted | same status and body |
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

## pkg/gleann/testdata/graph_golden

Separate from this directory: `basic.json`, `resolution.json`,
`selfrecursion.json`, `multiseed.json` hold the answers of the Kùzu `GraphDB`
for the fixtures of `pkg/gleann/graphdb_parity_test.go` (all `GraphDB` methods
with a call graph). `TestGraphDBGoldenSQLite` (every build) checks the format-2
SQLite `GraphDB` against them; `TestGraphDBGoldenKuzu` (treesitter) regenerates
them with `-update`. The only normalisation is that an empty `ShortestPath`
result is `[]` for both backends (Kùzu returned `nil`).
