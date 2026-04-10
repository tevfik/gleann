git s# Architecture & Design

## Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│          TUI / CLI / REST API / MCP Server / A2A Protocol            │
├──────────────────────────────────────────────────────────────────────┤
│  Middleware Layer                                                     │
│  ├── Rate Limiter  (per-IP token bucket, 429)                        │
│  ├── Timeout       (per-path context deadline, 504)                  │
│  └── CORS / Logging                                                  │
├───────────────────┬──────────────────────────────────────────────────┤
│  LeannBuilder     │  Searcher Interface                              │
│  (build index)    │  ├── LeannSearcher (single index)                │
│  .gleannignore    │  └── MultiSearcher (fan-out merge)               │
├───────────────────┤                                                  │
│  LeannChat        │  Conversations / Roles / Format                  │
│  (LLM Q&A)       │  Stdin · Pipe · Raw · Quiet                       │
│  + retry logic    │  ↑ memory context injected as system message     │
├───────────────────┴──────────────────────────────────────────────────┤
│  Retry Layer  (pkg/retry — exponential backoff for transient errors) │
├──────────────────────────────────────────────────────────────────────┤
│  A2A Protocol Layer  (internal/a2a — Agent-to-Agent communication)   │
│  ├── Agent Card   (/.well-known/agent-card.json)                     │
│  ├── Skills       (semantic-search, ask-rag, code-analysis, memory)  │
│  ├── Skill Router (keyword match → scoring-based fallback)           │
│  └── Task Store   (in-memory, bounded at 1000)                       │
├──────────────────────────────────────────────────────────────────────┤
│  Unified Memory API  (internal/server/unified_memory_handler.go)     │
│  ├── Ingest       (facts → blocks, relationships → graph)            │
│  ├── Recall       (parallel: blocks + graph + vector search)         │
│  └── Project field (syntactic sugar for scope + index)               │
├──────────────────────────────────────────────────────────────────────┤
│              Backend Registry                                        │
├──────────────────┬───────────────────────────────────────────────────┤
│  Pure Go         │  FAISS (optional, CGo)                            │
│  HNSW Graph      │  HNSW via libfaiss C API                          │
│  CSR Format      │  AVX2/SIMD, OpenMP                                │
├──────────────────┴───────────────────────────────────────────────────┤
│  Passage Manager  │  BM25 Scorer  │  Chunking                        │
│  (Bbolt KV)       │  (Okapi BM25) │  (sentence/AST/markdown)         │
├───────────────────┴───────────────┴──────────────────────────────────┤
│  Embedding Server  (goroutine pool + retry)                          │
├──────────────────────────────────────────────────────────────────────┤
│  KuzuDB Graph Layer                                                  │
│  ├── Code Graph  (CodeFile, Symbol, CALLS, IMPLEMENTS …)             │
│  ├── Document Graph  (Folder, Document, Heading, Chunk …)            │
│  └── Memory Engine  (Entity, RELATES_TO — external agent memory)     │
├──────────────────────────────────────────────────────────────────────┤
│  Long-term Memory Layer  (pkg/memory)                                │
│  ├── Short-term  in-process  session notes → promoted on exit        │
│  ├── Medium-term BBolt      conversation summaries, daily digests    │
│  ├── Long-term   BBolt      permanent facts, preferences, knowledge  │
│  └── Maintenance Scheduler  (background goroutine, 24h cycle)        │
├──────────────────────────────────────────────────────────────────────┤
│  Background Task Manager  (internal/background)                      │
│  ├── Worker Pool   (bounded, default 2 workers)                      │
│  ├── Task Lifecycle (queued → running → completed/failed)            │
│  └── Progress Tracking  (real-time 0.0–1.0 + messages)               │
├──────────────────────────────────────────────────────────────────────┤
│  Auto-Bootstrap  (internal/autosetup)                                │
│  └── Detects Ollama, picks best models, creates config automatically │
├──────────────────────────────────────────────────────────────────────┤
│  Multimodal Layer  (internal/multimodal)                             │
│  ├── Media Detection  (image/audio/video classification)             │
│  ├── Model Capability Detection  (Ollama + heuristics)               │
│  └── Processor  (base64 → Ollama /api/chat → text description)      │
└──────────────────────────────────────────────────────────────────────┘
```

### Three Intelligence Pillars

gleann combines three capabilities into one coherent system.  Each pillar feeds
the others automatically at query time:

```
┌────────────────────────────────────────────────────────┐
│  1. Document & Code Search  (index + search + ask)     │
│     Build semantic vector indexes from any docs/code.  │
│     Ask questions → RAG-powered answers from your data.│
├────────────────────────────────────────────────────────┤
│  2. Code Intelligence  (graph + treesitter)            │
│     AST-level call graphs stored in KuzuDB.            │
│     Trace dependencies, callers, blast-radius.         │
│     Search results enriched with structural context.   │
├────────────────────────────────────────────────────────┤
│  3. Long-term Memory  (pkg/memory → BBolt)             │
│     Persistent facts, preferences, conversation sums.  │
│     Injected as a system message into EVERY LLM query. │
│     Human: /remember  •  CLI: gleann memory remember   │
└────────────────────────────────────────────────────────┘
         All three active simultaneously during ask/chat
