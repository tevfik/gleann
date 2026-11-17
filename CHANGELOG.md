# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Breaking changes / Removed
- **Kùzu is removed; the code graph is SQLite in every build (RFC 004 A3).** gleann no longer depends on `go-kuzu`/`libkuzu` (the full build ships no `libkuzu`), and `-tags treesitter` now only adds the tree-sitter parsers. The code graph of a format-2 index (`<name>/<name>.db`) is unchanged. **A format-1 index has no code graph any more:** its `<name>_graph` Kùzu directory is not read; `gleann graph …`, the MCP graph tools, `/api/graph/*` and A2A answer `index X has no code graph yet` with the hint `gleann index migrate X`, then `gleann index build X --docs <dir> --graph`; `index build|sync --graph` on it skips the graph with a notice, and `POST /api/graph/X/index` answers 409. The MCP graph tools are advertised only when a format-2 graph can exist (a format-2 index in the index directory or `GLEANN_STORE_FORMAT=2`), also in tree-sitter builds. `GLEANN_KUZU_AUTOREPAIR` is gone; the `module` field of `POST /api/graph/{name}/index` is accepted and ignored. `gleann index remove` still deletes an old `<name>_graph` directory. The memory vector syncer (`<name>_memory_vectors`) now runs in every build, `CGO_ENABLED=0` included. Memory entities of old `<name>_memory` Kùzu stores are still carried over with `kuzu-memory-export` (a separate module) and `gleann memory import-graph`.

### Security
- **The REST server no longer indexes or reads arbitrary directories on a client's request.** `index-path`, `update` (`docs_root`, `sources`), `watch` (`dir`), graph `docs_dir` and the A2A multimodal file path must lie inside `allowed_roots` (new setting, or `GLEANN_ALLOWED_ROOTS`; default: your home directory), otherwise the server answers 403. Paths are resolved through symlinks before the check. The command line is not restricted. Servers that index directories outside the home directory through the API need `allowed_roots` set.
- **MCP file tools stay inside the project.** `gleann_read_full_document` reads only inside the index's source directory; `gleann_read` reads inside the agent's working directory (unless that is a file-system root or the home directory) and the source directories of the exposed indexes; `gleann_sync` keeps `docs_dir` and `files` inside the allowed roots or the source directory. A refused path gets the same answer as a missing file.

- **Plugins get a bearer token.** gleann creates a token per plugin on first launch, keeps it in `<gleann home>/auth/plugins/<name>.token` (0600, shared by all gleann processes), passes it to the plugin as `GLEANN_PLUGIN_TOKEN` and sends it as `Authorization: Bearer` on every request except `/health`. A `token` in `plugins.json` overrides it (for plugins started by hand). Plugins that do not check tokens keep working.
- `/api/proxy/models` no longer follows client-supplied loopback, private or link-local hosts, or redirects to them; the configured Ollama and OpenAI-compatible hosts stay reachable, and a configured host may not redirect to another host. `GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE=1` lifts the guard.

### Storage engine (RFC 003, opt-in in this release)
- **Persisted lexical index.** `GLEANN_LEXICAL=fts5` replaces the in-memory BM25 that every cold open rebuilt by a SQLite FTS5 index on disk. Measured at 100K passages (same binary): first query of a running server 4.5 s → 0.3 s, server memory 1.2 GB → 0.4 GB. `GLEANN_LEXICAL_OPTS=and,df=0.2` adds AND-first queries and a document-frequency cut that remove the latency tail (warm p95 135 → 55 ms) and keep the retrieval-quality gate (BM25 MRR 0.521 against 0.530, within tolerance). Default unchanged.
- **Format-2 index store.** `GLEANN_STORE_FORMAT=2` creates new indexes as one SQLite database (`<name>/<name>.db`: passages with stable IDs, the lexical index and the file manifest), written one transaction per source file, with crash-safe vector publication. At 1M passages with the lexical options: warm search p50 0.9 s → 47 ms, p95 1.2 s → 110 ms, first server query 35 s → 3.6 s, server memory 8.8 → 1.4 GB, index 2.46 → 1.97 GiB. Build time is about 10-18 % longer and a one-file sync still rewrites the vector file. `gleann index migrate <name>|--all [--rollback] [--cleanup]` converts an existing index; an older gleann that syncs a migrated index rebuilds it as format 1 from the sources. Default unchanged.
- **Memory moves to SQLite** (`~/.gleann/memory/memory.sqlite`). The first open copies `memory.db` under a lock, verifies the counts, keeps the old file as `memory.db.v1` and leaves a guard file, so several agent processes can now write memory at once. **After the first open, older gleann binaries on the same machine get an error on memory commands: update every installation together.**
- **One build or sync per index across processes.** Build, sync, repair and watch take the index lock `<IndexDir>/<name>.lock` (the one `index migrate` takes) for the whole operation. A second gleann process that builds, syncs or migrates the same index stops at once with a message naming the index; `index build --watch` keeps the changes and retries on the next change. Before, only bbolt's file lock kept two format-1 writers apart, and nothing did for format 2.
- **The memory entity graph works in every build.** `inject_knowledge_graph`, `delete_graph_entity`, `traverse_knowledge_graph` and `/api/memory/*` keep their entities in `memory.sqlite` (tables `entities`, `relations`, `entity_archive`, one namespace per index) instead of a Kùzu database per index, so they also work in the `go install` build and on Windows. Traversal keeps the depth cap of 10 and no longer slows down on densely linked entities. Entities stored by v1.9.x in an old `<name>_memory` Kùzu store (a single file; it is not a directory) are carried over with the separate `kuzu-memory-export` tool (`make kuzu-memory-export`) and `gleann memory import-graph <file.json|dir>`, which keeps ids, attributes, timestamps and the archive and is a no-op the second time; gleann logs the two commands once per process and `gleann doctor` lists unimported stores. The old store is never modified.
- **Failure memory.** `gleann memory failure add --command <c> --error <text> --fix <text> [--path p]... [--symbol s]...` and the MCP tool `memory_failure_add` record a command that failed and the fix that worked. The same failure recorded again (error text differing only in paths, line numbers, addresses or timestamps) confirms the existing record instead of adding one. `gleann memory context --command <c> --path <p>` and MCP `memory_context` with `command`/`paths` add the matching failures in their own section, inside the injection budget; `gleann memory failure list` shows them all. `memory_failure_add` is in the `core` MCP profile.
- The embedding cache writes each batch of vectors before computing the next, so a build stopped halfway keeps the vectors it already computed.
- A graph rebuild (`ReplaceAll`) no longer leaves a database-sized WAL file next to the index: free pages are reclaimed before the WAL is checkpointed, not after (at 1M edges: 183 MiB WAL removed, 364 → 182 MiB on disk).
- **The code graph of a format-2 index lives in the index database and works in every build.** `index build --graph` and `index sync` write symbols, edges and document nodes into `<name>/<name>.db` (one transaction per changed file; a file whose graph write fails keeps its old hash and is retried by the next sync), and `gleann graph deps|callers|explain|path|query|toc` and the MCP graph tools read it, also in the pure-Go `CGO_ENABLED=0` binary; no `<name>_graph` Kùzu directory is created. Without tree-sitter the graph comes from `go/ast` (Go files: calls, implements, references; other languages: declarations only). Format-1 indexes keep the Kùzu graph. `internal/analysis` (the tokenizer, with Turkish folding for Turkish prose and CJK bigrams) is not yet used.
- **Graph analysis works on the format-2 graph in every build.** `gleann graph viz|report|communities|export|wiki|hook|map|risk`, the MCP tools `gleann_communities`, `gleann_risk_analysis`, `gleann_repo_map` and `gleann_navigate_symbol`, `GET /api/graph/{name}`, `POST /api/graph/{name}/query|index`, `/toc`, `/documents` and the A2A skills (communities, repo map, risk) read the SQLite graph, also in the pure-Go `CGO_ENABLED=0` binary. Without tree-sitter, only Go files have call edges, so for other languages the analysis rests on declarations; tree-sitter CALLS for them is a separate step. Community detection, PageRank, risk, repo map, export and viz are now deterministic (before, the same Kùzu graph gave different community counts, ranks and edge directions from run to run). Compared with the Kùzu answers: no phantom nodes for standard-library and built-in calls, method calls between Go methods are found (`main -> Service.Report`), and a Go declaration is anchored at its `func` line, not its doc comment.
- **Raw Cypher is removed from the REST API and the UI** for format-2 indexes: `type: "cypher"` answers 400 `raw Cypher was removed; use callers/callees/symbols_in_file/impact`, and the Graph page no longer offers it. `GET /api/graph/{name}` of an index without a code graph is 404 (it answered `available: true` with zeros because opening the path created an empty Kùzu database), and `toc` of a path that is not an indexed document is 404 (it was a 500). A binary without tree-sitter answers format-1 graph requests with 503 only when the index really is format 1.
- **Exact Go call resolution in the format-2 graph.** When the indexed tree is a Go module that type-checks, a full build resolves CALLS with `go/types` (interface dispatch, promoted/embedded methods, aliased and dot imports, generics and method values; a shadowed local no longer resolves to the package-level function) and adds IMPLEMENTS edges from method sets, labelled `confidence='exact'`; standard-library/third-party callees stay external. Packages with type errors, trees without `go.mod`, cgo-only/build-tag-excluded files and incremental syncs keep the syntactic `go/ast` resolver, and no load failure aborts the build. On gleann's own tree the full graph build goes ~1.8 s → ~3.8 s and caller recall over 20 sampled functions rises 0.948 → 1.000 (precision ~0.99). It needs a Go toolchain and never downloads modules or edits `go.mod` (`GOPROXY=off`, `-mod=readonly`, two-minute bound). `GLEANN_GRAPH_EXACT=0` forces the syntactic resolver.
- **Call graph of Python, JavaScript, TypeScript, Rust, Java, C, C++, C#, Ruby, PHP and Swift sources in the format-2 graph (`-tags treesitter` builds).** `index build --graph` and `index sync` now extract tree-sitter CALLS into the SQLite graph (before, only the Kùzu path had them). A call is charged to the method that makes it (the Kùzu path charges the class), calls at the top level of a file to a per-file `<script:file>` symbol anchored at line 1, and a call to a method of the same package binds to it (`inferred`, or `ambiguous` with several candidates). A lite build still gives declarations only for these languages. A full build extracts files on `NumCPU` workers: 587 Python files of the standard library took 4.2-7.7 s serial and 1.4-1.5 s now (Kùzu path 1.6-1.8 s); an incremental sync re-extracts unchanged files whose calls a new declaration resolves.

