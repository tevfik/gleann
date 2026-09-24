# Recommended Models & Index Migration Guide

Gleann is built for high-precision code intelligence and agentic memory retrieval. This guide outlines recommended embedding and reranking models, compatibility rules, and index migration steps.

---

## 1. Recommended Embedding Models

Embedding models map code passages and documentation into vector representations. For code intelligence, models with strong multilingual and identifier tokenization are essential.

| Model | Dimensions | Provider | Strengths | Recommended For |
|---|---|---|---|---|
| **`bge-m3`** *(Default)* | 1024 | Ollama / LlamaCPP | Multi-lingual, dense + sparse synergy, 8192 context window | Production codebases, mixed Go/Rust/Python/TS |
| **`nomic-embed-text`** | 768 | Ollama | Fast inference, low RAM consumption, 8192 context | Local laptops, fast iteration |
| **`text-embedding-3-small`** | 1536 | OpenAI | High precision, cloud-managed, zero local GPU/RAM requirement | Cloud environments, CI/CD |
| **`all-minilm-l6-v2`** | 384 | Ollama / HuggingFace | Minimal RAM (<500MB), ultra-fast CPU inference | Resource-constrained devices |

---

## 2. Recommended Reranker Models (Cross-Encoders)

Cross-encoders evaluate the query and candidate passage simultaneously, computing deep semantic attention rather than separate cosine distances.

| Model | Provider | Strengths | MCP / CLI Flag |
|---|---|---|---|
| **`bge-reranker-v2-m3`** *(Default)* | Ollama / LlamaCPP | Multilingual, optimized for technical code & docs | `--rerank-model bge-reranker-v2-m3` |
| **`jina-reranker-v2-base-multilingual`** | Jina AI / HTTP | 8K context, high code rank accuracy | `--rerank-model jina-reranker-v2-base-multilingual` |
| **`cohere-rerank-v3.5`** | Cohere API | Industry standard accuracy for complex reasoning | `--rerank-model rerank-english-v3.0` |

---

## 3. Embedding Model Consistency & Migration

Gleann enforces strict embedding consistency:
1. **Dimension Protection:** If an index was created with 1024 dimensions (`bge-m3`) and the server attempts to query it with a 768-dimension model (`nomic-embed-text`), Gleann immediately aborts before backend query execution to prevent crashes or vector corruption.
2. **MCP Auto-Adaptation:** The Gleann MCP server inspects the index's `meta.json`. If the index was built with a specific embedding model (e.g. `nomic-embed-text`), MCP automatically initializes an embedder matching that model for that index.

### Migrating an Index to a New Model

If you switch your default embedding model, rebuild the index:

```bash
# Rebuild existing index with new model
gleann index rebuild <index-name> --model <new-model> --docs <code-dir>

# Example: rebuild myrepo with bge-m3
gleann index rebuild myrepo --model bge-m3 --docs /path/to/myrepo
```

To incrementally refresh without changing models:
```bash
gleann index sync <index-name> --docs <code-dir>
```

---

## 4. Enabling Reranker in MCP & CLI

### CLI
```bash
# Search with default reranker
gleann search myrepo "authentication handler" --rerank

# Search with custom reranker model
gleann search myrepo "store initialization" --rerank --rerank-model bge-reranker-v2-m3
```

### MCP Tool Usage
Set `"rerank": true` in `gleann_search` or `gleann_search_ids`:
```json
{
  "index": "myrepo",
  "query": "OpenStore configuration",
  "top_k": 5,
  "rerank": true
}
```
Or start the MCP server with environment variables:
```bash
GLEANN_RERANK=1 GLEANN_RERANK_MODEL=bge-reranker-v2-m3 gleann mcp
```
