# Architecture & Design

## Overview

```
┌──────────────────────────────────────────────────────┐
│          TUI / CLI / REST API / MCP Server           │
├────────────────┬─────────────────────────────────────┤
│  LeannBuilder  │  Searcher Interface                 │
│  (build index) │  ├── LeannSearcher (single index)   │
│  .gleannignore │  └── MultiSearcher (fan-out merge)  │
├────────────────┤                                     │
│  LeannChat     │  Conversations / Roles / Format     │
│  (LLM Q&A)    │  Stdin · Pipe · Raw · Quiet          │
├────────────────┴─────────────────────────────────────┤
│              Backend Registry                        │
├──────────────┬───────────────────────────────────────┤
│  Pure Go     │  FAISS (optional, CGo)                │
│  HNSW Graph  │  HNSW via libfaiss C API              │
│  CSR Format  │  AVX2/SIMD, OpenMP                    │
├──────────────┴───────────────────────────────────────┤
│  Passage Manager  │  BM25 Scorer  │  Chunking        │
│  (JSONL + idx)    │  (Okapi BM25) │  (sentence/code) │
├───────────────────┴───────────────┴──────────────────┤
│  Embedding Server  (goroutine pool)                  │
└──────────────────────────────────────────────────────┘
```

### Storage Optimization: Selective Recomputation

Instead of storing all embedding vectors, gleann stores only the HNSW graph structure (CSR format) and recomputes embeddings on-demand during search. This is the core LEANN innovation:

1. **Build time**: Insert all vectors into HNSW graph, convert to CSR, prune embeddings
2. **Search time**: Traverse graph structure, recompute embeddings only for visited nodes
3. **Result**: Up to 87% storage reduction with 98.8% recall

## Design Decisions

### Pure Go HNSW (default, no FAISS/CGo)

- Zero external C dependencies → easy cross-compilation
- Single binary deployment (~2.5 MB)
- 98.8% recall@10 matches production HNSW quality

### Optional FAISS Backend (CGo)

- 15-34x faster builds, 3-28x faster search via AVX2 SIMD + OpenMP
- Same `BackendFactory` interface — just change backend name
- Enabled via `-tags faiss` build flag — excluded by default

### Hierarchical GraphRAG & Extractive Summarization

- Explicitly models the structural layout of files, folders, and markdown headings in KuzuDB alongside AST code symbols.
- Zero-config Extractive Summarizer: High-density sentences are extracted algorithmically during build time, completely bypassing slow/costly LLMs, enabling zero-latency "Smart Summaries".

### Goroutine Embedding Server (no ZMQ)

- Python LEANN uses ZMQ for embedding server communication
- gleann-go uses goroutine workers + channels (idiomatic Go)
- No external process needed — embeddings computed in-process

### CSR Graph Format

- Binary format with magic `0x474C454E` ("GLEN"), version 1
- Per-level CSR adjacency + per-node level tracking
- Selective embedding storage with entry-point preservation
- O(1) node lookup by position

### JSONL + Offset Index

- Passages stored as newline-delimited JSON
- Binary offset index (`.passages.idx`) for O(1) random access
- Append-only for incremental indexing

## Comparison with Python LEANN

### Feature Parity

| Feature | Python LEANN | gleann-go |
|---------|:---:|:---:|
| HNSW Vector Index | ✅ FAISS | ✅ Pure Go + optional FAISS |
| CSR / Graph Pruning | ✅ | ✅ |
| Embedding Providers | Ollama, OpenAI, Gemini, MLX, SentenceTransformers | Ollama, OpenAI, Gemini |
| Prompt Templates | ✅ | ✅ |
| Token Limit Detection | ✅ | ✅ |
| Metadata Filtering | ✅ | ✅ |
| LLM Chat (`ask`) | Ollama, OpenAI, Anthropic, Gemini, HuggingFace | Ollama, OpenAI, Anthropic |
| Multi-Index Chat | — | ✅ (comma-separated indexes) |
| Conversations | — | ✅ (persist, continue, manage) |
| Named Roles | — | ✅ (built-in + custom in config) |
| Stdin/Pipe Support | — | ✅ (auto-raw when piped) |
| `.gleannignore` | — | ✅ (gitignore-style exclusions) |
| ReAct Agent | ✅ | ✅ |
| AST-aware Chunking | tree-sitter | go/ast + optional tree-sitter |
| Hierarchical GraphRAG | — | ✅ (Folders, Documents, Headings) |
| Extractive Summarizer | — | ✅ (Build-time NLP algorithm) |
| MCP Server | ✅ | ✅ (built-in) |
| File Sync (incremental) | ✅ | ✅ |
| Hybrid Search (BM25) | — | ✅ |
| REST API Server | — | ✅ |
| DiskANN Backend | ✅ | — |
| IVF Backend | ✅ | — |
| Local Embeddings (torch) | ✅ | — |
| Interactive TUI | ✅ | ✅ (Bubble Tea) |

### Architecture Comparison

| Dimension | Python LEANN | gleann-go |
|-----------|-------------|-----------|
| Language | Python 3.10+ | Go 1.24+ |
| Deployment | `pip install` + system deps | Single binary (~2.5 MB) |
| Embedding Server | ZMQ (external process) | Goroutine pool (in-process) |
| Concurrency | asyncio / threading | Goroutines + channels |
| AST Parser | tree-sitter (C bindings) | go/ast + optional tree-sitter |
| Storage Format | Custom binary | CSR binary (`GLEN` magic) |
| Backends | 3 (HNSW, DiskANN, IVF) | 2 (Pure Go HNSW, FAISS CGo) |
| CLI Framework | Click | flag (stdlib) + Bubble Tea TUI |
| External Dependencies | ~40 PyPI packages | 0 (pure Go default) |
