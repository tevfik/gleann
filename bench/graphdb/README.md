# bench/graphdb: graph database benchmark

Measures a candidate graph database against gleann's SQLite code graph
(`internal/graphstore`, the reference) on one deterministic synthetic call
graph, so a database can be judged on numbers and on result parity before
anyone considers it for gleann.

This is a **nested Go module** (`github.com/tevfik/gleann/bench/graphdb`,
`replace github.com/tevfik/gleann => ../..`). It may import gleann's internal
packages because its path is below the root module's, and the root module's
`go mod tidy`, `go vet ./...` and `go test ./...` never see it, so candidate
databases and CGo dependencies stay out of the root `go.mod`.

## Run

```bash
cd bench/graphdb

# SQLite reference only, no CGo, any platform
go run ./cmd/graphbench -edges 10000

# with the Kùzu candidate (CGo)
CGO_ENABLED=1 go run -tags kuzu ./cmd/graphbench -backends sqlite,kuzu -edges 10000,100000

# comparable numbers: pin the cores (the runner warns when more than 4 are visible)
taskset -c 0-3 go run -tags kuzu ./cmd/graphbench -edges 1000000
```

From the repo root: `make bench-graphdb [GRAPHDB_TAGS=kuzu] [GRAPHDB_EDGES=10000,100000] [GRAPHDB_PIN="taskset -c 0-3"]`.

Flags: `-edges` (comma list, env `GLEANN_GRAPH_BENCH_EDGES`; symbols = edges/5,
files = edges/500), `-queries` (sampled start symbols per query type, 200),
`-paths` (shortest-path pairs, 20), `-backends` (default: every registered
one; the reference `sqlite` always runs first), `-results` (report directory,
default `results/`), `-workdir`, `-list`. A markdown report is printed and
`results/graphdb-<timestamp>.{json,md}` written. The exit status is 1 when a
candidate disagrees with the reference or fails.

Tests (`go test ./...`, SQLite only, no CGo) check the harness itself,
including that a deliberately wrong backend is caught. `go test -tags kuzu ./...`
also runs the Kùzu parity test (about 15 s).

## What is measured

Per backend and size: bulk load, update of one file (rename one symbol that
other files call), removal of another file, disk size after closing, peak RSS
growth (Linux; `VmHWM` reset per backend), and p50/p95 latency of callers,
callees and impact at depth 1 and 3, symbol search (limit 50), shortest path
and stats. Every result set is normalised (sorted FQNs, hop count) and compared
with the reference answer to the same query: callers, callees, impact (callers
and affected files), search, path length, and stats plus probe queries after the
update and the removal. Search answers that hit the 50-result cap are skipped
(a different ordering would pick a different subset).

## What counts as passing

* **Parity: 0 mismatches** against the reference in every row of the parity
  table. Known, documented differences must be listed by the adapter in
  `Caveats()` and are still counted; the maintainer decides if they are
  acceptable (for example Kùzu's path cap below).
* Latency, disk, load, update and memory are **reported side by side**, with no
  pass/fail threshold: the decision is the maintainer's.

## Add a candidate database

1. `cd bench/graphdb && go get <the database's Go binding>` (go.mod/go.sum of
   this module only).
2. Create `adapters/<name>/<name>.go` implementing `graphdb.Backend`
   (see `graphdb.go`, the contract is in the interface comments) and register it:
   `func init() { graphdb.Register("<name>", func() graphdb.Backend { return &backend{} }) }`.
   Copy `adapters/sqlite` (plain Go) or `adapters/kuzu` (CGo) as the template.
   If it needs CGo or is heavy, put `//go:build <name>` on the file and add an
   untagged `doc.go` with the package clause so `go test ./...` still works.
3. Import it for the runner: `cmd/graphbench/backends_<name>.go` containing
   `import _ ".../adapters/<name>"` (with the same build tag).
4. Run `go run -tags <name> ./cmd/graphbench -backends sqlite,<name> -edges 10000`
   and iterate until parity is 0; optionally add a parity test next to the
   adapter like `adapters/kuzu/kuzu_test.go`.

Adapter rules: results are sorted, duplicate-free FQN lists without the start
symbol; `ShortestPath` returns the hop count (CALLS walkable in both
directions) or `graphdb.NoPath`; `ApplyFileDelta` must keep inbound edges from
other files whose target is still declared and drop the rest with no dangling
edges; `DiskBytes` is called after `Close`.

## Last known numbers

Kùzu removed from gleann after this comparison (RFC 003). 1M edges (999K
unique stored), 200K symbols, 2K files, cores 0-3, p50:

| | Kùzu | SQLite (graphstore) |
|---|---:|---:|
| bulk load | 2.3 s | 7.4 s |
| disk | 52 MiB | 182 MiB |
| callers d3 | 125 ms | 1.6 ms |
| callees d3 | n/a | 1.8 ms |
| impact d3 | 298 ms | 1.7 ms |
| search | 34.5 ms | 2.2 ms |
| path | 0.43 s (found no path: 1000-node cap) | 5.6 ms |
| update one file | 865 ms | 140 ms |
| parity | n/a | 0 mismatches |

Those were measured with the earlier in-tree test; this harness reproduces the
same operations, so expect the same ballpark (the Kùzu adapter returns only
FQNs and sorts search results, so its figures differ slightly).

Reproduced with this harness at 100K edges (99,978 stored), cores 0-3, p50
(Kùzu parity 0 everywhere except path, 20 of 20 pairs: the 1000-node cap):

| | Kùzu | SQLite |
|---|---:|---:|
| bulk load | 1.07 s | 549 ms |
| disk | 10.9 MiB | 19.1 MiB |
| callers d3 | 54 ms | 0.8 ms |
| callees d3 | 55 ms | 0.8 ms |
| impact d3 | 125 ms | 0.8 ms |
| search | 3.4 ms | 0.4 ms |
| path | 237 ms | 1.0 ms |
| update one file | 317 ms | 78 ms |
| remove one file | 41 ms | 38 ms |
| peak RSS delta | 432 MiB | 75 MiB |

Kùzu notes: its load is faster and its disk smaller, but graph queries are two
orders of magnitude slower, and it needs CGo. Observed with go-kuzu 0.11.3:
`DETACH DELETE` of nodes leaves a stale row in the `CALLS` relationship count
(`count(r)` disagrees with the rows returned), so the adapter deletes
relationships explicitly before nodes. The Kùzu code in gleann did that too.

### Adapter semantics worth knowing when comparing

* `sqlite`: `RemoveFile` keeps the file's manifest row, so `Stats.Files` after a
  removal is backend-defined and not compared.
* `kuzu`: callers/callees/impact are a Go BFS over single-hop Cypher queries (as
  gleann's code did; variable-length paths enumerate paths, not nodes). The
  shortest path BFS stops after 1000 explored nodes, so path lookups on larger
  graphs report no path where the reference finds one: these are the path
  mismatches in the report.
