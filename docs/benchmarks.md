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

---

## Document Parsing Quality (ParseBench-aligned)

Reference benchmark for evaluating gleann's document parsing pipeline quality. Based on [ParseBench](https://www.llamaindex.ai/blog/parsebench) — ~2,000 human-verified enterprise document pages with 167K+ test rules.

**ParseBench evaluates 5 capability dimensions:**

| Dimension | What it measures | Why it matters for RAG |
|-----------|-----------------|----------------------|
| Tables | Merged cells, hierarchical headers, multi-page tables | Agents read specific cells for decisions |
| Charts | Structured data extraction from visualizations | Chart data must be queryable, not raw OCR |
| Content Faithfulness | Omissions, hallucinations, reading order | Missing text = wrong answers |
| Semantic Formatting | Strikethrough, superscript, bold meaning | ~~$49.99~~ $39.99 vs "$49.99 $39.99" |
| Visual Grounding | Element-to-page localization | Auditability in regulated industries |

**Industry Leaderboard (Top 5, April 2025):**

| Provider | Overall | Tables | Charts | Content | Formatting | Grounding | ¢/Page |
|----------|---------|--------|--------|---------|------------|-----------|--------|
| LlamaParse Agentic | **84.9** | 90.7 | 78.1 | 89.7 | 85.2 | 80.6 | 1.25¢ |
| Gemini 3 Flash (High) | 75.1 | 91.5 | 64.8 | 90.9 | 68.3 | 59.8 | 2.41¢ |
| Reducto (Agentic) | 73.0 | 80.4 | 73.4 | 86.4 | 57.6 | 67.1 | 4.76¢ |
| LlamaParse Cost Effective | 71.9 | 73.2 | 66.7 | 88.0 | 73.0 | 58.6 | 0.38¢ |
| Gemini 3 Flash (Minimal) | 71.0 | 89.9 | 64.8 | 86.2 | 58.4 | 56.0 | 0.65¢ |

**gleann Pipeline Targets:**

| Dimension | gleann-plugin-marker | Target | Notes |
|-----------|---------------------|--------|-------|
| Tables | TBD | >75% | Marker + VLM hybrid |
| Charts | TBD | >50% | VLM-native (gemma4/qwen3-vl) |
| Content Faithfulness | TBD | >85% | Text extraction accuracy |
| Semantic Formatting | TBD | >60% | Marker preserves some formatting |
| Visual Grounding | N/A | — | Text-only pipeline |

> **To run ParseBench against gleann:** See [ParseBench GitHub](https://github.com/run-llama/ParseBench) for evaluation code.
> Dataset: [HuggingFace](https://huggingface.co/datasets/llamaindex/ParseBench) | Paper: [arXiv:2604.08538](https://arxiv.org/abs/2604.08538)

---

## Context Efficiency Comparison

How gleann compares to other context-optimization tools:

| Tool | Approach | Token Savings | Method |
|------|----------|--------------|--------|
| context-mode | Sandbox tools + FTS5 session continuity | 98% | Raw data never enters context |
| token-savior | Symbol-level navigation + persistent memory | 97% | Pointer-based, not file-based |
| code-review-graph | AST graph + blast radius | 8.2x reduction | Only affected code surfaces |
| **gleann** | Semantic search + RAG + graph | TBD | Hybrid vector + graph retrieval |

> gleann's advantage: all-in-one local binary with zero cloud dependency.

---

## BM25 Stress & Scale Benchmarks

Validated via `go test ./tests/benchmarks/ -run "TestRecall|TestStress"`:

| Operation | Corpus Size | Latency | Notes |
|-----------|-------------|---------|-------|
| BM25Adapter index | 50,000 passages | 390 ms | Full tokenize + index build |
| PassageManager disk | 20,000 passages | 55.6 MB | ~2.8x raw text overhead |
| Hybrid search (vector+BM25) | 5,000 | <100 ms | End-to-end with score merging |

**Edge case coverage (all pass):**
- Empty corpus searches
- Single-document corpus
- Common stop words only queries
- Unicode / special character handling
- Very long passage indexing (10K+ tokens)

---

## Graph Intelligence Benchmarks

| Feature | Method | Performance | Notes |
|---------|--------|-------------|-------|
| PageRank | Power iteration (30 iter, d=0.85) | <10 ms / 5K nodes | Converges in ~20 iterations |
| Community detection | Louvain | <50 ms / 5K nodes | Q > 0.4 = well-modularized |
| Risk scoring | Centrality + Coupling + Blast | <15 ms / 5K nodes | Composite [0,1] score |
| Repo map generation | PageRank + TopK + grouping | <5 ms / 5K nodes | Token-budgeted output |
| Blast radius (BFS) | 3-hop BFS with PR weighting | <2 ms / query | Per-symbol impact analysis |

---

## Multi-Index Search

| Indexes | Query Latency | Merge Strategy |
|---------|---------------|----------------|
| 2 indexes | ~2x single | Score-weighted interleave |
| 5 indexes | ~5x single | Parallel search + merge |

Multi-index queries (`gleann ask idx1,idx2 "question"`) search each index independently and merge results by relevance score.
