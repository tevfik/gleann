# bench/vectordb: vector database benchmark

Measures candidate vector databases against gleann's vector backends
(`modules/hnsw`, `modules/diskann`) on deterministic synthetic datasets
(Gaussian mixture, 256 clusters, unit-normalised, 384/768 dimensions), so a
database can be judged on numbers, recall, update cost, memory and disk
footprint before anyone considers it for gleann (RFC 006).

This is a **nested Go module** (`github.com/tevfik/gleann/bench/vectordb`,
`replace github.com/tevfik/gleann => ../..`). It may import gleann's internal
packages because its path is below the root module's, while the root module's
`go mod tidy`, `go vet ./...` and `go test ./...` never see it. Candidate
databases, client SDKs, CGo, and Docker clients stay strictly out of the root
`go.mod`.

Status: RFC 006 step 1. The harness and the two reference adapters (`hnsw`,
`diskann`) exist; no candidate database has an adapter yet.

## Run

```bash
cd bench/vectordb

# Default run: hnsw and diskann on 10,000 vectors at 384 dimensions
go run ./cmd/vectorbench -sizes 10000 -dims 384

# Comparable numbers: pin the run to cores 0-3
taskset -c 0-3 go run ./cmd/vectorbench -sizes 10000 -dims 384

# Sweep multiple sizes
taskset -c 0-3 go run ./cmd/vectorbench -sizes 10000,100000 -dims 384 -stores hnsw,diskann

# List registered stores
go run ./cmd/vectorbench -list
```

From the repository root:

```bash
make bench-vectordb [VECTORDB_SIZES=10000] [VECTORDB_DIMS=384] [VECTORDB_TAGS=<adapter tag>] [VECTORDB_PIN="taskset -c 0-3"]
```

### Flags

- `-stores`: comma-separated stores to run (default: every registered one; e.g. `hnsw,diskann`).
- `-sizes`: comma-separated vector counts (default: `10000`).
- `-dims`: vector dimensionality (default: `384`).
- `-queries`: sampled queries per run (default: `1000`).
- `-k`: top-k nearest neighbors (default: `10`).
- `-results`: output directory for JSON and Markdown reports (default: `results/`).
- `-workdir`: parent directory for scratch databases (default: system temp).
- `-dup`: share of vectors (0-1) replaced by near-copies of 20 base vectors, as in real corpora with license headers, boilerplate or repeated table text (default: `0`). Graph indexes whose neighbour selection rejects candidates that are closer to each other than to the node can starve on such data; `-dup 0.3` or `0.6` shows it.
- `-list`: list registered stores and exit.

- `-vectors`: a `.fvecs` file of real embedding vectors to run on instead of the synthetic mixture; `-queries` of them are held out as queries, and `-sizes` / `-dims` come from the file.

## Real embeddings

Synthetic clusters are an easy case. To measure on the embeddings of a real corpus, export the vectors of a gleann HNSW index (it must store its vectors, the default):

```bash
gleann index build myrepo --docs /path/to/repo --backend hnsw
go run ./cmd/exportvecs -index ~/.gleann/indexes/myrepo/myrepo.index -out /tmp/myrepo.fvecs
go run ./cmd/vectorbench -vectors /tmp/myrepo.fvecs
```

## Other commands

- `cmd/filtbench` measures gleann's **real** search path (`gleann.NewBuilder`, `LeannSearcher.Search` with metadata filters) against the same ground truth, for `-backend hnsw` or `diskann`, with `-ef` as the search width. The adapters above call the backend modules directly; this goes through the index format, the record store and the filter logic.
- `cmd/buildbench` times a build through `gleann.NewBuilder` (passage staging, then the vector index) with `-cpuprofile` / `-memprofile`, for `-backend hnsw` or `diskann`. Run the same file on two checkouts to compare their build cost.
- `gate/` is the recall gate: `go test -run TestRecallGate ./gate/` (or `make recall-gate` from the repository root) builds 10,000-vector indexes and fails when recall@10 is below the thresholds in `gate/thresholds.go`. CI runs it on every change.
- `competitive/` compares the HNSW and BM25 modules with chromem-go (brute force): `go test -v -run TestCompetitive ./competitive/`.

## What is measured

Per store and dataset size:
1. **Load (bulk)**: wall time to insert all vectors and flush to disk; peak RSS growth (`VmHWM` reset per store on Linux); disk bytes on disk.
2. **Cold start**: time to reopen the database on disk and return the first query answer.
3. **Search with ef sweep**: queries evaluated with sweep over search beam parameter (`ef`/`search_l`) at concurrency 1 and 8. Reports p50/p95/p99 latency, QPS, and average recall@10 against exact brute-force ground truth.
4. **Recall-matched rows**: cheapest `ef` setting achieving recall@10 ≥ 0.90, ≥ 0.95, and ≥ 0.99.
5. **Filtered search**: metadata filters of known selectivity (1%, 10%, 50%), evaluated against exact filtered ground truth.
6. **File update**: replacing all passages of one file (~24 passages) via `Upsert` + `Flush`, measuring wall time until visible in queries.
7. **Footprint**: total database size on disk and peak memory.

## What counts as passing

* **Recall@10 ≥ 0.90** on the search sweep. A store returning inaccurate or random IDs fails the run.
* Differences in filtering or update support are documented in `Caveats()` and listed in the report.
* Decision rules (RFC 006 section 6): To justify an adoption RFC, a candidate must beat the reference on 100K/1M sets at recall@10 ≥ 0.95 by 2× search p95 at concurrency 8, 10× file update, 0.05 filtered recall at 1% selectivity, while being within 1.5× memory and disk.

## Add a candidate database

1. `cd bench/vectordb && go get <candidate Go client or binding>` (in this nested module only).
2. Create `adapters/<name>/<name>.go` implementing `vectordb.Store`:
   ```go
   func init() {
       vectordb.Register("<name>", func() vectordb.Store { return &Store{} })
   }
   ```
   If candidate requires CGo or external daemon, add `//go:build <name>` build tag.
3. Import it in `cmd/vectorbench/backends_<name>.go`:
   ```go
   //go:build <name>
   package main
   import _ "github.com/tevfik/gleann/bench/vectordb/adapters/<name>"
   ```
4. Test and verify parity:
   ```bash
   go test -v ./adapters/<name>/...
   go run -tags <name> ./cmd/vectorbench -stores <name> -sizes 1000
   ```
