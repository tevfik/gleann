# RFC 002: Retrieval Quality — Reranking, Query Expansion, Explanations, Citations

* **Status:** Implemented on `main` (4 commits), awaiting maintainer review before release
* **Commits:** `feat(rerank)`, `feat(search)` explain, `feat(ask)` cite, `feat(search)` expand
* **Evidence:** [docs/retrieval-experiments.md](../retrieval-experiments.md)

---

## 1. Problem

Measured with `gleann bench` on the gleann code base (391 tasks in 4 sets).

| Problem | Evidence |
|---|---|
| Natural-language questions are found poorly | Hybrid retrieval puts the right file first for only 34% of questions (MRR 0.485). The words of a question rarely appear in the code that answers it. |
| The shipped reranker setups did not help code search | bge-reranker-v2-m3 through llama-server: MRR −0.017 at ~5 s per query. The default mmarco model: +0.01. The `ollama` provider compared embeddings instead of reranking, and crashed for bge-reranker-v2-m3. |
| Rerankers hurt exact symbol lookups | Without a gate, symbol queries lost MRR 0.05 (p=0.02). |
| `gleann ask --rerank` and `--graph` did nothing | Unless saved in the settings, both flags were applied after the searcher had copied the configuration. |
| REST `ask` could report the wrong sources | It searched a second time to list sources, which could differ from the passages the LLM saw. |
| No way to see why a result matched, or which passages an answer used | — |

## 2. Design

### 2.1 Rerank stage (`pkg/gleann/rerank_stage.go`)

Every search path calls one function, `rerankStage`. It applies three rules:

1. **Pool.** `RerankPool` limits only the candidates sent to the
   reranker. The first stage keeps its wide dense and BM25 lists, because
   narrowing them lowered the quality of the pool.
2. **Identifier gate.** Identifier-like queries are left in first-stage
   order (`IsIdentifierQuery`): one token, or two tokens of which one is
   code-shaped. The gate is on by default.
3. **Truncation.** `RerankerConfig.MaxDocChars` truncates candidates before
   scoring, and the full text is restored in the results.

Two alternatives were measured and dropped: fusing the reranker with the
first-stage order by RRF, and blending their scores. Neither beat plain
replacement.

### 2.2 MiniLM-L6 reranker with pooler patch (`internal/modelcatalog/pooler.go`)

Published GGUFs of `cross-encoder/ms-marco-MiniLM-L6-v2` have no BERT
pooler, so their scores are nearly constant.

The catalog entry `ms-marco-minilm-l6` fixes this at download:

- **Inputs:** a `pooler` block names the original safetensors file and its
  two pooler tensors.
- **Fetch:** after the GGUF is downloaded and its checksum verified,
  gleann range-requests just those tensors (~600 KB).
- **Patch:** it appends them as `cls.weight` / `cls.bias` and checks the
  size of the result.
- **Verification:** the output is byte-identical to an offline patch that
  was checked for score parity with PyTorch.

We do not have to host a model file ourselves.

### 2.3 Query expansion (`pkg/gleann/expand.go`)

`QueryExpander` is an interface with one implementation, `LLMQueryExpander`
(HyDE).

1. The LLM writes a short code snippet (at most 15 lines) in the index's
   dominant language. Vendored paths are ignored when detecting the
   language.
2. The query plus the snippet is used for embedding and BM25.
3. The "query contains symbol name" boost also sees the snippet. Exact
   symbol boosts, test detection and the reranker use the original query.

Behaviour:

- **Never expanded:** identifier queries.
- **Cache:** snippets are cached per query and language.
- **Errors:** an LLM error falls back to the plain query.
- **Default:** off. It needs a ~9B model to help.

### 2.4 Explanations (`pkg/gleann/explain.go`, `bm25.ExplainDoc`)

Each result is split into 8 segments. A segment's importance is the drop
in query similarity when that segment is masked. Per-term BM25
contributions come from a separate function, so the scoring hot path is
unchanged.

MCP `focus` returns the most important quarter of each result.

### 2.5 Citations (`pkg/gleann/citations.go`)

`Attribute` links each answer sentence to the most similar context passage
(cosine similarity ≥ 0.6). The prompt is unchanged.

`LeannChat` reports the passages it used: `AskWithResults` returns them, and
`SetResultsHandler` delivers them to the REST `ask` handler, which no longer
searches a second time.

## 3. Build Tag & Platform Impact

