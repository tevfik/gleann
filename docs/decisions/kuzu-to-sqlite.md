# Decision: the code graph moves from Kùzu to SQLite (v1.20.0)

Status: accepted 2026-11-17, implemented in v1.20.0. Plan: [RFC 004](../plans/004-v1.20-sqlite-only.md). Storage design: [RFC 003](../plans/003-storage.md).

## Summary

From v1.20.0 gleann keeps the code graph in the same SQLite database as the passages, the FTS5 lexical index and the manifest (`<IndexDir>/<name>/<name>.db`). The embedded Kùzu graph database is removed. On the same graph, SQLite gives the same answers and is faster on every query and on incremental updates. Kùzu was better at bulk loading and on disk; after a schema rework (RFC 004 item E) a full SQLite load takes about 1.6 times as long as Kùzu's and the graph is about 1.45 times as large, and we accept that.

## Why

1. **Faster where users wait.** Queries and one-file updates are what `graph explain`, MCP navigation and `index sync` do all day. SQLite is 15 to 175 times faster on these queries and 6 times faster on a one-file update (table below, before item E; after E the gap is wider).
2. **A code graph in every build.** Kùzu needs CGo and a shipped shared library (`libkuzu`). The default `go install` build, `CGO_ENABLED=0` builds and Windows had no code graph at all. SQLite (`modernc.org/sqlite`, pure Go) works in every build on every supported platform.
3. **One store, one transaction.** Passages, lexical index, manifest and graph used to live in different stores, so a sync could leave them out of step after a crash. In one SQLite file they change in one transaction per source file.
4. **One write path.** Two graph engines meant two writers, build-tag pairs, stubs, a raw-Cypher endpoint and separate tests for each. One engine removes about 11,000 lines.
5. **Same answers.** See "Parity" below.

## Measurements

Synthetic graph with 1M CALLS edges (999K unique stored, 200K symbols, 2K files), p50, pinned to cores 0-3, linux/amd64. Harness: `bench/graphdb` (a separate Go module). "SQLite, before E" is the text-keyed schema the decision was taken on; "SQLite, after E" is the integer-keyed schema that ships in v1.20.0 (commit "perf(graphstore): faster bulk load and smaller code graph", median of 3 runs).

| Operation | Kùzu | SQLite, before E | SQLite, after E (v1.20.0) |
|---|---:|---:|---:|
| callers, depth 3 | 125 ms | 1.6 ms | 0.71 ms |
| impact, depth 3 | 298 ms | 1.7 ms | 0.73 ms |
| symbol search | 34.5 ms | 2.2 ms | 1.47 ms |
| shortest path | 0.43 s (no path found, node cap) | 5.6 ms | 2.61 ms |
| one-file update | 865 ms | 140 ms | 102 ms |
| full load | **2.3 s** | 7.4 s | 3.77 s |
| disk | **52 MiB** | 182 MiB | 75.3 MiB |

At 100K edges, after E: load 339 ms, disk 8.0 MiB. On a real build of this repository (12,224 symbols, 35,620 edges) the graph build went from 1.27 s to 0.85 s and the graph tables from 16.9 to 7.8 MiB with item E.

A full graph load happens once per `index build --graph`; it is not on the query path. Disk use is small next to the vector file of the same index. Item E changed the schema with a named migration step (`graph_ids`): an existing format-2 graph is converted in place on first open, without re-extraction (7 s at 1M edges).

## Parity

Two independent checks:

1. **Goldens frozen before removal (RFC 004 A0).** While Kùzu was still in the tree, a small fixture project (Go, Python, TypeScript and Rust, cross-file calls, an interface, a test file) was indexed through the Kùzu path and its answers were written to `internal/graph/testdata/analysis_golden/`: callers and callees of every non-Go symbol, `graph map|risk|communities|export|report`, the MCP tools `gleann_communities`, `gleann_risk_analysis`, `gleann_repo_map`, `gleann_navigate_symbol`, and the REST graph queries. Parts that Kùzu itself produced non-deterministically (community membership, PageRank order, edge direction) are kept only in their invariant form. The SQLite path is tested against these files (`go test ./cmd/gleann ./internal/mcp ./internal/server -run SQLite`, `go test -tags treesitter ./internal/indexing -run TestFormat2NonGoCallsMatchKuzuGolden`, `go test -run TestGraphDBGoldenSQLite ./pkg/gleann`). Every difference is explained in the golden README; they are deliberate: no phantom nodes for standard-library and built-in calls (they are external edges), a call inside a method is charged to the method instead of its class, method calls between Go methods are found, a Go declaration is anchored at its `func` line, and top-level calls of a file get a real anchored `<script:file>` symbol.
2. **Synthetic graph (`bench/graphdb`).** The harness normalises every answer (sorted FQNs, hop count) and compares each candidate with the SQLite reference: callers, callees and impact at depth 1 and 3, symbol search, path length, stats, and probe queries after a one-file update and a file removal. Kùzu had 0 mismatches at 1M edges on everything except the shortest path, where its 1000-node BFS cap made it report no path where one exists (at 100K edges: 20 of 20 sampled pairs). Item E was checked the same way against the pre-E SQLite schema: identical answers at 10K, 100K and 1M edges, except 1 of 200 sampled path pairs at 1M that reports a different path of the same length.

## What it costs

- **Old graphs are not read.** A v1.9 `<name>_graph` directory is ignored, and every command except `gleann index migrate` refuses an index of the old store format with `index X uses the old store format; run: gleann index migrate X`. `gleann index migrate <name>` converts the index and rebuilds its graph from the index's source directory; `--cleanup` and `index remove` delete the old directory.
- **No raw Cypher.** `POST /api/graph/{name}/query` with `type: "cypher"` answers 400 and the UI "Raw Cypher" option is gone; the typed queries (callers, callees, symbols in file, impact, toc) remain, and `gleann graph export --format cypher` still writes Neo4j statements for use elsewhere.
- **Bulk load and disk.** A full graph load takes about 1.6 times as long as Kùzu's and the graph is about 1.45 times as large (after E, 1M edges).
- **Memory entities in Kùzu.** Entities that v1.9 stored in `<name>_memory` are carried over once with `kuzu-memory-export` (a separate module with the Kùzu binding, released as prebuilt binaries for linux/amd64, linux/arm64 and darwin/arm64) and `gleann memory import-graph` (see [memory_engine.md](../memory_engine.md#upgrading-entities-from-v19)).

## Evaluating another database later

`bench/graphdb` runs the same workload (load, callers/callees/impact, path, search, one-file update, file removal, disk, peak RSS) against any backend that implements its `Backend` interface, and checks parity against SQLite. The Kùzu adapter stays there behind the `kuzu` build tag as the reference candidate. To evaluate a new database (see [bench/graphdb/README.md](../../bench/graphdb/README.md)):

```bash
cd bench/graphdb
go get <the database's Go binding>          # this module's go.mod only
# add adapters/<name>/<name>.go (implements graphdb.Backend, registers itself)
# and cmd/graphbench/backends_<name>.go (imports it, same build tag)
go run -tags <name> ./cmd/graphbench -backends sqlite,<name> -edges 10000

# comparable numbers: pin the cores
taskset -c 0-3 go run -tags <name> ./cmd/graphbench -backends sqlite,<name> -edges 100000,1000000

# re-check the Kùzu numbers (CGo)
CGO_ENABLED=1 go run -tags kuzu ./cmd/graphbench -backends sqlite,kuzu -edges 100000
```

From the repository root: `make bench-graphdb GRAPHDB_TAGS=<name> GRAPHDB_EDGES=100000,1000000 GRAPHDB_PIN="taskset -c 0-3"`. The report is printed and written to `results/graphdb-<timestamp>.{json,md}`; the exit status is 1 when a candidate disagrees with the reference. Nothing in the main module changes. A candidate is worth considering only with 0 parity mismatches and a clear gain on the query and update rows, since those are what users wait for.
