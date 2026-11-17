# ContextBench / SWE-Bench Retrieval Benchmark Report

> Evaluated across **20 tasks** comparing BM25 keyword search, DiskANN+PQ vector search, and GraphRAG.

| Strategy | Recall@1 | Recall@5 | Recall@10 | MRR | Coverage | Avg Tokens | Token Savings | Latency |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **BM25** | 50.0% | 70.0% | 95.0% | 0.615 | 95.0% | 1323 | 0.0% | 13.5ms |
| **Vector (DiskANN+PQ)** | 40.0% | 80.0% | 85.0% | 0.575 | 85.0% | 1141 | **-13.8%** | 97.4ms |
| **Hybrid** | 55.0% | 80.0% | 80.0% | 0.652 | 80.0% | 1166 | **-11.9%** | 164.6ms |
| **GraphRAG** | 55.0% | 80.0% | 80.0% | 0.652 | 80.0% | 1161 | **-12.2%** | 211.5ms |
| **Ripgrep** | 10.0% | 35.0% | 35.0% | 0.183 | 35.0% | 0 | 100.0% | 554.2ms |

### Key Takeaways

- 🚀 **1.1x Higher Recall**: GraphRAG achieved 80.0% Recall@5 vs BM25's 70.0%.
- 💰 **12.2% Token Savings**: Agents navigate directly to relevant definitions, saving context tokens.

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
