# Retrieval experiments (code search)

What was tried to improve gleann's code retrieval, what helped, and what was
left out. Use it before re-running an idea that was already measured.

## Setup

- **Corpus:** `git archive HEAD` of gleann without `bench/` (1040 files,
  22 891 passages, including vendored C headers). Indexed as `bench-nomic`
  with nomic-embed-text (768-d), HNSW, hybrid BM25 + RRF.
- **Task sets:**

  | Set | Tasks | Kind |
  |---|---|---|
  | `bench/tasks/gleann_nl.json` | 121 | natural-language questions |
  | `bench/tasks/gleann.json` | 20 | symbol lookups |
  | `gen_doc` | 150 | queries paraphrased from Go doc comments |
  | `gen_sym` | 100 | symbol-name queries |

  `gen_doc` and `gen_sym` were generated after the decisions on the first two
  sets were taken, so they are held out.
- **Metrics:** file-level R@1, R@10 and MRR. Results are compared task by task
  with a 95% bootstrap CI and a sign test.
- **Noise:** the default hybrid search varies by MRR 0.48–0.50 from run to run.
- **Harness:** `gleann bench` with `GLEANN_BENCH_PERTASK=<file>` writes
  per-task rows. `GLEANN_BENCH_RERANK=1` (with `_MODEL`, `_PROVIDER`, `_URL`,
  `_POOL`, `_CHARS`) adds reranker strategies, and
  `GLEANN_BENCH_EXPAND_MODEL=<ollama model>` adds HyDE strategies. The
  offline studies (Python baselines, candidate dumps) are not part of the
  repository.

## Shipped

| Feature | Result | Cost |
|---|---|---|
| `--expand` (HyDE, qwen3.5:9b) | NL questions MRR 0.485 → **0.629** (+0.144, p<0.001), R@1 0.34 → 0.49, R@10 0.71 → 0.81; symbol lookups +0.10 (n.s.); doc-comment paraphrases −0.05 (p=0.02) | one LLM call (~1.5–3 s on GPU) |
| `--expand` + `--rerank` | NL +0.123 (p<0.001); no set loses; overall MRR +0.048 | LLM call + ~0.4 s |
| Reranker pool 20, 500 chars, identifier gate (MiniLM-L6, in-process) | MRR +0.03–0.04, R@1 +0.06–0.07 over 391 tasks; NL questions +0.07–0.08; no significant loss on any set | ~0.4 s on CPU |
| `--explain` (occlusion, 8 segments) | sufficiency 1.10 vs 0.89 for "first lines"; preferred 73/37 over term overlap | ~70 ms per result |
| MCP `focus` | uses the explain segments, ~4× fewer tokens per result | ~70 ms per result |
| `ask --cite` (post-hoc attribution, τ=0.6) | coverage 0.73, gold-file precision 0.28 (all retrieved: 0.21), judged support 0.72; better than inline `[n]` citations | ~100 ms |

### HyDE details

- **Prompt:** the prompt asks for a short code snippet (signatures,
  identifiers, comments, at most 15 lines) in the index's dominant language.
  The query and the snippet are concatenated for both embedding and BM25.
- **Variants measured offline** (141 tasks, MRR; base 0.486):

  | Variant | MRR |
  |---|---|
  | Snippet, Go hint, qwen3.5:9b, 200 tokens | **0.671** |
  | Snippet, 100 tokens | 0.661 |
  | Snippet, llama3.2:3b | 0.608 |
  | Snippet, no language hint | 0.577 |
  | Identifier list instead of a snippet | 0.532 |
  | RRF of query and expanded rankings instead of concatenation | 0.621 |
  | doc→code bridging (identifiers from the top doc chunks) | 0.339 (rejected) |

- **Two details carry most of the gain end to end:**
  - **Symbol-name boost.** The "query contains symbol name" boost must also
    see the snippet. Without it the gain disappeared (NL −0.01).
  - **Language detection.** The dominant language must ignore vendored paths.
    A prefix sample of this index returned "C" (vendored headers) instead of
    "Go".
- **Identifier queries** are never expanded.
- **The model matters.** End to end, llama3.2:3b gained only MRR +0.03 on
  questions and lost 0.06 on doc-comment paraphrases; with the reranker it
  was no better than the reranker alone. qwen3.5:9b gives the gain above.
  The offline file-level numbers (top-10 distinct files) overstate the
  end-to-end gain (top-10 chunks): +0.185 vs +0.144.

### Reranker strategy grid

Results are MiniLM-L6 in-process (bitllm.c), pool 20, 500 chars, over the
four sets.

| Strategy | ΔMRR | ΔR@1 |
|---|---|---|
| **replace + identifier gate** | **+0.039** | **+0.074** |
| replace, no gate | +0.033 (gen_sym −0.030) | |
| blend 0.7 · reranker + 0.3 · first stage | +0.029 | |
| blend 0.5 | +0.024 | |
| RRF of reranker and first-stage order | +0.009 | |