### Fixed
- **An uploaded document lives only in its index.** `POST /api/indexes/{name}/upload` builds ran with the vault tracker (recording the deleted temp folder) and wrote every passage vector to `~/.gleann/cache/embeddings`, so deleting the index left traces. Upload builds now skip the vault and use a memory-only embedding cache (`CacheOptions.MemoryOnly`, `indexing.Options.NoEmbeddingDiskCache`); at start the server prunes the vault records of past upload folders (`Tracker.RemovePathsWithPrefix`, temp path normalised through symlinks, case-insensitive on Windows).
- **A long build keeps its answer.** The HTTP write timeout is `max(30 min, GLEANN_TIMEOUT_BUILD_S + 5 min)` instead of a fixed 30 minutes, so a build limit above 25 minutes no longer ends with a dropped connection.
- Format-2 indexes: `search --explain` shows the per-term breakdown for the FTS5 lexical scorer, `gleann info` and the storage inspection count `<name>.db` (and its -wal/-shm), and `index storage --set` backs up and restores the database with its WAL files.
- Adding to an index that does not exist now fails with a typed `index not found` error, and opening its passage store no longer creates an empty directory; the missing-index case is told apart from a format-1 index (`gleann.DetectStoreFormat`).
- **A running server, MCP server or chat sees another process's sync.** A cached searcher kept its vector index and resolved the renumbered passage slots of a format-2 index against it, so after a `gleann index sync` in another terminal a running `serve` could return the wrong passage text. Acquiring a searcher now compares the published vector generation and the graph state with the loaded one (one small read, no file walk) and reloads on a change; a search that straddles a publication returns `index changed while searching` (the REST search retries once) instead of a wrong passage, and a graph built after the server started is picked up.
- **A rebuild of a format-2 index keeps its code graph.** A lexical-only sync and `index build` without `--graph` deleted `<name>.db`, which also holds the graph, so the callers of every unchanged file were lost. The passages, the lexical index and the file manifest are now replaced inside the existing database in one transaction: a file that disappeared loses its symbols and edges in the same commit, a file whose content changed is marked for the next graph pass, and no database file is deleted while a server or another process has it open (Windows refused that). A database that cannot be opened (damaged, or written by an incompatible build) is still recreated, and then the graph is rebuilt from the sources, with a message.
- **Filtered search returns k results.** Metadata filters (`metadata_filters` in REST, `filters` in MCP, `WithMetadataFilters` in the API) were applied after the top-k retrieval, so a selective filter returned far fewer than k results although k passages matched (a filter matching 3% of the corpus returned ~2 of k=10). The candidate pool now widens until k results pass the filter, sized from the observed pass rate and capped at 4096 candidates, on the vector, lexical and hybrid legs; when the cap is reached short of k, a format-2 index pre-selects the matching IDs in SQL (no passage text read, at most 50,000 rows) and scores just those. A vector backend that cannot score given IDs (memory-mapped HNSW, DiskANN) keeps the matches the loop found and fills up behind them with the pre-selected passages that match the query words. A passage scores the same with and without a filter that keeps it (the vector score normalizer is fixed to the first candidate pool), the pre-selection fallback applies the same `min_score`, kind and ranking boosts as the main path, and filter field names are bound as SQL parameters, never interpolated into the statement. Unfiltered search is unchanged. Cost per query on a synthetic index (mock embedder, k=10), unfiltered / 3% / 0.01% filter: 24 / 55 / 261 ms at 20K passages, 256 / 503 / 521 ms at 200K.
- **`go install github.com/tevfik/gleann/cmd/gleann@latest` works** (from the next release on). `modules/bm25`, `chunking`, `diskann` and `hnsw` are packages of the root module and the root `go.mod` has no `replace` directives; `modules/settings` stays a separate module because the plugins import it.
- **A malformed `~/.gleann/config.json` is no longer ignored.** Commands stop with the file path and the line and column of the JSON error; the file is never rewritten. `version`, `help`, `config path|edit|validate`, `setup`, `doctor`, `completion` and `update` still run, and `config validate` and `doctor` report the error.
- **`gleann search` and `gleann ask` reject unknown flags** (exit code 2, `unknown flag --x`, with a suggestion for near misses); `--` ends flag parsing. The values of `--doc-model`, `--backend` and about a dozen other flags no longer leak into the search query.
- **Every command rejects unknown flags** (exit code 2 with a "did you mean" hint), not only `search` and `ask`; use `--` to pass text that starts with dashes to `memory remember|add|forget|search` and `config set`. `memory search --json` no longer puts `--json` into the query.
- **`--flag=value` works like `--flag value`.** Before, it was accepted and silently ignored (`search --top-k=2` returned 10 results; `index set x --mcp=true` did nothing).
- `gleann search --help` shows the real `--top-k` default (10), and the saved `top_k` setting now applies to CLI commands (`--top-k` still wins).
- **`gleann mcp` in the default pure-Go build no longer advertises tools that need the code graph** (`gleann_graph_neighbors`, `gleann_impact`, `gleann_document_links`, `gleann_graph_stats`, `gleann_symbols_in_file`, `gleann_document_toc`); they could only return errors. They stay advertised when format-2 indexes (which have their graph in SQLite) are in use. `gleann_def` stays (it falls back to search).
- The generated AGENTS.md and SKILL.md list only the MCP tools of the default profile and say how to enable the full set (`gleann mcp --tools full`).
- **The text chunker splits text without sentence punctuation or spaces** (logs, minified text, CJK) at the size limit instead of producing one huge chunk; `。！？` end a sentence, and a single paragraph longer than the chunk size is split by sentences. On gleann's own documentation the passage count changes from 662 to 663.
- **`index sync` no longer deletes the only good copy of an index after a killed rebuild.** A leftover `<index>.sync-bak` is restored (or discarded when the index is complete), and a cross-process lock keeps a second gleann away from an index that is being rebuilt. `gleann index list` and `gleann doctor` report a pending rollback copy.
- **`index watch` updates go through the same path as `index sync`**: the recorded embedding model is enforced (before, watch could mix vectors of another model into the index) and a failed rebuild is rolled back.
- `gleann graph report` prints the absolute path of the report and does not overwrite a `GRAPH_REPORT.md` that belongs to another index unless `--force` is given.

