# RFC 006: Vector database benchmark (bench/vectordb)

Status: approved by the maintainer (2026-11-17); step 1 implemented (bench/vectordb, 2026-11-19), steps 2-7 open
Builds on: RFC 004 section 5 (bench/graphdb, the pluggable graph benchmark), RFC 005 (incremental vectors)
Release: none. This is a measurement tool; it changes nothing in gleann. A decision to adopt a candidate would be a separate RFC.

## 1. Problem

gleann stores vectors in its own backends: HNSW (`modules/hnsw`, default) and DiskANN (`modules/diskann`), plus FAISS behind `-tags faiss`. They live next to the index database `<name>/<name>.db`, are published crash-safely with it (RFC 003 section 3.4) and are updated incrementally (RFC 005).

Dedicated vector databases such as Qdrant and Chroma are a natural question: are they faster, more accurate, or cheaper to update than our backends? We have never measured them. The only numbers we have are for our own backends (gleann-rag-test `results/scale/v120-README.md`):

| 1M passages, fake embedder | v1.20 |
|---|---:|
| warm search p50 / p95 | 41 ms / 274 ms |
| cold first query | 1.85 s |
| one-file sync | 2.5 s |
| merge / compaction | 1361 s / 1397 s |
| serve peak RSS | 1.24 GB |
| index on disk | 2.15 GB |

The merge cost is our known weak spot: it rebuilds the HNSW graph from stored vectors, while Qdrant inserts into a live graph.

Kùzu was judged the same way: we built `bench/graphdb`, measured it and removed Kùzu on the numbers. This RFC does the same for vectors.

## 2. Goals and non-goals

Goals:
- G1. One harness that measures any vector store on the same deterministic data and queries, and reports latency, recall, update cost, memory and disk side by side.
- G2. Every candidate is checked against exact search, so a fast but inaccurate store cannot look good.
- G3. Candidates and their dependencies (CGo, Docker, client libraries) never reach gleann's root `go.mod`.
- G4. Adding a candidate is one adapter package plus one registration file.

Non-goals:
- Changing gleann's default backend. The core invariants in AGENTS.md (single static binary, `CGO_ENABLED=0`, no Python, no mandatory Docker, every platform) rule out any store that needs a server process or a C library as the default.
- A general vector database leaderboard. Workloads are gleann's: code and document passages, top-10 retrieval, metadata filters, file-level updates.

## 3. Candidates

| Candidate | Kind | Go access | How it runs in the benchmark |
|---|---|---|---|
| `hnsw` (reference) | gleann, in process | `modules/hnsw` | always |
| `diskann` | gleann, in process | `modules/diskann` | always |
| `faiss` | gleann, CGo | `pkg/backends/faiss` | tag `faiss`, when libfaiss is installed |
| Qdrant | server (Rust) | official Go client (gRPC) | Docker container started by the runner, tag `qdrant` |
| Chroma | server (Python, Rust core) | HTTP API | Docker container, tag `chroma` |
| LanceDB | embedded (Rust) | Go binding, CGo | tag `lancedb`; feasibility checked in step 5 |
| sqlite-vec | SQLite extension (C) | `mattn/go-sqlite3` with the extension, CGo | tag `sqlitevec`; feasibility checked in step 5 |

pgvector, Milvus and Weaviate are left out of the first round (server-only like Qdrant, with no gleann-specific reason to expect a different result). Each can be added later as one more adapter.

## 4. Design

### 4.1 Module and layout

Like `bench/graphdb`: a nested module `github.com/tevfik/gleann/bench/vectordb` with `replace github.com/tevfik/gleann => ../..`.

```
bench/vectordb/
  vectordb.go        Store interface, registry, data types
  dataset.go         deterministic synthetic vectors, .fvecs/.ivecs loader
  truth.go           exact top-k ground truth (cached per dataset)
  run.go             runner: phases, timing, RSS, disk
  report.go          markdown + JSON report
  adapters/hnsw/     reference (modules/hnsw)
  adapters/diskann/
  adapters/qdrant/   //go:build qdrant
  adapters/chroma/   //go:build chroma
  adapters/lancedb/  //go:build lancedb
  adapters/sqlitevec///go:build sqlitevec
  cmd/vectorbench/   main + one backends_<name>.go per tag
  docker/            compose file pinning server images by digest
  results/           git-ignored reports
```

### 4.2 Interface

```go
type Store interface {
    Name() string
    Caveats() []string // documented differences, listed in the report
    Open(ctx context.Context, dir string, p Params) error
    Insert(ctx context.Context, ids []int64, vecs [][]float32, meta []Meta) error // bulk load
    Upsert(ctx context.Context, ids []int64, vecs [][]float32, meta []Meta) error // incremental
    Delete(ctx context.Context, ids []int64) error
    Flush(ctx context.Context) error // make writes visible and durable
    Search(ctx context.Context, q []float32, k int, f *Filter) ([]int64, error)
    Stats(ctx context.Context) (Stats, error) // count, disk bytes, server RSS if known
    Close() error
}

type Params struct {
    Dims     int
    Metric   string // "cosine" | "l2" | "ip"
    M, EfConstruction int
    EfSearch int    // swept by the runner, see 4.4
}

type Meta struct {
    Source string // file path, for file-level updates and filters
    Kind   string // "code" | "prose"
    Tag    int    // 0..99, for filters of known selectivity
}
```

Server adapters measure server memory through the container's cgroup (`docker stats` or `/sys/fs/cgroup`) and disk through the mounted volume, so they are not flattered by running outside our process.

### 4.3 Data