The identifier gate is now on by default (`SearchConfig.RerankSkipIdentifiers`). The
blend and RRF modes were not kept, because neither beat plain replacement.

Model comparison (replace + gate, same settings):

| Model | NL ΔMRR | Overall ΔMRR | Latency p50 (CPU, incl. search) |
|---|---|---|---|
| ms-marco MiniLM-L6, pooler patched, catalog `ms-marco-minilm-l6` | +0.069 | +0.029 | ~0.41 s |
| ms-marco MiniLM-L6, pooler patched, another Q8_0 base | +0.082 | +0.039 | ~0.41–0.65 s |
| mMiniLMv2-L12 mMARCO (current default, multilingual) | +0.011 | +0.015 | ~0.77 s |

Without the gate, the catalog MiniLM-L6 loses on gen_sym (MRR −0.051,
p=0.02).

`gleann models download ms-marco-minilm-l6` adds the pooler at download.
gleann range-requests the two tensors from the original safetensors and
appends them (`internal/modelcatalog/pooler.go`). The result is byte-identical
to the offline Python patch that was checked for score parity with
PyTorch.

**Community GGUF caveat.** Community ms-marco MiniLM GGUFs lack the BERT
pooler (`cls.weight`/`cls.bias`), and their scores are nearly constant. The
default mmarco-mMiniLMv2 GGUF has the pooler.

**Ollama has no rerank API.** The former Ollama "reranker" compared
embeddings: MRR −0.017, and it crashed for bge-reranker-v2-m3. It now returns
an error that points to `eif` or llama-server `--reranking`.

### CPU cost of reranking

Setup:
- **Tasks:** 121 natural-language questions.
- **Reranker settings:** pool 20, 500 characters.
- **Hardware:** i9-13900H, with the process pinned to the given number of
  physical cores (`taskset` + `OMP_NUM_THREADS`).
- **Latency:** end-to-end search per query, measured by `gleann bench`.

| Configuration | MRR | R@1 | p50 | p95 | Peak RSS |
|---|---|---|---|---|---|
| Hybrid, no reranker | 0.49 | 0.35 | 62 ms | 89 ms | 0.44–0.46 GB |
| MiniLM-L6, 1 core (1 thread) | 0.57 | 0.46 | 2.23 s | 2.59 s | 0.50 GB |
| MiniLM-L6, 1 core (2 hyperthreads) | 0.57 | 0.46 | 2.07 s | 2.39 s | 0.51 GB |
| MiniLM-L6, 2 cores (4 threads) | 0.57 | 0.46 | 1.10 s | 1.26 s | 0.58 GB |
| MiniLM-L6, all cores (20 threads) | 0.57 | 0.46 | ~0.4 s | | |
| mMiniLMv2 mMARCO, 1 core (2 hyperthreads) | 0.50 | 0.34 | 4.38 s | 5.07 s | 0.71 GB |
| mMiniLMv2 mMARCO, 2 cores (4 threads) | 0.50 | 0.34 | 2.28 s | 2.60 s | 0.98 GB |

How cost behaves:
- **Cores:** cost falls roughly linearly with physical cores and is about
  linear in the pool size.
- **Memory:** each worker thread loads its own copy of the model, so RSS
  grows with the thread count (MiniLM-L6: 25 MB per copy; mMARCO: 130 MB).

Reranking stays off by default, and is worth it for agents and `ask`. On
a 2-core machine it raises search time from ~60 ms to ~1.1 s per query.

## Measured and not shipped

| Idea | Result | Why not |
|---|---|---|
| Convex / BB25 score fusion | within noise of RRF at every pool size and α | no gain |
| Semantic-graph neighbour expansion | R@10 −5..−8 points | hurts |
| Topic clusters (kNN graph + multi-level Louvain) | NMI 0.68 vs 0.64 for k-means with the same k; 2–6× faster | useful for browsing, not for retrieval; deferred |
| os-v3 inference-free sparse (instead of BM25) | lexical leg MRR +0.116 vs BM25; hybrid only +0.013 | 65 min CPU indexing for a small hybrid gain |
| ColBERT MaxSim rerank (GTE-ModernColBERT) | MRR +0.06 | 49 min CPU indexing, 857 MB store; the cross-encoder gives the same gain without an index |
| MUVERA FDE | recall of exact top-10 ≤ 0.26 | approximation too weak at feasible sizes |
| LLM listwise rerank (qwen3.5:9b, 20 files) | MRR +0.10 | 2.6 s per query, no better than HyDE + cross-encoder |
| gte-modernbert cross-encoder ⊕ RRF | MRR +0.095 | 11.8 s per query on CPU |
| LLM entity/relation KG for retrieval | MRR +0.017 (n.s.) | 16–38 h to extract a full index |