### Changed
- **Memory injection has a token budget.** The `<memory_context>` window is capped at 4000 estimated tokens by default (`GLEANN_MEMORY_MAX_TOKENS`, `0` = unlimited; manager setting `MaxContextTokens`). A typical memory set stays under the cap and renders unchanged; over budget, whole blocks are dropped lowest-priority first (short-term, then medium, then long-term; within a tier the older, then the less-accessed block), never truncated. `gleann memory context --json|--verbose`, REST `GET /api/blocks/context` and MCP `memory_context` `verbose` report the injected and dropped blocks with token estimates, and drops are logged.
- **Memory search is ranked.** Queries are tokenised (camelCase and snake_case aware) and scored with BM25 over label, content, tags, paths and symbols, so word order no longer matters; exact phrase matches still rank first and ties go to the newer block. `memory forget` (CLI, MCP, REST) still deletes only verbatim matches.
- `docs/evaluation.md` and `gleann bench --agent` state that the agent suite is a simulated scenario with assumed numbers, not a measurement.

### Added
- **A search does not wait behind a build in the in-process `eif` embedder.** Calls of up to 4 texts (a search query) run on a second model instance, loaded on first use under its own lock, instead of queueing behind a build's batch on the primary instance; if the second instance cannot be loaded they take the old path, and closing the embedder frees both. Instances share the memory-mapped model file and add only their activation scratch. Measured with multilingual-e5-small (q8_0) on linux/amd64, 16 cores: embedding one query while a build batch runs takes 12 ms instead of 3.7 s (`BenchmarkEIFQueryDuringBuild`; set `GLEANN_TEST_EIF_MODEL` to run it and the lane tests). Not yet measured on arm64 or macOS.
- **`X-Gleann-Cover` for multi-index pass-through answers.** When the best passages overall come from one or two indexes, an index whose best passage is close to the overall best (vector distance within a margin, or BM25 score at least a share of the best and above a floor) keeps that passage, replacing the lowest-ranked extra passages of other indexes; the top-k and `X-Gleann-Max-Context-Chars` limits hold, no index loses its only passage, and the other passages keep their order. Off by default (`proxy_cover`); thresholds `proxy_cover_margin`, `proxy_cover_share`, `proxy_cover_floor` or the `X-Gleann-Cover-Margin|Share|Floor` headers. The defaults (0.1, 0.6, 1.0) are starting points and are not calibrated to a particular embedding model.
- **Pass-through proxy context intro is configurable and asks for the question's language.** The sentence before the retrieved passages comes from `proxy_context_intro` (or `GLEANN_PROXY_CONTEXT_INTRO`; the default is the former English text) and gleann always appends "Answer in the language of the user's question.", so documents and questions in different languages get an answer in the asker's language without language detection.
- **`X-Gleann-Hybrid-Alpha` on the OpenAI-compatible proxy.** A number from 0 to 1 sets the vector/lexical weight of the retrieval for that request in both proxy modes; `0` searches lexically only (no query embedding), which keeps working for multi-index answers (the cross-index ranking uses the BM25 scores then). A value that is not a number in range is ignored, like `X-Gleann-Top-K` and `X-Gleann-Min-Score`.
- **`GET /api/storage` reports the whole data folder.** Besides the index directory it returns `data_dir` (the gleann home, `GLEANN_CONFIG_DIR` or `~/.gleann`), `data_bytes` and `data_folders` (bytes per top-level entry, so model downloads show up). Only regular files count, a symlinked home is resolved, and the walk is cached for 60 s so polling does not re-read large model folders. New `gleann.MeasureDir`.
- **HNSW vectors can be stored as float16** (`hnsw_config.vector_dtype: f16` or `GLEANN_HNSW_DTYPE=f16` for new builds; default `f32`, unchanged). Distances are computed from the half-precision values directly, without a float32 copy in memory. On 100K × 768 synthetic vectors: index file 315 → 169 MB, memory after load 868 → 514 MB (in-RAM searcher) and 328 → 182 MB (memory-mapped), recall@10 unchanged, search p50 +2 % (RAM) / +10 % (mmap); retrieval-quality gate Dense MRR 0.297 → 0.296. A float16 index is written as HNSW file version 2, which older gleann versions cannot read; float32 indexes keep version 1.
- **DiskANN maps its vectors instead of reading them into RAM; searcher caches can have a memory budget** (WP-2.7). `diskann_config.use_mmap` defaulted to true but was ignored; DiskANN (the default backend) now memory-maps the raw vectors and releases the mapping when the searcher closes. At 100K passages (format 2): first `serve` query 338 → 200 ms, `serve` RSS 191 → 148 MB, warm latency unchanged. One top-level `use_mmap` (`--no-mmap`, `GLEANN_MMAP`) overrides every backend; unset, the per-backend keys decide (both default to mapped). `searcher_cache_budget` / `GLEANN_SEARCHER_CACHE_BUDGET` (e.g. `2GB`, default 0 = count limit only) evicts least-recently-used searchers in `serve` and the MCP server until their index files fit.
- **Sleep-time reflection as diff and persistent cursor** (WP-4.4): sleep-time engine cursor resumes from SQLite `memory_meta`, reflection writes route through `RememberBlock` without overwriting or deleting original blocks, and `gleann memory reflect [--dry-run] [--revert <run-id>]` runs on demand, tests proposed changes, or reverts added blocks.
- **Markdown export and import for memory** (`gleann memory export --dir <dir>`, `gleann memory import --dir <dir>`): lossless, idempotent round trip for memory blocks and conversation digests with full YAML front matter and Windows-safe file names.
- **Retrieval quality gate in CI** (`internal/quality`): builds an index of a pinned snapshot of the repository (one archive, 320 files) with a deterministic embedder and fails when file-level MRR or R@10 of BM25, dense or hybrid search drops more than 0.01 below the stored baseline on 141 tasks. `go test ./internal/quality -run TestRetrievalQuality -update` rewrites the baseline on purpose.
- **Docs ↔ code check** (`internal/doccheck`): fails when a REST route, `GLEANN_*` variable, MCP tool or CLI command is missing from the docs, or documented but gone from the code.
- `allowed_roots` setting (settings screens, web UI, `config.json`, `GLEANN_ALLOWED_ROOTS`).
- [RFC 003](docs/plans/003-storage.md) (draft): SQLite record store, persisted lexical index and in-memory code graph.