- **Synthetic (default):** a deterministic Gaussian mixture (seeded, 256 clusters, unit-normalised) at 384 and 768 dimensions, 10K / 100K / 1M vectors, 1000 queries drawn from the same distribution. Metadata is assigned so filters of 1 %, 10 % and 50 % selectivity exist, and passages are grouped into files of ~24 passages (the measured average of the gleann corpus).
- **Real (optional):** `.fvecs`/`.ivecs` files, so a set of real embeddings (for example the gleann quality corpus embedded once with the default model) can be used without a model at benchmark time.
- **Ground truth:** exact top-100 by brute force, computed once per dataset and cached next to it.

### 4.4 Fair comparison

Latency means little without recall, so each store is swept over its search parameter (`ef`/`ef_search`/`nprobe`) and the report gives:
- latency p50/p95 and throughput at recall@10 ≥ 0.90, 0.95 and 0.99 (the cheapest setting that reaches each);
- the recall/latency curve.

Build parameters are matched where the store allows it (HNSW M=32, ef_construction=200); differences are listed in `Caveats()`. Quantization is off by default and measured as a separate configuration.

### 4.5 Phases measured

| Phase | What | Reported |
|---|---|---|
| Load | bulk insert of all vectors + flush | wall time, peak RSS, disk |
| Cold start | close, reopen, first query | time to first answer |
| Search | 1000 queries, k=10, concurrency 1 and 8 | p50/p95/p99, QPS, recall@10 per sweep point |
| Filtered search | selectivity 1 %, 10 %, 50 % | p50/p95, recall@10 against exact filtered truth |
| File update | replace the passages of one file (upsert 24, delete stale) | wall time until visible |
| Churn | 50 rounds: 1 % of files changed, 10 % deleted overall | recall@10 drift against a fresh load of the final set |
| Merge/compaction | the store's own maintenance, if any, triggered as the store documents | wall time, RSS |
| Footprint | after closing | disk bytes, server RSS at rest |

The churn phase is the comparison that matters most for gleann (RFC 005 G6 measured our own drift: 0.941 against 0.917 for a fresh build). Gleann's merge is measured through the same `UpdateIndex` path the CLI uses, so the 23-minute 1M merge is compared with Qdrant's live insertion like for like.

### 4.6 Runner and reproducibility

- `go run ./cmd/vectorbench -stores hnsw,diskann,qdrant -sizes 10000,100000 -dims 384` (tags as needed); `make bench-vectordb` from the root.
- Measurements are pinned to cores 0-3; server containers get the same CPU set (`--cpuset-cpus 0-3`) and a memory limit equal to the in-process stores' cgroup cap, so no candidate gets more hardware.
- Server images are pinned by digest in `docker/compose.yml`; the report records image digests, client versions, Go version, CPU and kernel.
- Results: `results/vectordb-<timestamp>.{json,md}`; a result is committed only by quoting its table in a doc.

## 5. Build tag and platform impact

None for gleann. The root module, its `go.mod`, its builds and its CI are unchanged. Inside the nested module the default build has only the gleann adapters (pure Go, any platform). Candidate adapters are behind tags; Docker is needed only to run the server candidates, only on the benchmark machine.

## 6. Decision rules

A candidate is reported as worth an adoption RFC only if, on the 100K and 1M synthetic sets at 768 dimensions and at recall@10 ≥ 0.95, it beats the `hnsw` reference clearly on at least one of:
- search p95 by 2× or more at concurrency 8;
- the file update or the merge/compaction by 10× or more;
- churn drift (recall loss after 50 rounds) by 0.02 or more;
- filtered recall at 1 % selectivity by 0.05 or more;

and is not worse by more than 1.5× on memory or disk.

Even then, a server store (Qdrant, Chroma) can only become an optional backend for server deployments (like `-tags faiss`), never the default. The likely outcome for gleann's default is to borrow the winning technique, for example inserting the delta into the live HNSW graph (RFC 005's planned `AddVectorsWithIDs`).

## 7. Steps

1. Harness, synthetic dataset, ground truth, report; adapters `hnsw` and `diskann`; harness tests (a deliberately wrong store must fail recall). No CGo, no Docker.
2. Qdrant adapter and compose file; parity of results on a 10K set; cgroup memory measurement.
3. Chroma adapter.
4. Churn and merge phases through gleann's real `UpdateIndex` for the reference.
5. Feasibility of LanceDB and sqlite-vec adapters (CGo builds on linux/amd64); add them if they build cleanly, otherwise record why not.
6. Full run at 10K/100K/1M × 384/768, report in `bench/vectordb/README.md` and a summary in this RFC.
7. Decision by the maintainer per section 6.

Rough effort: steps 1–4 about two working days of agent time; step 6 is mostly machine time (the 1M runs take hours per store).

## 8. Test and verification

| Layer | Test |
|---|---|
| harness | ground truth equals brute force on a small set; recall computed correctly; a store that returns random IDs fails; report round-trips through JSON |
| adapters | each adapter's own test inserts 1K vectors, searches, upserts, deletes, filters, and compares with exact search (tag-gated for candidates) |
| CI | `go vet` and `go test ./...` of the nested module with the default tags (gleann adapters only), like `bench/graphdb` |

## 9. Risks

- **Unfair setup.** Defaults differ across stores. Mitigation: the recall-matched sweep, matched build parameters, the same CPU set and memory cap, and `Caveats()` in the report.
- **Network overhead hides store speed.** Server stores pay gRPC/HTTP per query. The report shows it as part of the real cost (gleann would pay it too), and, where the store exposes server-side timing, also the server-only latency.
- **Image and client drift.** Pinned digests and recorded versions; re-running a year later must reproduce or explain differences.
- **Docker on the benchmark machine.** Allowed here (a benchmark, not a gleann requirement); the default build of the harness never needs it.

## 10. Audit log
- 2026-11-17: drafted after the maintainer asked why gleann does not use Qdrant or Chroma; approved by the maintainer the same day.