| Item | Impact |
|---|---|
| New code | Pure Go. No new CGo, no build tags. |
| Dependencies | None added; `go.mod` is unchanged. |
| In-process reranking | Still needs `-tags eif`. Builds without it return the existing descriptive error. |
| Python | None in the core; the pooler patch is Go. |
| Docker | Not required. |
| Platforms | No platform-specific calls. |

## 4. Compatibility & Migration

| Change | Effect on existing users |
|---|---|
| Identifier gate on by default | Rerank users stop reranking one-word or code-shaped queries. Opt out per request with `WithRerankSkipIdentifiers(false)`. |
| `rerank_provider: ollama` returns an error | Users of that setting get an error instead of the previous weaker-than-no-rerank scores. The error names the alternatives. |
| `rerank_provider: llamacpp` calls `/v1/rerank` | It used to compare embeddings through `/api/embed`. It now needs a llama-server started with `--reranking` (the documented setup). |
| New settings `expand`, `expand_model` | Default off. Old `config.json` files load unchanged. |
| `ask --rerank` / `--graph` now take effect | Intended fix, also for multi-index and `<name>-code`/`<name>-docs` asks, and for multi-index `search --rerank`. Reranking makes these commands slower. |
| Index format | Unchanged; no re-index needed. |

## 5. Test & Verification Matrix

| Check | Result |
|---|---|
| Unit tests | Rerank stage, identifier gate, expansion (fake LLM over httptest, cache, fallback), explanation, citations, BM25 term breakdown, pooler patch (F32/BF16/F16, refuses double patch, `Download` end to end) |
| Network-dependent test | Patch of the real Hugging Face file versus a reference; opt-in with `GLEANN_POOLER_REF_IN` / `_OUT` |
| Each commit | Exported separately; `go build ./...`, `go vet`, `gofmt`, tests of the touched packages |
| Final tree | `go test -race ./...`, `staticcheck` (the CI version), pure-Go / `eif` / `treesitter` builds |
| Quality and cost | `gleann bench` with `GLEANN_BENCH_PERTASK`, `GLEANN_BENCH_RERANK*`, `GLEANN_BENCH_EXPAND_MODEL`. Numbers in [docs/retrieval-experiments.md](../retrieval-experiments.md), including CPU cost per core count. |

## 6. Audit

Gemini Flash (agy) audited this RFC and the commits against AGENTS.md. Its
verdict was "approve with changes", with no AGENTS.md violations.

Addressed in a follow-up commit:

- **Flags lost in other paths:**
  - `ask --rerank`, `--graph` and `--expand` were ignored for multi-index
    and code/docs-split indexes.
  - `search --rerank` was ignored for multi-index searches.
  - The values of `--rerank-url`, `--rerank-pool` and `--rerank-provider`
    leaked into the `ask` question.
  - REST `ask` ignored `expand`.
- **Pooler download:** it now validates safetensors offsets and shapes and
  rejects a truncated GGUF.
- **Language detection:**
  - Windows paths are normalised before vendor directories are excluded.
  - A call on an unloaded searcher no longer disables detection.
- **Small items:** wrapped errors; the bench HyDE + rerank strategy now uses
  the same reranker settings as the plain rerank strategy.

Kept as is, with reasons:

- **A failed pooler fetch also removes the downloaded base file.** It is
  25 MB, and keeping it under the final name would install an unpatched
  model.
- **The HTTP-200 fallback does not drain the body.** Draining would
  download the whole ~90 MB checkpoint just to reuse a connection.
- **`search --explain` with several indexes** prints a note that it works
  for a single index only.

## 7. Not Included

| Item | Reason |
|---|---|
| Semantic topic clusters (`index topics`) | Useful for browsing, not for retrieval. Parked locally for a later RFC. |
| Score fusion `--fusion convex/bb25` | No gain over RRF. |
| ColBERT, os-v3 sparse, LLM knowledge graph | Too costly to index for the gain (see the experiments doc). |

## 8. Maintainer Decisions

Both questions this RFC raised were accepted.

1. **Default model for code.** With no `rerank_model` set, the in-process
   engine reranks candidates that are mostly source code (by file
   extension) with `ms-marco-minilm-l6` when it is downloaded. In every
   other case it uses mMiniLMv2, which stays the choice for multilingual
   documents.
2. **Pool and truncation.** The in-process engine defaults to 20
   candidates of 500 characters each (`RerankerConfig.Pool`,
   `MaxDocChars`). Server rerankers keep scoring every candidate in full:
   the PDF benchmark measured bge-reranker-v2-m3 on a GPU with 50
   candidates. `--rerank-pool` and explicit settings still win.