## [v1.9.0] — 2026-11-11

See [docs/releases/v1.9.0.md](docs/releases/v1.9.0.md).

### Added
- **Query expansion (`--expand`, HyDE).** Before searching, an LLM writes a short code snippet that would answer a natural-language question, in the index's dominant language, and the search uses the question plus the snippet. `search`/`ask --expand`, `--no-expand`, `--expand-model`; REST search and ask `"expand": true`; MCP `gleann_search` `expand`; settings `expand` and `expand_model`. On 121 questions about the gleann code base MRR rose from 0.485 to 0.629 and R@10 from 0.71 to 0.81 with qwen3.5:9b (~1.5–3 s per new query on a GPU). Off by default: a 3B model gained only 0.03.
- **Catalog reranker `ms-marco-minilm-l6`.** English cross-encoder, best for code: on English code questions MRR +0.07 (mMiniLMv2: +0.01). The published GGUF lacks the model's pooler layer, which makes its scores nearly constant; `gleann models download` adds it from the original checkpoint (~600 KB, HTTP range requests).
- **`gleann search --explain`.** Shows the most important segments of each result (~70 ms per result) and the BM25 weight of each query term.
- **MCP `gleann_search` `focus`.** Returns only the most relevant quarter of each result, ~4× fewer tokens.
- **`gleann ask --cite` and REST ask `"cite": true`.** Link each answer sentence to the context passage it came from and list the passages used (~100 ms, prompt unchanged).
- **Pass-through mode for the OpenAI-compatible proxy** (`proxy_mode: passthrough` setting or `GLEANN_PROXY_MODE`). The client's request goes to the LLM server unchanged except for the retrieved context, and the reply streams back byte for byte (reasoning, metrics, extra fields); `POST /stop` is forwarded. Headers: `X-Gleann-Index`, `X-Gleann-Context: user` (keeps the LLM server's prompt cache), `X-Gleann-Max-Context-Chars`, `X-Gleann-Rerank`, `X-Gleann-Rerank-Pool`; the reply lists its sources in `X-Gleann-Sources`.
- **`GET /api/storage`** (admin): disk space of every index, index and passage counts, and the free space of the file system.
- Makefile: `ARM64_CFLAGS` for the arm64 eif cross build (e.g. `-march=armv8.2-a+dotprod+fp16`).
- `gleann bench`: `GLEANN_BENCH_PERTASK` writes per-task rows; `GLEANN_BENCH_RERANK=1` and `GLEANN_BENCH_EXPAND_MODEL` add reranker and HyDE strategies.
- [docs/retrieval-experiments.md](docs/retrieval-experiments.md): what was measured, what was shipped and what was left out, including the CPU cost of reranking.

### Changed
- **Built-in reranker defaults.** With no `rerank_model` set, results that are mostly source code are reranked with `ms-marco-minilm-l6` when it is downloaded (mMiniLMv2 otherwise and for documents), and the built-in engine scores 20 candidates of 500 characters instead of every candidate in full (~2.5× cheaper on a CPU, no loss on the benchmark). `--rerank-pool` and a configured model still win; server rerankers are unchanged.
- Catalog ids work as reranker models (`--rerank-model ms-marco-minilm-l6`), also when the engine is chosen automatically.
- **Identifier queries are not reranked.** One-word or code-shaped queries (`LeannSearcher`, `parse_args`, `func NewServer`) keep the first-stage order: passage rerankers demote exact symbol hits (symbol-query MRR −0.05 without this).
- `--rerank-pool` limits only the candidates sent to the reranker; the first stage keeps its wide lists (narrowing them lowered quality).
- **`rerank_provider: ollama` returns an error.** Ollama has no rerank API; the old fallback compared embeddings, scored worse than no reranking and crashed for bge-reranker-v2-m3. Use `eif` or `llamacpp` with a llama-server `--reranking` (which now uses `/v1/rerank`).

### Fixed
- **E5 embedding models get their `passage: ` / `query: ` prefixes.** The prompt template settings were never used, so `multilingual-e5-small` (the Turkish-capable default) embedded text without the prefixes it is trained with and ranked relevant passages low. New indexes record the prefixes in their meta and every search, update, `--explain` and `--cite` uses them; existing indexes keep working unchanged (rebuild one to get the prefixes).
- **Results of several indexes are ranked by their raw relevance.** Each index normalises its own best passage to about 1, so merged multi-index results put every document's best guesses on top and could crowd out the passage that answers the question. The proxy, `MultiSearcher` and `SearchMultiple` now fuse the raw vector distances and BM25 scores; reranked results keep their cross-encoder scores.
- Index-building uploads (`POST /api/indexes/{name}/upload`, `index-path`, `update`) get the build timeout (`GLEANN_TIMEOUT_BUILD_S`, 10 min) instead of failing with 504 after 60 s.
- `gleann ask --rerank` and `gleann ask --graph` had no effect unless saved in the settings, and multi-index or `<name>-code`/`<name>-docs` asks and multi-index `search --rerank` never reranked.
- `gleann ask` put the values of `--rerank-url`, `--rerank-pool` and `--rerank-provider` into the question.
- REST `ask` searched a second time to report sources, which could differ from the passages the answer was based on.
- `gleann bench` used the configured embedding model instead of the index's; a mismatch silently turned every strategy into BM25.

## [v1.8.13] — 2026-11-07

See [docs/releases/v1.8.13.md](docs/releases/v1.8.13.md).

### Fixed
- CI: removed an unused field of the TUI settings screen that failed `staticcheck` (U1000). No change in behaviour.

## [v1.8.12] — 2026-11-07