```

### Two Memory Subsystems

gleann has two distinct memory subsystems that serve different audiences:

| Subsystem | Storage | Interface | Audience |
|-----------|---------|-----------|----------|
| **Long-term Memory** (`pkg/memory`) | BBolt | `gleann memory *` · `/remember` in chat | Human users & agents via CLI |
| **Memory Engine** (`internal/graph/kuzu`) | KuzuDB | MCP tools · REST `/api/memory/{name}/*` | External AI agents (programmatic API) |

The Long-term Memory layer stores natural-language facts and conversation
summaries that are compiled into `<memory_context>` XML and automatically
prepended to every LLM query.

The Memory Engine is a low-level graph store for structured entity-relationship
knowledge that external agents inject via MCP or REST (no CLI interaction).



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

### Memory Engine: Generic AI Agent Memory

gleann v2 introduces a **Memory Engine** that transforms the system from a
closed RAG box into a generic knowledge graph backend for autonomous AI agents.

```
External Agent (e.g. Yaver, Claude)
        │
        │  MCP tools:                    HTTP endpoints:
        │  inject_knowledge_graph   ←→   POST /api/memory/{name}/inject
        │  delete_graph_entity      ←→   DELETE /api/memory/{name}/nodes/{id}
        │  traverse_knowledge_graph ←→   POST /api/memory/{name}/traverse
        │
        ▼
  KuzuDB Entity / RELATES_TO schema
  ├─ Node: {id, type, content, attributes}
  └─ Edge: {from, to, relation_type, weight, attributes}
        │
        ▼ (when content is non-empty)
  HNSW + BM25 vector index (via VectorSyncer)
```

**Key design properties:**

| Property | Implementation |
|----------|---------------|
| **Generic schema** | Single `Entity` node table + single `RELATES_TO` rel table |
| **Idempotent writes** | Cypher `MERGE` — safe to re-inject the same payload |
| **Atomic batches** | `BEGIN / COMMIT` transaction per `InjectEntities` call |
| **Vector sync** | Optional `VectorSyncer` interface — graph write commits first, then vector store is updated |
| **Cascade deletes** | `DETACH DELETE` removes a node and all its incident edges atomically |
| **Traversal** | Variable-length Cypher path `[:RELATES_TO*0..N]` with depth cap at 10 |
| **Co-existence** | Entity/RELATES_TO live in a separate DB (`<name>_memory`) from the AST/document graph |

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

### bbolt Passage Storage

- Key-value store for passages and metadata
- Efficient random access and append-only operations
- Single file database for easy deployment

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
| Memory Engine (AI agent memory) | — | ✅ (Entity/RELATES_TO KuzuDB) |
| Long-term Memory (BBolt blocks) | — | ✅ (short/medium/long tiers, auto-injection) |
| Rate Limiting | — | ✅ (per-IP token bucket, 429) |
| Request Timeouts | — | ✅ (per-path context deadline, 504) |
| Retry Logic | — | ✅ (exponential backoff for LLM/embedding calls) |
| Batch Query (MCP) | — | ✅ (`gleann_batch_ask` — 10 concurrent questions) |
| Background Maintenance | — | ✅ (auto-promote blocks, prune expired) |
| Sleep-Time Compute | — | ✅ (Letta-inspired background reflection on conversations) |
| A2A Skill Router | — | ✅ (keyword + scoring-based fallback routing) |
| Temporal Graph Edges | — | ✅ (auto `created_at`/`updated_at` on edges) |
| Project-scoped Memory | — | ✅ (`project` field → scope + index shorthand) |
| Memory Block Limits | — | ✅ (per-block char limit with auto-truncation) |
| Scoped Memory Blocks | — | ✅ (conversation/session isolation) |
| OpenAI-Compatible Proxy | — | ✅ (`/v1/chat/completions`) |
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
