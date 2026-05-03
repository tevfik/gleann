# Retrieval-Augmented Generation (RAG)

Retrieval-Augmented Generation combines a retrieval system with a generative
language model. Instead of relying solely on parametric knowledge, the model
fetches relevant passages from an external corpus and conditions its output
on that retrieved context.

## Pipeline Stages

1. **Ingestion** — Documents are chunked and converted into vector embeddings.
2. **Indexing** — Embeddings are stored in a vector database (FAISS, HNSW, etc.).
3. **Retrieval** — At query time, the question is embedded and nearest neighbors
   are returned from the index.
4. **Augmentation** — Retrieved passages are inserted into the LLM prompt.
5. **Generation** — The LLM produces a grounded answer citing the sources.

## Why RAG Matters
- Reduces hallucinations by grounding answers in real documents.
- Allows knowledge updates without retraining the model.
- Provides citations and provenance for every answer.
- Works with private, proprietary, or recent data the LLM has never seen.

## Hybrid Retrieval
Modern RAG systems combine dense vector search with sparse lexical search (BM25)
and rerank the top candidates with a cross-encoder for higher precision.

## Common Pitfalls
- Poor chunking destroys context boundaries.
- Embedding model mismatch between index and query time produces noise.
- Missing reranking lets near-duplicate passages dominate the context window.