### Added
- **One settings list for the web UI and the TUI.** gleann's own settings are described once with `modules/settings` (`internal/tui.Settings`): the web UI renders them from `GET/PUT /api/settings` (System → Settings) and the TUI from the same list (`gleann setup` → All Settings), with the same fields, options, defaults, help and validation. A test fails when a `config.json` key has no setting. Saving in the web UI applies to the running server without a restart (except providers, address and services).
- **Reranker engine setting** (`rerank_provider`: `auto`, `eif`, `ollama`, `llamacpp`, `jina`, `cohere`, `voyage`) and `--rerank-provider`, `--no-rerank`. `auto` uses the built-in bitllm.c cross-encoder when the build has it and the model is a local GGUF.
- Catalog: `mmarco-mminilmv2-l12` (multilingual cross-encoder, the recommended default) and catalog notes that say when to pick a model.
- Vector storage mode (`storage_mode`) in the settings of both screens.
- Web UI: the Indexes page shows each index's vector storage and switches it (`POST /api/indexes/{name}/storage`, a background task), like the TUI Index Manager. `GET /api/indexes` returns `storage` per index.
- `auth`, `cors_origins`, `rate_limit` and `rate_burst` in `config.json` are read (they were ignored; only the environment variables worked) and editable in both settings screens. The environment variables still win.

### Changed
- Every reranker (CLI, REST, MCP, TUI chat) is resolved by `gleann.ResolveRerankerConfig`; before, the engine followed the embedding provider and the TUI chat always used Ollama.
- `rerank_enabled` now applies to CLI `search` and `ask` as it already did to REST and MCP.
- The catalog's `bge-reranker-v2-m3` is the Q8_0 file (the Q4_K_M one could not run in the built-in engine).
- The web UI's hand-written configuration panel is replaced by the settings form; its unused "auto-indexing" switch (`auto_index`, read by nothing) is gone.

### Fixed
- Reranker settings saved in the web UI were lost on restart (they were written to `search_config`, which no command reads); they are now stored in `rerank_enabled` / `rerank_model` / `rerank_provider` like the TUI's.
- The setup wizard dropped the saved repeat penalty, and stored a catalog reranker as its "(Download) …" label instead of the file name.
- A cross-encoder (bge-reranker, mmarco) is no longer offered as an embedding model.

See [docs/releases/v1.8.12.md](docs/releases/v1.8.12.md). Releases v1.8.8 to v1.8.11 are
described only in `docs/releases/`.

### Fixed
- The catalog's `multilingual-e5-small` GGUF had the wrong tokenizer; it is replaced by a correct conversion. Rebuild indexes made with it.
- Loopback clients are no longer rate-limited; proxied traffic still is.
- `gleann llamacpp` status lines go to stderr, so `--json` output stays valid.

### Added
- In-process cross-encoder reranking with bitllm.c (`reranker: eif`); before, reranking was skipped for the `eif` provider.
- `rerank_pool` in REST search.
- Embedded llama-server for `linux-arm64`.

### Changed
- Embedded llama-server updated from llama.cpp b4898 to b12176; readiness waits for `/health`, `--mlock` is no longer passed.
- The full Docker image installs `libgomp1` and `libssl3` for the embedded llama-server.

## [v1.8.7] — 2026-10-19

### Added
- **Chat settings in the agenttui interface** (`/settings`, Ctrl+S): model, temperature (slider), repeat penalty, max tokens, top-k, reranker and model, role and system prompt, with visible limits and hints. `/history` resumes a saved conversation; `/clear` and sources-first answers; the header, sidebar and welcome screen are gleann-specific.
- **`gleann chat` runs on the agenttui interface by default** (cancellable streaming, collapsible reasoning, source cards, slash-command popup, `@file` and `@symbol` completion). `--classic` (or `GLEANN_CHAT_UI=classic`) selects the old interface; `--modular` is still accepted and does nothing.
- **Index and tag picker in chat.** `/index` opens a panel with one switch per index and per tag (`gleann index tag`); ticking a tag selects every index that carries it. Also `/index a,b`, `/index #tag`, `/index add|remove <name>`, `/index info`. The selection really changes what is retrieved (a new searcher is swapped in and the reranker is restored); before, `/index` only changed the label.
- **Retrieval sidebar** lists every index with passage count, graph marker and tags, which ones are searched, and how many passages of the last answer came from each.
- `@symbol` completion works over a multi-index search (first index with a code graph); sources of a multi-index answer show which index they came from.
- `/forget` asks for confirmation before deleting memories.
- `gleann.ChatContextWindow` asks the provider for the model's context window (Ollama `num_ctx` sent / Modelfile / loaded model, OpenAI-compatible `/models` fields). `GLEANN_CONTEXT_TOKENS` overrides it.

### Changed
- The `ext/eif-runtime` submodule is replaced by `ext/bitllm.c` (updated to `cef6d8a`); the embedded web UI is rebuilt (provider select fix). Requires `git submodule update --init --recursive`.
- The chat no longer guesses the context window from the model name; when the provider does not report one the gauge shows used tokens only.

## [v1.8.6] — 2026-10-15

### Added
- **Optional Ollama API key (`ollama_api_key` / `OLLAMA_API_KEY`)** for Ollama behind an auth proxy or hosted Ollama endpoints:
  - New config field `ollama_api_key`; when set (or `OLLAMA_API_KEY` is exported) every Ollama request carries `Authorization: Bearer <key>` — chat, streaming chat, embeddings, Ollama reranker, `/api/tags` model listing (TUI, Web UI proxy, `gleann doctor`, `gleann config validate`), multimodal (`/api/show`, `/api/chat`), auto-setup detection and `/api/pull`, conversation/memory summarizers.
  - CLI: `gleann config set ollama_api_key <key>` (aliases `ollama-api-key`, `ollama_key`); `--ollama-api-key` flag for build/search/ask/chat/serve/multimodal and `gleann setup --bootstrap|--auto`.
  - Setup wizard: optional masked "Ollama API Key" step after the Ollama URL (embedding and LLM; one shared value, Enter skips); shown masked in the summary and flagged as `(key)` in the settings menu.
  - Web UI: the API Key field is enabled for the `ollama` provider (bound to `ollama_api_key`, placeholder "Optional") and model listing passes the provider-specific key. Requires a UI rebuild (`internal/server/dist`).
  - `gleann doctor` reports "API key configured" / "no API key" for Ollama and hints when Ollama answers 401/403.
- **`gleann index repair <name>|--all [--check] [--force]`** checks an index for damage and rebuilds its vector index from the passages it already stores (no source files needed; embeddings come from the cache where the texts are unchanged). It repairs indexes that older versions left damaged by in-place updates (see Fixed), a missing vector file and a stale passage count. `--check` only reports and exits 1 when an index needs repair; `--force` rebuilds even when the check finds nothing. `gleann doctor` now flags such indexes under "Indexes" and says which command fixes them.
- **Update statistics.** `LeannBuilder.UpdateIndexWithStats` reports how an update was applied (`in-place`, `compact`, `emptied`, `noop`) and what it cost: passages added/removed, texts embedded, embedding time, total time. `gleann index sync` prints it (`↳ vector index compacted: -2 +1 passages (now 6), 6 text(s) re-embedded in 120ms`) and returns it in `SyncResult.Update`; `gleann index repair` reports passages, texts embedded and the embedding cache hit rate; `POST /api/indexes/{name}/update` returns `mode`, `passages`, `removed` and `embedded_texts`.
- **Searching a damaged index warns.** Loading an index whose passage IDs have gaps (see Fixed) logs a warning that names the fix (`gleann index repair <name>`), `LeannSearcher.Warnings()` returns it, `POST /api/indexes/{name}/search` includes it as `warnings`, and the MCP search tool prints it above the results so an agent does not trust them blindly. The check reads only the passage store's key count and last key, so it costs nothing per load.
- `gleann index repair` shows progress while passages are re-embedded (`--progress plain|json|none`, a line every couple of seconds; a warm embedding cache finishes before the first one).
- `gleann doctor` summarises every index on one line: passages, backend, embedding model, dimensions (or "lexical-only") and size on disk.
- **New `/metrics`**: `gleann_update_requests_total{mode}`, `gleann_update_errors_total`, `gleann_update_latency_avg_ms`, `gleann_update_embedded_texts_total`.

