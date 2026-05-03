# Vector Search Fundamentals

Vector search retrieves items by semantic similarity rather than exact keyword
match. Each item is represented as a high-dimensional embedding, and similarity
is measured by cosine distance, dot product, or Euclidean distance.

## Embedding Models

An embedding model maps text, code, or media into a fixed-length numeric vector
where semantically similar inputs land near each other in the vector space.
Popular choices include `bge-m3`, `text-embedding-3-large`, and `nomic-embed-text`.

## Approximate Nearest Neighbor (ANN)

Exhaustive search is O(N) per query. ANN structures trade a small amount of
recall for sub-linear query time:

- **HNSW** — Hierarchical Navigable Small World graphs. Excellent recall/speed
  trade-off; the de-facto standard for modern vector databases.
- **IVF** — Inverted file index that partitions vectors into Voronoi cells.
- **PQ** — Product Quantization compresses vectors for memory efficiency.

## Reranking

After ANN retrieval returns the top-K candidates, a cross-encoder reranker
re-scores each (query, candidate) pair jointly. This second pass is much more
expensive per item but operates only on a few candidates, yielding a large
precision boost at minimal latency cost.

## When To Use What
- Pure keyword? BM25 or full-text search.
- Pure semantic? Dense vectors with HNSW.
- Production-grade? Hybrid (dense + sparse) with cross-encoder reranking.
