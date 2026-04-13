# Benchmarks & Performance Reports

All benchmarks on Intel i9-13900H (20 threads), Go 1.22, Linux.

## HNSW Search Performance

| Dataset Size | ef | Latency | QPS | Recall@10 |
|-------------|-----|---------|-----|-----------|
| 1,000 | 32 | 325µs | 3,078 | 79.8% |
| 1,000 | 64 | 586µs | 1,707 | 92.5% |
| 1,000 | 128 | 717µs | 1,394 | 98.8% |
| 1,000 | 256 | 1.08ms | 930 | 99.9% |
| 5,000 | 32 | 657µs | 1,522 | — |
| 5,000 | 128 | 1.48ms | 675 | — |
| 10,000 | 128 | 2.91ms | 344 | — |

## Storage Reduction (CSR + Pruning)

| Embedding Dim | Vectors | Full Size | Pruned Size | **Savings** |
|--------------|---------|-----------|-------------|-------------|
| 128 | 1,000 | 927 KB | 427 KB | **53.9%** |
| 384 | 1,000 | 1,928 KB | 429 KB | **77.7%** |
| 768 | 1,000 | 3,428 KB | 431 KB | **87.4%** |
| 768 | 5,000 | 17,156 KB | 2,159 KB | **87.4%** |

## Memory Savings (In-Memory Graph → CSR)

| Dim | Vectors | Graph | CSR (pruned) | Savings |
|-----|---------|-------|-------------|---------|
| 128 | 1,000 | 1.06 MB | 0.42 MB | 60.4% |
| 384 | 5,000 | 10.35 MB | 2.13 MB | 79.5% |
| 768 | 5,000 | 17.68 MB | 2.13 MB | **88.0%** |

## Build Throughput

| Operation | N=100 | N=1,000 | N=10,000 |
|-----------|-------|---------|----------|
| HNSW Insert | 13ms | 787ms | 34.8s |
| CSR Convert | 102µs | 1.37ms | 18.0ms |
| End-to-End Pipeline | 28ms | 856ms | — |

## BM25 Performance

| Operation | Corpus | Latency |
|-----------|--------|---------|
| Score (1K docs) | 1,000 | 787µs |
| TopK (5K docs) | 5,000 | 59ms |

## HNSW vs Brute Force (5K vectors, 128-dim)

| Method | Latency/query | Recall@10 |
|--------|---------------|-----------|
| Brute Force | 43.8ms | 100% |
| HNSW (ef=128) | 41.5ms | 98.8% |
| HNSW (ef=256) | — | 99.9% |

> At 5K vectors, HNSW's advantage is modest (1.1x). The sub-linear advantage grows significantly with larger datasets (50K+).

## Retrieval Quality Report (5K vectors, 128-dim, 30 clusters, 500 queries)

Full 5-stage quality evaluation via `go test -v -run TestRecallReport ./tests/benchmarks/`.

**Stage 1: Index Build**
| Mode | Build Time | Pruned Embeddings |
|------|-----------|-------------------|
| Full | 11.0s | — |
| Compact | 11.5s | 4999/5000 (100%) |

**Stage 2: Recall@K (Full Graph vs Flat)**
| K | Recall@K |
|---|---------|
| 1 | 96.60% |
| 3 | 96.73% |
| 5 | 96.80% |
| 10 | 96.72% |
| 20 | 96.66% |
| 50 | 96.50% |

**Stage 3: EfSearch Complexity Sweep**
| efSearch | Recall@10 | Latency/query |
|---------|-----------|---------------|
| 16 | 91.88% | 329µs |
| 32 | **95.98%** | 475µs |
| 64 | 96.50% | 688µs |
| 128 | 96.72% | 1.25ms |
| 256 | 97.76% | 2.10ms |
| 512 | 97.90% | 4.33ms |

> **Minimum efSearch for ≥95% recall:** 32

**Stage 4: Compact (Recompute) vs Full Index**
| Mode | Recall@10 | Latency/query | Size |
|------|-----------|---------------|------|
| Full | 96.72% | 521µs | 4.54 MB |
| Compact + Recompute | 96.72% | 576µs | 2.06 MB |

> **Storage saving: 54.7%** with only 1.1x latency overhead and **zero recall loss**.

**Stage 5: M Parameter Trade-offs**
| M | Recall@10 | Build Time | Search/query |
|---|-----------|-----------|-------------|
| 8 | 75.88% | 1.3s | 222µs |
| 16 | 86.14% | 2.4s | 279µs |
| 32 | 96.72% | 5.6s | 464µs |
| 48 | **98.80%** | 8.4s | 595µs |
| 64 | 98.62% | 10.0s | 772µs |

> **M=32** is the default — best balance of recall (96.7%) vs build/search cost. M=48 peaks at 98.8% recall.