### Changed
- `RemoveVectors` is gone from the vector backend interfaces (`gleann.BackendBuilder`, `modules/hnsw`, `modules/diskann`, `pkg/backends`, FAISS). Removing vectors in place is what damaged indexes: vectors are addressed by position, so removing one moves every later vector onto the wrong passage. Removals now go through `UpdateIndex`, which rebuilds from the remaining passages. Out-of-tree backends that implement `RemoveVectors` can simply drop it.

### Fixed
- Embedding progress was never reported during an index build: `embedding.ProgressCallback` was a defined func type, so the embedders' `SetProgressCallback` did not satisfy the `SetProgressCallback(func(int, int))` interface the index builder looks for. It is an alias now, so `--progress` and the MCP build progress see embedding batches.
- The API key is now selected per provider: an `openai_api_key` is no longer sent to Ollama endpoints (CLI ask/chat, MCP ask/batch_ask, TUI chat, server ask/search/proxy, embeddings, reranker).
- `gleann config show` and `gleann config set` mask API keys (Ollama, OpenAI, Anthropic) instead of printing them in clear text.
- `--api-key` value is no longer mistaken for the question/index name in `gleann ask`.
- `gleann install --user` no longer falls back to `sudo` (and fails) when the target directory does not exist yet, e.g. a fresh home without `~/.local`.
- **`gleann install --user/--system` installed an unusable binary.** Run through the self-extracting `gleann-full` launcher, it copied the extracted `gleann-full-bin` (the `--system` path copied only `libfaiss*` next to it), which fails with `error while loading shared libraries: libgleann_core_rs.so`. The launcher now exports `GLEANN_LAUNCHER` and install copies that self-contained file instead; without it the previous behaviour stays and the sudo path copies every bundled library. `gleann help` now lists `install`/`uninstall`.
- `DELETE /api/indexes/{name}` no longer fails on Windows while searches still hold the index open (it retries for up to 5 s, dropping any re-loaded searcher).
- **Incremental index updates no longer corrupt the index.** Removing passages in place (a changed, deleted or renamed file in `gleann index sync`/watch, `UpdateIndex`, `Reconcile`, memory-entity delete) left permanent gaps in the passage IDs while the vector backends address a vector by position, so vectors ended up attached to the wrong passages: searches returned the wrong text or far fewer results than a fresh build. `UpdateIndex` now appends in place only when IDs are dense and the backend can take the add; otherwise it rebuilds the index from the remaining passages plus the new items (embeddings are computed before any file changes, so a failure leaves the index untouched). This also repairs indexes already left with gaps by older versions on their next update. Removing the last passage empties the index; a later add builds it afresh.
- `gleann index sync` edited the vector file with the backend named by the caller's flag/default instead of the one the index was built with (any sync touching a file failed with `backend "" not registered`, or silently rewrote the index with another backend); it now uses the index's own backend.
- A full rebuild no longer drops an index's tags, description, MCP exposure, auto-watch flag and creation time.
- An HNSW index built with pruned embeddings is no longer edited in place (it cannot be); the update compacts instead.
- **`gleann install claude` installed every detected platform.** A bare platform name was ignored, so auto-detection ran and also configured gemini, copilot, etc. `gleann install <name>` now means `--platform <name>`.
- **`gleann install` for `claude` and `gemini` wrote into the current directory.** They now write the user-level `~/.claude/CLAUDE.md` and `~/.gemini/GEMINI.md` (plus the hook settings next to them); `--project` or `--dir` keeps the old project-level behaviour. `uninstall` follows the same rule.
- **Graph endpoints reported a missing index on builds without graph support.** On a binary built without `-tags treesitter` (pure-Go and EIF-only builds, including ARM64), graph requests answered `404 graph index "x" not found: graph database requires CGo ...`. They now answer `503` with a message that says the binary has no graph support and points to the full build.
- **Web UI could not show or keep the EIF provider.** The embedding and LLM provider selects had no `eif` option, so an `eif` config rendered as "Ollama" and picking a model from that list switched the active model to an Ollama one. The selects now offer `EIF (Embedded GGUF)` with the local GGUF picker, and show any provider they do not know instead of silently displaying the first option. Requires a UI rebuild (`internal/server/dist`).
- **False "Mismatch / rebuild" warnings in the Web UI.** The index list compared an index's model only against the general embedding model and by exact string. It now accepts the code and docs models (`current_embedding_models` in `GET /api/indexes`) and compares file names, as the searcher does.

## [v1.8.5] — 2026-10-14

### Added
- **Table-Aware Markdown Chunker (`modules/chunking`)**:
  - Implemented atomic markdown table detection and preservation during document chunking. Tables under the token limit remain intact in a single chunk with their headers.
  - Large tables exceeding chunk size are partitioned with replicated header rows across every sub-chunk, ensuring continuous tabular context for embedding and RAG.
  - Fixed slice bounds out-of-range regression (`[len:len-1]`) in heading parser.
- **Plugin Priority & Capability Routing (`pkg/gleann/plugin.go`)**:
  - Upgraded document extractor discovery to prioritize high-capability, native plugins (`Priority: 10`) over legacy fallbacks.
  - Full seamless integration with standalone native `gleann-plugin-docs` daemon.
- **Optional API Keys for OpenAI-compatible & Ollama endpoints**:
  - `Authorization: Bearer <key>` is sent only when a key is configured; empty keys are omitted so local/unauthenticated servers (Ollama, vLLM, LM Studio, LocalAI) work unchanged.
  - New `--api-key`, `--base-url` flags and `gleann config set api_key|base_url|ollama_host` aliases.
  - `gleann doctor` validates OpenAI-compatible and Anthropic providers without requiring `ollama pull`.
- **Single-step docs plugin install**: `gleann-plugin-docs` binary and Granite Docling BitNet model are fetched from GitHub release assets.

### Fixed
- Plugin install no longer fails with `'NoneType' object is not iterable` when `plugins.json` contains `{"plugins": null}`; registry is always loaded/written with a non-nil list.
- `gleann-plugin-docs` is installed via the native Go pipeline (catalog metadata corrected from Python).
- eif-runtime diagnostics now go to stderr, keeping stdout clean for JSON/pipes.

