# gleann-go

[![CI](https://github.com/tevfik/gleann/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/tevfik/gleann/actions/workflows/ci.yml)
[![Release](https://github.com/tevfik/gleann/actions/workflows/release.yml/badge.svg?event=push)](https://github.com/tevfik/gleann/actions/workflows/release.yml)

Pure Go implementation of [LEANN](https://github.com/yichuan-w/LEANN) — a lightweight vector database achieving **up to 87% storage reduction** through graph-based selective recomputation.

gleann-go provides semantic search across various data sources (documents, code, emails) on a single laptop without cloud dependencies or CGo.

## Key Features

- **Pure Go** — No CGo, no FAISS, no external dependencies (default)
- **Optional FAISS Backend** — CGo-based FAISS integration for 15-34x faster builds and 3-28x faster search
- **HNSW Index** — Hierarchical Navigable Small World graph with 98.8% recall@10
- **CSR Compact Format** — Compressed Sparse Row storage with selective embedding pruning
- **Ollama Entegrasyonu:** `bge-m3`, `nomic-embed-text` gibi modellerle yerel embedding.
- **Güçlü HNSW Arama:** Yüksek performanslı bellek içi (in-memory) Vektör Arama Motoru.
- **Zero-Copy MMAP:** `.index` Graph dosyalarının devasa bellek kullanımını engellemek için `mmap` üzerinden 0 Garbage-Collection (GC) ile %100 Go native index okuması.
- **Dosya İzleme (Watch):** `fsnotify` ve `SQLite` tabanlı Vault takipçisi ile `Create/Rename/Modify/Delete` klasör olaylarını anında algılama.
- **Saf Go (Pure-Go) Mimarisi:** CGO bağımlılığı kullanmadan tek komut ile Linux, macOS, ve Windows üzerinde AMD64/ARM64 derlenebilme.
- **Hibrit Reranking Arama** ve **Anında Sohbet (CLI Chat).** — Build, search, list, remove, serve, mcp from the command line
- **Setup Wizard** — `gleann setup` for guided configuration of all providers and features
- **Embedding Providers** — Ollama, OpenAI, and Gemini APIs
- **LLM Chat & ReAct** — Search + LLM answer with multi-turn reasoning
- **Two-Stage Reranker** — Cross-encoder reranking pipeline for higher accuracy
- **Metadata Filtering** — 12 operators (eq, gt, contains, regex, etc.) with AND/OR logic
- **AST-aware Chunking** — Go native AST (default) + optional tree-sitter for 8 languages via `-tags treesitter`
- **MCP Server** — Built-in JSON-RPC 2.0 stdio for Claude Code / VS Code integration (`gleann mcp`)
- **File Sync** — Incremental re-indexing with SHA-256 change detection

## Architecture

```
┌──────────────────────────────────────────────────────┐
│          TUI / CLI / REST API / MCP Server            │
├────────────────┬─────────────────────────────────────┤
│  LeannBuilder  │         LeannSearcher               │
│  (build index) │  (search + hybrid BM25 + reranker)  │
├────────────────┴─────────────────────────────────────┤
│              Backend Registry                         │
├──────────────┬───────────────────────────────────────┤
│  Pure Go     │  FAISS (optional, CGo)                │
│  HNSW Graph  │  HNSW via libfaiss C API              │
│  CSR Format  │  AVX2/SIMD, OpenMP                    │
├──────────────┴───────────────────────────────────────┤
│  Passage Manager  │  BM25 Scorer  │  Chunking        │
│  (JSONL + idx)    │  (Okapi BM25) │  (sentence/code) │
├───────────────────┴───────────────┴──────────────────┤
│  Embedding Server  (goroutine pool)                   │
└──────────────────────────────────────────────────────┘
```

### Storage Optimization: Selective Recomputation

Instead of storing all embedding vectors, gleann-go stores only the HNSW graph structure (CSR format) and recomputes embeddings on-demand during search. This is the core LEANN innovation:

1. **Build time**: Insert all vectors into HNSW graph, convert to CSR, prune embeddings
2. **Search time**: Traverse graph structure, recompute embeddings only for visited nodes
3. **Result**: Up to 87% storage reduction with 98.8% recall

## Installation

```bash
# From source
git clone https://github.com/tevfik/gleann.git
cd gleann

# Build CLI (includes TUI, REST server, MCP server)
go build -o gleann ./cmd/gleann/

# Run setup wizard
./gleann setup

# Run tests
go test ./...
```

Requires Go 1.24+.

### Install to PATH

The setup wizard (`gleann setup`) can install the binary to `~/.local/bin` or `/usr/local/bin` with shell completions (bash, zsh, fish). It can also configure MCP for Claude Code and Claude Desktop automatically.

## FAISS Backend (Optional)

gleann-go includes an optional FAISS backend via CGo for significantly faster HNSW operations. The FAISS backend uses the same `BackendFactory` interface — just change `config.Backend = "faiss"`.

### Prerequisites

```bash
# Ubuntu/Debian
sudo apt-get install cmake g++ libopenblas-dev libomp-dev swig

# Build FAISS from source with C API
git clone --branch v1.13.2 --depth 1 https://github.com/facebookresearch/faiss.git /tmp/faiss-src
cd /tmp/faiss-src && mkdir build && cd build
cmake .. -DFAISS_ENABLE_C_API=ON -DFAISS_ENABLE_GPU=OFF \
         -DBUILD_TESTING=OFF -DFAISS_ENABLE_PYTHON=OFF \
         -DCMAKE_BUILD_TYPE=Release
make -j$(nproc) faiss faiss_c

# Install
sudo cp -r c_api/libfaiss_c.a faiss/libfaiss.a /usr/local/lib/
sudo mkdir -p /usr/local/include/faiss/c_api/impl
sudo cp ../c_api/*.h /usr/local/include/faiss/c_api/
sudo cp ../c_api/impl/*.h /usr/local/include/faiss/c_api/impl/
```

### Building with FAISS

```bash
# Build with FAISS support
go build -tags faiss -o gleann ./cmd/gleann/

# Run tests including FAISS
go test -tags faiss ./internal/backend/faiss/ -v

# Run FAISS vs Pure Go comparison
go test -tags faiss -run TestFAISSvsPureGo -timeout 300s ./internal/backend/faiss/ -v

# Standard benchmarks
go test -tags faiss -bench=BenchmarkFAISS -benchmem ./internal/backend/faiss/
```

Without `-tags faiss`, the FAISS backend is excluded and gleann builds as pure Go with zero C dependencies.

### FAISS vs Pure Go Performance

All benchmarks on Intel i9-13900H (20 threads), Linux. Both backends use M=32, efSearch=128.

| Config | Metric | FAISS (CGo) | Pure Go | Speedup |
|--------|--------|-------------|---------|---------|
| **1K×64d** | Build | 17ms | 314ms | **18.4x** |
| | Search/query | 292µs | 284µs | ~1x |
| | QPS | 3,424 | 3,522 | |
| | Recall@10 | 100% | 100% | |
| **1K×128d** | Build | 20ms | 332ms | **16.9x** |
| | Search/query | 86µs | 356µs | **4.1x** |
| | QPS | 11,590 | 2,812 | |
| | Recall@10 | 100% | 100% | |
| **5K×128d** | Build | 107ms | 4.9s | **45.3x** |
| | Search/query | 227µs | 1.5ms | **6.5x** |
| | QPS | 4,400 | 678 | |
| | Recall@10 | 98.8% | 99.0% | |
| **5K×384d** | Build | 436ms | 9.4s | **21.5x** |
| | Search/query | 279µs | 2.5ms | **9.0x** |
| | QPS | 3,588 | 398 | |
| | Recall@10 | 96.8% | 98.2% | |

### When to Use Each Backend

| | Pure Go (`hnsw`) | FAISS (`faiss`) |
|---|---|---|
| **Best for** | Simplicity, portability | Maximum throughput |
| **Dependencies** | None | libfaiss, OpenBLAS, libomp |
| **Cross-compile** | Yes | No (needs C toolchain) |
| **Binary size** | ~2.5 MB | ~15 MB |
| **Build speed** | Instant | Requires FAISS from source |
| **SIMD** | No | AVX2/SSE (auto-detected) |
| **Recall@10** | 98.8% (ef=128) | Tunable via efSearch |
| **Vector removal** | Supported | Not supported (rebuild needed) |

## Tree-sitter Backend (Optional)

gleann-go includes an optional tree-sitter backend for precise AST-aware code chunking across all supported languages. Without this tag, non-Go languages use regex-based boundary detection.

### Default (Regex) vs Tree-sitter

| | Default (regex) | Tree-sitter (`-tags treesitter`) |
|---|---|---|
| **Go** | `go/ast` native (full AST) | `go/ast` native (unchanged) |
| **Python** | Regex: top-level + 1 indent | Full AST: nested classes, decorators |
| **JS/TS** | Regex: function/class/const | Full AST: arrow functions, exports |
| **Java/C#** | Regex: class/method | Full AST: inner classes, annotations |
| **C/C++** | Regex: function/struct | Full AST: templates, namespaces |
| **Rust** | Regex: fn/struct/impl | Full AST: generics, macros |
| **Dependencies** | None | CGo + tree-sitter grammars |
| **Chunk Expansion** | — | Parent scope context headers |

### Building with Tree-sitter

```bash
# Build with tree-sitter support
go build -tags treesitter -o gleann ./cmd/gleann/

# Run tree-sitter tests
go test -tags treesitter ./internal/chunking/ -v -run TestTreeSitter

# Both FAISS and tree-sitter
go build -tags "faiss treesitter" -o gleann ./cmd/gleann/
```

Without `-tags treesitter`, all non-Go languages use regex patterns and the binary remains pure Go.

### What Tree-sitter Improves

**Nested structures** — Regex `^    def` only matches 1 indent level. Tree-sitter correctly parses:

```python
class Outer:
    class Inner:          # ← regex misses this
        def deep_method(self):  # ← regex misses this
            pass
```

**Chunk expansion** — Each chunk gets a parent scope header for better embedding quality:

```python
# File: calc.py | Scope: Calculator
def add(self, a, b):
    return a + b
```

**Accurate boundaries** — Tree-sitter knows exactly where a function/class ends (matching braces, indentation), while regex guesses from the next boundary pattern.

## Usage

### As a Go Library

```go
package main

import (
    "context"
    "fmt"

    "github.com/tevfik/gleann/pkg/gleann"
    "github.com/tevfik/gleann/internal/embedding"
)

func main() {
    ctx := context.Background()

    // Create embedding computer (Ollama).
    embedder := embedding.NewComputer("nomic-embed-text", "ollama", 768)

    // Configure.
    config := gleann.DefaultConfig()
    config.IndexDir = "./indexes"

    // Build index.
    builder, _ := gleann.NewBuilder(config, embedder)
    builder.Build(ctx, "my-docs", []gleann.Item{
        {Text: "Go is a statically typed language"},
        {Text: "Python is dynamically typed"},
        {Text: "Rust has zero-cost abstractions"},
    })

    // Search.
    searcher := gleann.NewSearcher(config, embedder)
    searcher.Load(ctx, "my-docs")
    defer searcher.Close()

    results, _ := searcher.Search(ctx, "compiled languages",
        gleann.WithTopK(3),
        gleann.WithMinScore(0.5),
    )

    for _, r := range results {
        fmt.Printf("%.4f: %s\n", r.Score, r.Text)
    }
}
```

### Hybrid Search (Vector + BM25)

```go
import "github.com/tevfik/gleann/internal/bm25"

scorer := bm25.NewScorer()
adapter := gleann.NewBM25Adapter(scorer)
searcher.SetScorer(adapter)

results, _ := searcher.Search(ctx, "neural networks",
    gleann.WithTopK(10),
    gleann.WithHybridAlpha(0.7), // 70% vector, 30% BM25
)
```

### CLI

```bash
# Interactive setup wizard
gleann setup

# Build index from documents
gleann build my-docs --docs ./documents/

# Search
gleann search my-docs "what is HNSW?"

# Search with reranking
gleann search my-docs "what is HNSW?" --rerank

# Chat with an index
gleann chat my-docs

# Ask a question (single-shot)
gleann ask my-docs "Explain the architecture" --interactive

# List indexes
gleann list

# Get index info
gleann info my-docs

# Remove index
gleann remove my-docs

# Start REST API server
gleann serve --addr :8080

# Start MCP server (for AI editors)
gleann mcp

# Launch TUI
gleann tui

# Show version
gleann version
```

### REST API

```bash
# Start server
gleann serve --addr :8080

# Health check
curl http://localhost:8080/health

# List indexes
curl http://localhost:8080/api/indexes

# Search
curl -X POST http://localhost:8080/api/indexes/my-docs/search \
  -H "Content-Type: application/json" \
  -d '{"query": "what is HNSW?", "top_k": 5}'

# Build index
curl -X POST http://localhost:8080/api/indexes/new-index/build \
  -H "Content-Type: application/json" \
  -d '{"texts": ["doc1 text", "doc2 text"]}'

# Delete index
curl -X DELETE http://localhost:8080/api/indexes/my-docs
```

## Configuration

```go
config := gleann.Config{
    IndexDir: "~/.gleann/indexes",
    Backend:  "hnsw",
    HNSWConfig: gleann.HNSWConfig{
        M:                32,   // Max connections per node
        EfConstruction:   200,  // Build-time beam width
        EfSearch:         128,  // Search-time beam width
        PruneEmbeddings:  true, // Enable storage optimization
        PruneKeepFraction: 0.0, // Keep only entry point (max savings)
    },
    ChunkConfig: gleann.ChunkConfig{
        MaxChunkSize: 512,
        Overlap:      50,
    },
}
```

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `OLLAMA_HOST` | Ollama server URL | `http://localhost:11434` |
| `OPENAI_BASE_URL` | OpenAI-compatible API URL | `https://api.openai.com` |
| `OPENAI_API_KEY` | OpenAI API key | — |

## Benchmarks

All benchmarks on Intel i9-13900H (20 threads), Go 1.22, Linux.

### HNSW Search Performance

| Dataset Size | ef | Latency | QPS | Recall@10 |
|-------------|-----|---------|-----|-----------|
| 1,000 | 32 | 325µs | 3,078 | 79.8% |
| 1,000 | 64 | 586µs | 1,707 | 92.5% |
| 1,000 | 128 | 717µs | 1,394 | 98.8% |
| 1,000 | 256 | 1.08ms | 930 | 99.9% |
| 5,000 | 32 | 657µs | 1,522 | — |
| 5,000 | 128 | 1.48ms | 675 | — |
| 10,000 | 128 | 2.91ms | 344 | — |

### Storage Reduction (CSR + Pruning)

| Embedding Dim | Vectors | Full Size | Pruned Size | **Savings** |
|--------------|---------|-----------|-------------|-------------|
| 128 | 1,000 | 927 KB | 427 KB | **53.9%** |
| 384 | 1,000 | 1,928 KB | 429 KB | **77.7%** |
| 768 | 1,000 | 3,428 KB | 431 KB | **87.4%** |
| 768 | 5,000 | 17,156 KB | 2,159 KB | **87.4%** |

### Memory Savings (In-Memory Graph → CSR)

| Dim | Vectors | Graph | CSR (pruned) | Savings |
|-----|---------|-------|-------------|---------|
| 128 | 1,000 | 1.06 MB | 0.42 MB | 60.4% |
| 384 | 5,000 | 10.35 MB | 2.13 MB | 79.5% |
| 768 | 5,000 | 17.68 MB | 2.13 MB | **88.0%** |

### Build Throughput

| Operation | N=100 | N=1,000 | N=10,000 |
|-----------|-------|---------|----------|
| HNSW Insert | 13ms | 787ms | 34.8s |
| CSR Convert | 102µs | 1.37ms | 18.0ms |
| End-to-End Pipeline | 28ms | 856ms | — |

### BM25 Performance

| Operation | Corpus | Latency |
|-----------|--------|---------|
| Score (1K docs) | 1,000 | 787µs |
| TopK (5K docs) | 5,000 | 59ms |

### HNSW vs Brute Force (5K vectors, 128-dim)

| Method | Latency/query | Recall@10 |
|--------|---------------|-----------|
| Brute Force | 43.8ms | 100% |
| HNSW (ef=128) | 41.5ms | 98.8% |
| HNSW (ef=256) | — | 99.9% |

> At 5K vectors, HNSW's advantage is modest (1.1x). The sub-linear advantage grows significantly with larger datasets (50K+).

### Retrieval Quality Report (5K vectors, 128-dim, 30 clusters, 500 queries)

Full 5-stage quality evaluation via `go test -v -run TestRecallReport ./benchmarks/`.

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

## Project Structure

```
gleann/
├── cmd/gleann/            # Single CLI binary (TUI, REST, MCP, all commands)
│   └── main.go
├── pkg/gleann/            # Public API
│   ├── types.go           # Core types (Config, Item, SearchResult)
│   ├── interfaces.go      # Interfaces (Backend, Embedder, Chunker, Scorer)
│   ├── registry.go        # Backend registry (auto-discovery)
│   ├── builder.go         # LeannBuilder (build indexes)
│   ├── searcher.go        # LeannSearcher (search + hybrid)
│   ├── passage.go         # PassageManager (JSONL + offset index)
│   ├── bm25_adapter.go    # BM25 → Scorer interface adapter
│   ├── chat.go            # LeannChat (search + LLM answer, 3 providers)
│   ├── filter.go          # MetadataFilterEngine (12 operators, AND/OR)
│   ├── react.go           # ReAct agent (Thought-Action-Observation)
│   ├── reranker.go        # Cross-encoder reranking pipeline
│   └── sync.go            # FileSynchronizer (SHA-256 change detection)
├── internal/
│   ├── backend/hnsw/      # HNSW implementation
│   │   ├── hnsw.go        # Pure Go HNSW graph (~750 lines)
│   │   ├── csr.go         # CSR format + pruning (~585 lines)
│   │   └── backend.go     # Backend factory + builder/searcher
│   ├── backend/faiss/     # FAISS backend (optional, CGo)
│   │   ├── backend.go     # CGo FAISS HNSW via libfaiss C API
│   │   └── benchmark_test.go # FAISS vs Pure Go comparison
│   ├── bm25/              # Okapi BM25 scorer
│   ├── chunking/          # Text/code chunking
│   │   ├── chunking.go    # Sentence/paragraph chunker
│   │   ├── ast_chunker.go # AST-aware code chunker (8 languages)
│   │   ├── treesitter.go  # Tree-sitter backend (optional, CGo)
│   │   └── treesitter_stub.go # Stub when tree-sitter disabled
│   ├── embedding/         # Ollama/OpenAI/Gemini compute + server
│   ├── mcp/               # MCP server (JSON-RPC 2.0 over stdio)
│   ├── server/            # REST API server
│   └── tui/               # Bubble Tea interactive TUI
│       ├── onboard.go     # Setup wizard (13-step guided config)
│       ├── home.go        # Home menu
│       ├── chat.go        # Chat interface
│       ├── indexlist.go   # Index browser
│       ├── indexmanage.go # Index manager (build/delete)
│       ├── install.go     # Install/uninstall + MCP config generation
│       └── styles.go      # Shared TUI styles
├── benchmarks/            # Performance benchmarks & reports
└── tests/                 # Integration tests
```

## Design Decisions

### Pure Go HNSW (default, no FAISS/CGo)

- Zero external C dependencies → easy cross-compilation
- Single binary deployment (~2.5 MB)
- 98.8% recall@10 matches production HNSW quality

### Optional FAISS Backend (CGo)

- 15-34x faster builds, 3-28x faster search via AVX2 SIMD + OpenMP
- Same `BackendFactory` interface — just change backend name
- Enabled via `-tags faiss` build flag — excluded by default

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
| ReAct Agent | ✅ | ✅ |
| AST-aware Chunking | tree-sitter | go/ast + optional tree-sitter |
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

## Roadmap

| Feature | Status |
|---------|--------|
| Interactive TUI | ✅ Done |
| Two-stage reranker | ✅ Done |
| MCP server (embedded) | ✅ Done |
| Setup wizard + install | ✅ Done |
| DiskANN backend (100M+ vectors) | Planned |
| IVF backend (PQ quantization) | Planned |
| Web search tool for ReAct agent | Planned |

## Testing

```bash
# All tests
go test ./...

# Include FAISS backend tests (requires libfaiss)
go test -tags faiss ./...

# Verbose
go test ./... -v

# Specific package
go test ./internal/backend/hnsw/ -v

# FAISS backend tests
go test -tags faiss ./internal/backend/faiss/ -v

# FAISS vs Pure Go comparison
go test -tags faiss -run TestFAISSvsPureGo -timeout 300s ./internal/backend/faiss/ -v

# Recall quality benchmarks
go test -v -run "TestRecallBasic|TestRecallHighDim|TestRecallAtMultipleK" ./benchmarks/
go test -v -run "TestRecallWithRecompute|TestRecallComplexitySweep|TestRecallAfterCSRRoundtrip" ./benchmarks/

# Full 5-stage retrieval quality report
go test -v -run TestRecallReport ./benchmarks/ -timeout 300s

# Performance benchmarks
go test ./benchmarks/ -bench=. -benchmem -run=^$

# FAISS benchmarks
go test -tags faiss -bench=BenchmarkFAISS -benchmem ./internal/backend/faiss/

# Performance reports
go test ./benchmarks/ -run="Report|Comparison" -v
```

## License

MIT
