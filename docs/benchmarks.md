# Benchmarks

Three measurements, each with its harness in the repository:

1. [Retrieval quality](#retrieval-quality-gleann-benchmark): do the right files come back for a question?
2. [Vector search](#vector-search-benchvectordb): recall and latency of the HNSW and DiskANN backends against exact search.
3. [Graph database](#graph-database-benchmark-benchgraphdb): the SQLite code graph against candidate graph databases.

## Retrieval quality (`gleann benchmark`)

A task is a query and the files that answer it (the ContextBench / SWE-Bench task shape). Each strategy returns its top 10 passages; Recall@K is the share of a task's gold files among the first K distinct files, averaged over the tasks.

Setup of the run below (2026-11-20): this repository at commit `11f4538` (1,491 files, 30,807 passages), embedded with `granite-embedding-30m-english` (384 dimensions, in-process), HNSW backend, default settings. The task files themselves are kept out of the index (see the note at the end of this section).

### 121 natural-language tasks (`bench/tasks/gleann_nl.json`)

Questions written the way a developer or an agent asks them, without the identifiers of the answer.

| Strategy | Recall@1 | Recall@5 | Recall@10 | MRR | Coverage | Avg Tokens | Tokens vs BM25 | Latency |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **BM25** | 24.4% | 47.9% | 54.1% | 0.345 | 54.1% | 1545 | 0.0% | 10.2ms |
| **Vector (HNSW)** | 32.1% | 65.2% | 67.2% | 0.471 | 67.2% | 1289 | **-16.6%** | 106.4ms |
| **Hybrid (α=0.5)** | 37.6% | 63.9% | 72.2% | 0.502 | 72.2% | 1421 | **-8.0%** | 186.7ms |
| **GraphRAG** | 37.6% | 63.9% | 72.2% | 0.502 | 72.2% | 1421 | **-8.0%** | 188.0ms |
| **Hybrid (1 chunk/file)** | 40.1% | 69.3% | 75.9% | 0.538 | 75.9% | 1336 | **-13.5%** | 355.7ms |
| **Hybrid (α=0.3)** | 38.4% | 57.4% | 59.9% | 0.477 | 59.9% | 1589 | +2.9% | 187.3ms |
| **Hybrid (default, α=0.7)** | 38.7% | 66.8% | 70.9% | 0.519 | 70.9% | 1243 | **-19.6%** | 187.8ms |
| **Ripgrep** | 0.8% | 4.1% | 6.6% | 0.021 | 6.6% | 0 | **-100.0%** | 11.3ms |

- Best Recall@5: Hybrid (1 chunk/file) (69.3%).
- Best Recall@10: Hybrid (1 chunk/file) (75.9%).
- Best MRR: Hybrid (1 chunk/file) (0.538).
- GraphRAG and Hybrid (α=0.5) have the same recall and MRR: the graph expansion did not change the rank of a gold file in these tasks.
- 121 tasks: one task weighs 0.8 points, so a difference of one or two tasks is not significant.

### 19 keyword tasks (`bench/tasks/gleann.json`)

Queries made of identifiers and terms that occur in the gold files. With 19 tasks the rows differ by one to three tasks; read it as a sanity check.

| Strategy | Recall@1 | Recall@5 | Recall@10 | MRR | Coverage | Avg Tokens | Tokens vs BM25 | Latency |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **BM25** | 21.1% | 65.8% | 71.0% | 0.408 | 71.0% | 1442 | 0.0% | 14.9ms |
| **Vector (HNSW)** | 42.1% | 84.2% | 84.2% | 0.601 | 84.2% | 1274 | **-11.7%** | 140.3ms |
| **Hybrid (α=0.5)** | 39.5% | 73.7% | 84.2% | 0.571 | 84.2% | 1323 | **-8.2%** | 226.8ms |
| **GraphRAG** | 39.5% | 73.7% | 84.2% | 0.571 | 84.2% | 1323 | **-8.2%** | 226.4ms |
| **Hybrid (1 chunk/file)** | 34.2% | 79.0% | 89.5% | 0.561 | 89.5% | 1265 | **-12.3%** | 421.9ms |
| **Hybrid (α=0.3)** | 44.7% | 68.4% | 89.5% | 0.600 | 89.5% | 1414 | **-1.9%** | 227.4ms |
| **Hybrid (default, α=0.7)** | 34.2% | 79.0% | 89.5% | 0.568 | 89.5% | 1270 | **-11.9%** | 227.7ms |
| **Ripgrep** | 5.3% | 21.1% | 36.8% | 0.112 | 36.8% | 0 | **-100.0%** | 12.1ms |

- Best Recall@5: Vector (HNSW) (84.2%).
- Best Recall@10: Hybrid (1 chunk/file), Hybrid (α=0.3), Hybrid (default, α=0.7) (89.5%).
- Best MRR: Vector (HNSW) (0.601).
- GraphRAG and Hybrid (α=0.5) have the same recall and MRR: the graph expansion did not change the rank of a gold file in these tasks.
- 19 tasks: one task weighs 5.3 points, so a difference of one or two tasks is not significant.

### What the numbers say

- **Hybrid search is the best default on natural-language questions**: at the shipped weight (`α=0.7`) it reaches 70.9 % Recall@10 and 0.519 MRR, against 67.2 % / 0.471 for vector-only and 54.1 % / 0.345 for BM25. The `α=0.5` row measured a slightly higher Recall@10 (72.2 %) on the same tasks, with a slightly lower MRR (0.502). On keyword queries vector-only and hybrid at `α=0.5` are level at 84 %, while `α=0.7` reaches 89.5 %.
- **One passage per file helps most** (`--one-per-file`, 76 % Recall@10 on the 121 tasks): several chunks of one file otherwise use up the top-10 slots. It costs about twice the latency.
- **GraphRAG equals Hybrid (α=0.5) in this benchmark.** The benchmark scores which files are returned; the graph expansion adds caller and callee context to the passages but did not change the rank of a gold file in any task. Its value, if any, is in the added context, which this benchmark does not measure.
- **Plain `ripgrep` on the query words finds little** (7 % on natural-language questions): the words of a question are rarely the words of the code. The Ripgrep baseline runs `rg -F -c -i` with `--no-ignore-parent` and `--no-require-git` for each non-stopword query term in the index source directory, ranks files by matching-line count (`rg -c`), and counts the tokens of the retrieved files that an agent would read into context. If ripgrep is not installed, the row indicates so rather than reporting zeros.
- These are results on one repository (Go, with its documentation) and one small embedding model. They are not a claim about other corpora.

### Reproduce

```bash
gleann index build myrepo --docs . --graph
gleann benchmark --index myrepo --tasks bench/tasks/gleann_nl.json --output report.md
gleann benchmark --index myrepo --tasks bench/tasks/gleann_nl.json --compare "Hybrid (default, α=0.7),Hybrid (1 chunk/file)"
gleann benchmark --gen-tasks . --output tasks.json     # draft tasks for another repository
```

### Paired significance testing (`--compare`, `--per-task`)

To evaluate whether a strategy difference is statistically significant across tasks:

- `--per-task <file.jsonl>` (or `GLEANN_BENCH_PERTASK=<file.jsonl>`): writes task-level metrics (MRR, Recall@1/5/10, tokens, latency) as JSON lines.
- `--compare "StratA,StratB"`: computes paired statistics between two strategies in the benchmark run, reporting paired mean differences, deterministic 95% bootstrap confidence intervals (fixed seed), and exact two-sided sign test p-values under the table.
- `--compare fileA.jsonl,fileB.jsonl`: compares per-task output files from two separate benchmark runs or binaries.

Keep the tasks file out of the index. It holds every query verbatim, so it outranks the files that answer them: on an index that contained it, BM25 Recall@1 was 0 %. `gleann benchmark` now drops hits on the tasks file and says so; this repository's `.gleannignore` excludes `bench/tasks/`.

## Vector search (`bench/vectordb`)

`bench/vectordb` measures a vector store against brute-force ground truth: build time, recall@10 and latency over a sweep of the search width, filtered search at 1 / 10 / 50 % selectivity, updates, disk and memory. It runs on a synthetic Gaussian mixture or, with `-vectors`, on the real embeddings of a gleann index (`cmd/exportvecs`). See [`bench/vectordb/README.md`](../bench/vectordb/README.md).

HNSW on the real embeddings of the index above (30,807 vectors x 384, 1,000 of them held out as queries, 4 CPUs):

| ef_search | recall@10 | p50 | p95 |
|---:|---:|---:|---:|
| 16 | 0.997 | 0.3 ms | 0.5 ms |
| 32 | 1.000 | 0.5 ms | 0.7 ms |
| 128 (default) | 1.000 | 1.1 ms | 1.7 ms |

Filtered search on the same data: recall@10 1.000 at 1 %, 10 % and 50 % selectivity.

A recall gate (`bench/vectordb/gate`) runs in CI on a 10,000-vector dataset and fails when recall@10 of the backends or of gleann's filtered search drops below its thresholds.

## Graph database benchmark (`bench/graphdb`)

A pluggable harness compares any graph database against gleann's SQLite code
graph (`internal/graphstore`) on the same synthetic graph: bulk load, p50/p95
latency of callers/callees/impact (depth 1 and 3), symbol search, shortest
path, one-file update, disk, memory, and result parity. It is a separate Go
module (`bench/graphdb`), so candidate databases and CGo never reach the root
`go.mod`. Kùzu, removed from gleann, is the first candidate.

```bash
make bench-graphdb                                    # SQLite reference only, 10K edges
make bench-graphdb GRAPHDB_TAGS=kuzu GRAPHDB_EDGES=100000,1000000   # with Kùzu (CGo)
```

See [`bench/graphdb/README.md`](../bench/graphdb/README.md) for how to add a
candidate and the last known numbers, and
[decisions/kuzu-to-sqlite.md](decisions/kuzu-to-sqlite.md) for the comparison
that led to the removal of Kùzu.