### Changed
- **Default LLM Model Standardization (`llama3.2:3b`)**:
  - Centralized default chat model to `llama3.2:3b` across CLI defaults (`cmd/gleann/defaults.go`), core chat engine (`pkg/gleann/chat.go`), and system diagnostics (`gleann doctor`).
  - `gleann doctor` checks now verify local LLM availability cleanly against configured providers.

### Documentation & Maintenance
- **Documentation Audit & Streamlining**:
  - Consolidated scattered integration test plans into `docs/TEST_PLAN.md` and removed redundant ephemeral test reports.
  - Synchronized plugin guides (`docs/plugins.md`, `docs/plugin-install-guide.md`) to reflect the standalone Go/C VLM architecture.
- **Refreshed Terminal Screencast**:
  - Updated VHS demo recording (`scripts/demo/gleann-demo.gif` & `scripts/demo/gleann-demo.mp4`) showcasing all-green system health, instant table indexing, and cross-session memory retrieval.

## [v1.8.4] — 2026-10-13

### Optimized
- **EIF-Runtime High-Performance Engine Upgrade (`ext/eif-runtime`)**:
  - Upgraded submodule to commit `bde2895` with full AVX2 / ARM NEON multi-tile vector GEMM kernels and Q8_0 4-token unrolled matrix multiplication.
  - Multi-Head Attention parallelism: uncapped thread allocation in `eif_bert_embed` and `bert_gemm_int8_gguf`, delivering up to 62% latency reduction across local GGUF models on multi-core CPUs.
  - Portable Binary Distribution: replaced host `-march=native` with portable `-mavx2 -mfma` architecture compilation, strictly preventing AVX-512 EVEX illegal instructions (`SIGILL`) on non-AVX512 host environments and Docker containers.
  - Host CPU Capability Pre-flight: added runtime probing in `internal/embedding` ensuring zero CGo illegal instruction panics on unsupported architectures.
  - Measured live inference latency across local backend models:
    - `snowflake-xs-q8_0`: 14.20 ms / embedding (62.4% faster)
    - `minilm.gguf`: 14.43 ms / embedding (59.9% faster)
    - `jina-code-q8_0`: 85.68 ms / embedding (39.5% faster)
    - `bge-m3-q8_0`: 237.98 ms / embedding (30.5% faster, down from 425 ms)
    - `bge-small-en-q8_0`: 40.08 ms / embedding (24.1% faster)
    - `granite-30m-q8_0`: 19.54 ms / embedding (13.7% faster)
  - Full semantic separation parity: preserved 100% mathematical vector clustering and code search accuracy.
  - In-process EIF embedding and causal LLM validation: verified with 100% pass rate in Gleann integration and end-to-end test suites.

### Added
- **Multi-Model Embedding Selection (Code & Docs)**:
  - Enabled dedicated code and documentation embedding model selection across TUI setup (`gleann setup` wizard and settings menu), CLI setup (`gleann config set code_model / doc_model`, `gleann setup --bootstrap`, `gleann setup --auto`), and Web UI (System settings and Directory indexing).
  - Seamless fallback to the primary embedding model when dedicated models are not specified.

## [v1.8.3] — 2026-10-10

### Added
- **Dual Embedding Model Architecture (Code + Docs Specialization)**:
  - Added `--code-model` and `--doc-model` (alias: `--docs-model`) CLI flags and `code_embedding_model` / `doc_embedding_model` configuration fields.
  - Automated dual-stage indexing: running `gleann index build <name> --docs <dir> --code-model <m1> --doc-model <m2>` automatically builds `<name>-code` and `<name>-docs` with their respective specialized models in a single command.
  - Automatic Dual-Index Expansion: querying `gleann search <name>` or `gleann ask <name>` transparently routes queries across both `<name>-code` and `<name>-docs` via `MultiSearcher`.
- **Per-Index Dynamic Embedder Resolution (`EmbedderResolver`)**:
  - Implemented `EmbedderResolver` across `pkg/gleann`, `internal/embedding`, CLI, and REST server.
  - In multi-index search, queries against each index are dynamically embedded using that index's native model (`meta.EmbeddingModel`), completely eliminating latent vector space mismatch and dimension mismatch panics across indices with different dimensions (e.g., 768-dim Jina Code + 384-dim BGE Small).
- **Web UI & REST API Dual Model Settings**:
  - Added dedicated Code Embedding Model and Docs Embedding Model fields to Web UI System Settings (`ui/src/components/System.tsx`).
  - Updated `/api/config` GET/POST endpoints and server searcher cache to support multi-model indexes without false model mismatch errors.
- **In-Process EIF-Runtime LLM & ALiBi Support**:
  - Submodule updated to latest `ext/eif-runtime` with ALiBi double LayerNorm support, enabling Jina Embeddings v2 Base Code (~93 ms/emb on CPU).
  - Added C99 in-process LLM generation and real-time token streaming callbacks (`pkg/gleann/chat_eif.go`).

### Fixed
- **CI/CD Toolchain Compatibility**:
  - Added `GOTOOLCHAIN=auto` across `.github/workflows/ci.yml` and `.github/workflows/release.yml` to prevent Go toolchain version mismatch failures.
  - Cleaned up mock structs in MCP token budget tests, passing 100% of `staticcheck`, `go vet`, and race-detector test suites.

## [v1.8.2] — 2026-10-07

### Added
- **In-Process EIF-Runtime Integration**:
  - Embedded high-performance C99 `eif-runtime` engine as a Git submodule (`ext/eif-runtime`) compiled directly into `gleann-full` standalone binaries.
  - Zero-heap, zero-copy `mmap` CPU inference achieving 450+ embeddings/sec for MiniLM and 90+ embeddings/sec for 12-layer models.
  - Added native EIF provider support (`ProviderEIF = "eif"`) across TUI onboarding wizard, model scanner, CLI indexing, and Go embedding computers.
  - Supported direct loading of both `.eifm` binary models and GGUF (`.gguf`, `_q8_0.gguf`) BERT-family models without external Python or server dependencies.
- **Multimodal Chat & Screenshot Capture**:
  - Added `/screenshot` (and `/shot`, `/paste-image`) slash commands to interactive TUI chat.
  - Automatic clipboard image capture via `wl-paste` (Wayland), `xclip` (X11), `pngpaste` (macOS), or PowerShell (Windows) with automatic screen capture fallback.
  - Queued images are formatted and sent seamlessly alongside user prompts to vision/multimodal models (Ollama, OpenAI, Anthropic).
- **Repetition Penalty Configuration**:
  - Added configurable repetition penalty across CLI flags (`--repeat-penalty`, `--repetition-penalty`), environment variables (`GLEANN_REPEAT_PENALTY`), TUI settings slider, and interactive slash commands (`/repeat <val>`, `/penalty <val>`).
  - Seamlessly propagated across Ollama (`options.repeat_penalty`) and OpenAI/llama.cpp APIs.

### Fixed
- **EIF-Runtime BERT Engine Robustness**:
  - Added support for quantized `Q8_0` position embeddings (`position_embd.weight`) in GGUF models with on-the-fly FP32 dequantization, fixing NaN generation in models such as Multilingual-E5.
  - Added dynamic vocabulary hash table allocation (up to 512K slots) to support large multilingual models (250K+ tokens) without collision drops.
  - Added detection and mapping for SentencePiece / LLaMA special tokens (`<s>`, `</s>`, `<unk>`, `<pad>`) via GGUF metadata (`tokenizer.ggml.bos_token_id` / `eos_token_id`).
