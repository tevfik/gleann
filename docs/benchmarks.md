# Benchmarks & Performance Reports

All benchmarks on Intel i9-13900H (20 threads), Go 1.25, Linux.

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
| Tables | ~65% | >75% | Marker extracts structure; VLM fallback for complex merges |
| Charts | ~35% | >50% | Text-only baseline; VLM plugin improves to ~55% |
| Content Faithfulness | ~82% | >85% | Marker's text extraction is reliable for standard docs |
| Semantic Formatting | ~45% | >60% | Marker strips most formatting; plugin enrichment helps |
| Visual Grounding | N/A | — | Text-only pipeline |

> **To run ParseBench against gleann:** See [ParseBench GitHub](https://github.com/run-llama/ParseBench) for evaluation code.
> Dataset: [HuggingFace](https://huggingface.co/datasets/llamaindex/ParseBench) | Paper: [arXiv:2604.08538](https://arxiv.org/abs/2604.08538)

---

## FAISS CGo vs Pure Go HNSW

FAISS backend uses CGo + libfaiss with SIMD (AVX2/SSE) acceleration. Requires `-tags faiss` build tag.
Run with: `go test -v -tags faiss -run TestFAISSvsPureGo -timeout 300s ./internal/backend/faiss/`

### Build Speed

| Config | FAISS (CGo) | Pure Go | Speedup |
|--------|------------|---------|---------|
| 1K × 64d | 4ms | 91ms | **22.8x** |
| 1K × 128d | 6ms | 165ms | **27.5x** |
| 5K × 128d | 33ms | 1.48s | **44.8x** |
| 5K × 384d | 72ms | 3.2s | **44.4x** |

### Search Speed

| Config | FAISS (CGo) | Pure Go | Speedup |
|--------|------------|---------|---------|
| 1K × 64d | 190µs | 465µs | **2.4x** |
| 1K × 128d | 335µs | 780µs | **2.3x** |
| 5K × 128d | 1.2ms | 3.8ms | **3.2x** |
| 5K × 384d | 3.9ms | 35.2ms | **9.0x** |

> FAISS advantage grows with dimensionality due to SIMD-vectorized distance computation.

### Recall@10 (vs Brute Force, efSearch=128, M=32)

| Config | FAISS | Pure Go |
|--------|-------|---------|
| 1K × 64d | 99.2% | 98.8% |
| 5K × 128d | 98.8% | 96.7% |
| 5K × 384d | 98.4% | 96.5% |

### Trade-offs

| Feature | FAISS CGo | Hybrid (`faiss-hybrid`) | Pure Go HNSW |
|---------|-----------|-------------------------|-------------|
| Build speed | **45x** faster | **13-24x** faster | Baseline |
| Embedding pruning | ❌ | ✅ (87%+ savings) | ✅ (87%+ savings) |
| SearchWithRecompute | ❌ | ✅ | ✅ |
| Mmap search | ❌ | ✅ | ✅ |
| Vector removal | ❌ (rebuild) | ❌ (rebuild) | ✅ (incremental) |
| Batch search | ✅ (native) | ❌ | ❌ |
| Cross-compile | ❌ (needs libfaiss) | ❌ (needs libfaiss) | ✅ |
| Best for | High-throughput server | Fast build + compact storage | Edge/single-binary |

### Hybrid Backend (`faiss-hybrid`)

Uses FAISS SIMD for index construction, then extracts the graph topology and converts to CSR format with embedding pruning. Search uses the pure-Go HNSW backend with full recompute support.

Run with: `go test -v -tags faiss -run TestHybridComparison ./internal/backend/faiss/`

| Config | FAISS Build | Hybrid Build | Go Build | Hybrid vs Go | Hybrid Size | FAISS Size |
|--------|------------|-------------|----------|-------------|-------------|------------|
| 1K × 64d | 11ms | 28ms | 645ms | **23x** faster | 610 KB | 515 KB |
| 1K × 128d | 31ms | 44ms | 808ms | **18x** faster | 621 KB | 765 KB |
| 5K × 128d | 119ms | 528ms | 10.4s | **20x** faster | 2.3 MB | 3.3 MB |

Recall: Hybrid achieves **100% recall@10** (vs 99.9% for FAISS). Overlap between hybrid and FAISS results: 99.9%.

---

## Context Efficiency Comparison

How gleann compares to other context-optimization tools:

| Tool | Approach | Token Savings | Method |
|------|----------|--------------|--------|
| context-mode | Sandbox tools + FTS5 session continuity | 98% | Raw data never enters context |
| token-savior | Symbol-level navigation + persistent memory | 97% | Pointer-based, not file-based |
| code-review-graph | AST graph + blast radius | 8.2x reduction | Only affected code surfaces |
| **gleann** | Semantic search + RAG + graph | ~90% | Hybrid vector + graph: only relevant chunks enter context |

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

| Feature | Method | 1K nodes | 5K nodes | 10K nodes | Notes |
|---------|--------|----------|----------|-----------|-------|
| PageRank | Power iteration (30 iter, d=0.85) | 10 ms | 48 ms | 242 ms | Linear scaling |
| Community detection | Louvain | 446 ms | 20 s | 77 s | O(n²) — optimize for >5K |
| Risk scoring | Centrality + Coupling + Blast | 828 ms | 21 s | — | BFS blast radius is O(n²) |
| Repo map generation | PageRank + TopK + grouping | 10 ms | 64 ms | 219 ms | Token-budgeted output |
| Blast radius (BFS) | 3-hop BFS with PR weighting | <2 ms / query | <5 ms | <10 ms | Per-symbol impact analysis |

> **Scale guidance:** PageRank and RepoMap scale linearly to 10K+. Louvain and RiskScoring hit O(n²) walls at ~5K nodes — consider sampling or incremental algorithms for larger graphs.

---

## Multi-Index Search

| Indexes | Query Latency | Merge Strategy |
|---------|---------------|----------------|
| 2 indexes | ~2x single | Score-weighted interleave |
| 5 indexes | ~5x single | Parallel search + merge |

Multi-index queries (`gleann ask idx1,idx2 "question"`) search each index independently and merge results by relevance score.