- **TUI Settings Alignment**:
  - Positioned Repetition Penalty slider directly below Temperature in TUI Settings menu for intuitive keyboard navigation.

## [v1.8.1] — 2026-10-06

### Fixed
- **Embedded Llama.cpp Runner & Inference**:
  - Automatically configured `--embedding` flag when spawning embedded `llama-server` process to enable native embedding inference.
  - Tuned default CPU batch size (`32`) and concurrency (`2`) for local llama.cpp embeddings to prevent memory thrashing and CPU thread contention.
  - Added robust response parsing for OpenAI-compatible embedding servers returning all zero indices (e.g. `llama-server`).
  - Unified base URL routing and API key propagation across CLI commands, REST server, MCP server, and interactive TUI chat.
- **Test Suite Isolation**:
  - Fixed test cleanup in MCP remote routing tests to prevent race conditions during full concurrent test runs.
  - Consolidated embedder computer initialization (`newEmbedder`) across `cmd/gleann` commands, eliminating duplicate code and unused imports.

## [v1.8.0] — 2026-09-25

### Added
- **Tiered Persistent Agent Memory**: Multi-tiered memory architecture (`short`, `medium`, `long`) backed by embedded BoltDB with auto-scope hierarchies (`project:{name}`, `session:{id}`, `global`), TTL expiration, and tier promotion.
- **Sleep-Time Engine**: Background reflection daemon that extracts entities and relationships from agent conversations and manages memory compaction.
- **Unified Memory API**: `/api/memory/recall` and `/api/memory/ingest` combining block memory, KùzuDB knowledge graph traversal, and vector search.
- **Index Governance & Target Isolation**:
  - Strict client target isolation across all REST API handlers (`/api/indexes/{name}/*`, `/api/search`, `/api/memory/*`, `/api/blocks/*`) using `X-Gleann-Target` / `X-Gleann-Index` headers and `?target=` parameters.
  - Automatic exclusion of private indexes (`mcp_exposed: false`) and untagged indexes from multi-search unless explicitly authorized.
  - Tag-based governance via `GLEANN_TAGS` and federated `@tag` search expansion.
- **Google A2A Protocol**: Agent discovery (`/.well-known/agent.json`), JSON-RPC message delivery, and targeted skills with parameter isolation.
- **MCP Tool Profiles**: Configurable profiles (`minimal`, `standard`, `full`, `memory-only`) via `--tools` flag.
- **Incremental Synchronization (`gleann_sync`)**: MCP and CLI tool to incrementally update both vector passages and AST knowledge graphs on code changes.

### Fixed
- **Client Target Isolation in Memory Blocks**: Enforced `?target=` parameter alias for `?scope=` in `/api/blocks/context`, `/api/blocks/search`, `/api/blocks`, and `/api/blocks` POST with mismatch rejection (403 Forbidden).
- **Cross-Platform Compatibility**: Fixed Windows file path delimiter issues (`filepath.Join` vs URL paths), macOS symlink resolution in file indexing, and BoltDB timeout handling on Linux.
- **AST and Treesitter Robustness**: Enhanced language parsers, FQN symbol matching in KùzuDB, and markdown/doc comment sanitization.

---

## [v1.1.0] — 2026-09-14

### Added
- **Web UI Architecture**: Completely modularized React frontend into clean, maintainable components (`ChatView`, `GraphView`, `MemoryView`, `TasksView`, `IndexesView`, `SettingsModal`).
- **Memory Scope Generics**: Clean generic hierarchical scoping patterns across memory engine and documentation.

### Fixed
- **Windows CI & Path Compatibility**: Resolved `models_handler_test` failure on Windows by honoring `USERPROFILE` path resolution.
- **Race Condition Handling**: Disabled Windows `-race` flag in CI to prevent TSAN crashes with CGO.
- **Graph CSV Import Crash**: Resolved STRING→DOUBLE cast errors in Kuzu DB caused by special characters (`"`, `,`, `\`, `\n`) in doc comments (added `sanitizeCSVField()`).
- **TUI Test Timeout**: Resolved 200+ TUI tests exceeding 90s timeout by introducing `TestMain` and dedicated test mode.

### Stats
- **Coverage**: %59.6 total statements (26 packages, 0 fail)
- **Graph Index**: 8080 nodes, 22037 edges, 789 communities
- **Unit Tests**: Gleann Core: **26 packages**, all passing

---

## [v1.0.0] — 2026-06-28

### Added
- **A2A Protocol** (Google Agent-to-Agent standard)
  - `/.well-known/agent-card.json` agent discovery endpoint
  - `/a2a/v1/message:send` task submission
  - `/a2a/v1/tasks/{id}` task status polling
  - 8 skill exposed (Semantic Search, RAG Q&A, Code Graph, Memory, Community Detection, Repo Map, Risk Analysis, Multimodal)
- **Memory Engine** — Hierarchical path-style scope with ancestor visibility
- **Context Field Theory (Φ Scoring)** — MCP search re-ranking with recency decay, frequency, graph proximity, degree centrality
- **10 File Read Modes** — `map`, `signatures`, `entropy`, `diff`, `task`, `reference`, `aggressive`, `lines:N`, `auto`, `full`
- **Shell Output Compression** — 95+ tool-specific regex patterns
- **17 Agent Platform Support** — `gleann install` auto-configures OpenCode, Claude Code, Cursor, Codex, Gemini CLI, Windsurf, Cline/Roo, Amp, Kiro, Amazon Q, Continue, Zed, Neovim, JetBrains, OpenClaw, Aider, GitHub Copilot CLI
- **Token Gain Tracking** — `gleann_gain` MCP tool for cumulative session savings
- **Embedding Cache** — Two-tier (L1: otter in-memory ≤50k; L2: disk keyed by SHA-256)

### Changed
- Centralized LLM model defaults, removed hardcoded llama3.2 references
- Standardized plugin extraction benchmarks
- Unified Rust/Candle native embedding engine build system
- Consolidated commands, reduced cyclomatic complexity

### Security
- Bumped Go to 1.25.9, added SBOM, hardened code
- SSRF-safe webhooks, request body cap, configurable task auto-eviction
- Fixed 7 stability bugs (memory lock, Kuzu corruption, dedupe, num_ctx, timeouts, multimodal, TUI-TTY)

### Performance
- Single tree-sitter parse + content-hash cache + language-aware weights
- DiskANN backend with optional FAISS CGo acceleration
- VectorSyncer bridge + IMPLEMENTS/REFERENCES edge extraction

---

## Historical Highlights

| Commit | Description |
|--------|-------------|
| `d518721` | TUI sandbox test mode (precursor to TestMain fix) |
| `6d19e52` | 7 stability bug fixes (memory lock, kuzu corruption, etc.) |
| `73285ce` | Shell compression, 10 read modes, Context Field Theory |
| `b2e1b55` | Native embedding engine (Rust/Candle) integration |
| `e3f450a` | Go 1.25.9 bump, SBOM, security hardening |

---

## Versioning

Gleann follows [Semantic Versioning](https://semver.org/):
- **Major** — Breaking API/config changes
- **Minor** — New features (backward compatible)
- **Patch** — Bug fixes and performance improvements
