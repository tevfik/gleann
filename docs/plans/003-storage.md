# RFC 003: Storage Engine — SQLite Record Store, Persisted Lexical Index, Code Graph in SQL

* **Status:** Approved by the maintainer 2026-11-16, including the two post-audit choices in 9.16 (graph through SQL and recursive CTEs only, no CSR; embedding cache kept as per-vector files in R1). Audited 2026-11-16 (section 10). Implementation follows section 8; the other open questions in section 9 are settled by the measurement or step named there.
* **v1.20 note (2026-11-17):** superseded in part by [RFC 004](./004-v1.20-sqlite-only.md). Format 2 is the only index format as of v1.20.0; there was no bridge release: steps 8 and 9 (Kùzu behind `-tags kuzu`, then removal) collapsed into one, Kùzu and the format-1 read paths are removed in v1.20.0, `gleann index migrate` is the only reader of format 1 (kept for v1.20.x), and memory entities of old Kùzu stores are carried over with `kuzu-memory-export` + `gleann memory import-graph` (question 11). See [the decision record](../decisions/kuzu-to-sqlite.md).
* **Related RFCs:** [001](./001-code-graph-rebuild.md) (graph schema and extraction), [002](./002-retrieval-quality.md) (retrieval stages that sit on top of the lexical leg)
* **Evidence:** storage decision record D4 and the spike code in `gleann-rag-test/bench/sqlbench` (`main.go`, `spike/main.go`, `drivers/main.go`). Every number below comes from those measurements; where something was not measured, the text says so.
* **Code references** are to commit 05863e4 (v1.9.0).

---

## 1. Problem

gleann keeps its data in several unrelated stores. Three weaknesses follow from that.

| # | Weakness | Evidence in code | Measured effect |
|---|---|---|---|
| W2 | Editing one file rewrites the whole index. Passage IDs are a bbolt sequence and double as vector positions, so a passage cannot be removed without renumbering. | `pkg/gleann/passage.go:106` (`NextSequence`), `pkg/gleann/builder.go:440-529` (`compact` reads every passage, re-embeds, rewrites passages and index), `pkg/gleann/index_health.go:73` (health check requires dense IDs because vectors are addressed by ID). A full build writes the embedding cache only after the whole `Compute` returns (`internal/embedding/cache.go:128-140`), one file per vector (`cache.go:206-219`). | Not measured here; the vector side belongs to WP-2.2. This RFC provides the stable IDs and per-file transactions that WP-2.2 and WP-2.3 need. |
| W3 | BM25 is rebuilt in RAM on every load and scans all documents per query. Above `MaxBM25Passages` only the first N passages are searchable. | `pkg/gleann/searcher.go:194-231` (tokenise the corpus on `Load`), `modules/bm25/bm25.go:111` (`Score` loops over all documents), `pkg/gleann/types.go:442-446` (silent cut). | BM25 at 100K chunks: 4.4 s on every load, 493 MB heap, p50 64 ms / p95 92 ms. At 500K: 26.7 s, 2,492 MB, p50 489 ms / p95 944 ms. |
| W5 | Memory is a bbolt file with substring search; several processes share it only through a daemon. | `pkg/memory/store.go:57` (bbolt, 5 s lock timeout), `store.go:288` (`strings.Contains`), `pkg/memory/remote.go:3-9` (a running `gleann serve` holds the exclusive lock, other processes route over REST). | Not measured. The storage part of W5 is multi-process access; recall quality is WP-4.1. |

Two related findings shape the design.

* **Graph store.** The code graph lives in Kùzu, which needs `-tags treesitter` (cgo). The recommended `go install` build prints that graph commands need CGo (`cmd/gleann/graph_stub.go`). `go-kuzu v0.11.3` is pinned (`go.mod:16`); the review roadmap records the project as archived (a web claim, not re-verified here). Kùzu itself was not benchmarked.
* **Ad-hoc stores.** The inventory in section 6.1 lists every file format in use. There are three bbolt users outside the passage store, one Kùzu database per index plus one per memory namespace, one file per cached vector, and several JSON files.

### 1.1 What the measurements say

All measurements: i9-13900H, Go 1.24.2 (the repository now declares `go 1.25.9`, `go.mod:3`; the spike was not re-run on it), `CGO_ENABLED=0`, `modernc.org/sqlite` v1.62.0 (SQLite 3.54.1), single process.

| Lexical engine | N | Build / load | Disk | Go heap | Cold open + first query | p50 | p95 |
|---|---|---|---|---|---|---|---|
| gleann `modules/bm25` | 100K | 4.4 s (every load) | none | 493 MB | = load | 64 ms | 92 ms |
| gleann `modules/bm25` | 500K | 26.7 s (every load) | none | 2,492 MB | = load | 489 ms | 944 ms |
| FTS5 `unicode61` | 100K | 3.4 s (once) | 172 MB | ~1 MB | 2.2 ms | 17 ms | 41 ms |
| FTS5 `unicode61` | 500K | 27.6 s (once) | 841 MB | ~1 MB | 23.5 ms | 146 ms | 356 ms |
| FTS5 `trigram` | 100K | 26.2 s | 424 MB | ~1 MB | 9.8 ms | 58 ms | 177 ms |
| FTS5, contentless, gleann tokenizer, commits of 2,000 rows | 100K | 2.64 s | 35 MB | 28 MB process | 44 ms | 17 ms | 44 ms |
| FTS5, contentless, gleann tokenizer | 1M | 31.5 s | 324 MB | 33 MB peak RSS | 468 ms | 180 ms | 473 ms |

Corpus: 5,199 real 40-line chunks of gleann's `.go` and `.md` files replicated to 100K and 500K; for 1M, about 5K real chunks replicated, so every term is about 190 times more frequent than in a natural corpus of that size. That is a pessimistic case for OR queries. 50 queries (identifiers and phrases), top 10.

| Graph engine (200K nodes, 1M edges) | Load / build | Disk | Heap | Direct callers p50 | Callers to depth 3 p50 / p95 |
|---|---|---|---|---|---|
| In-memory CSR, pure Go | 14 ms | 8 MB | 5 MB | 36 ns | 8.6 µs / 19 µs |
| SQLite recursive CTE | 1.9 s | 37 MB | ~0 | 18 µs | 388 µs / 735 µs |

Several processes (one writer at 100 Hz plus one reader for 3 s): 164 writes, 674K reads, 0 busy or locked errors. Four processes, `busy_timeout` 10 s, one transaction holding the write lock for 3 s: 0 errors, 0 lost rows, readers never blocked (slowest read 1 ms), but small writers waited up to 3.0 s and 4.6 s.

The graph table shows the trade-off behind the graph decision in 2.1: the in-memory CSR is about 45 times faster at depth 3 (8.6 µs against 388 µs p50), but the recursive CTE is already well below a millisecond at 1M edges, needs no load step in a second process, and cannot go stale between processes. The CSR numbers stay here so the cost of choosing SQL only is visible; see 3.7 for when a CSR would be added.

**Measured afterwards (orchestrator, 2026-11-16, modernc.org/sqlite v1.62.0, SQLite 3.54.1):**

* `SELECT ... WHERE lex MATCH ''` fails with `fts5: syntax error near ""`. An empty query must never reach FTS5 (4.2).
* With `contentless_delete = 1`, `DELETE FROM lex WHERE rowid = ?` and `INSERT OR REPLACE INTO lex(rowid, tokens)` work: the deleted or replaced terms no longer match. Space reclaimed after many deletes and the speed of deletes at 100K+ rows were not measured.

**Not measured:** Kùzu itself; two concurrent writers beyond the test above; the ncruces driver beyond the first step; Windows and ARM64 at run time; modernc against C SQLite; the full hybrid `gleann bench` with FTS5 inside gleann; AND-first query construction; the document-frequency cut; `wal_checkpoint(PASSIVE)` timing; the Turkish rules applied per chunk kind (4.1).

---

## 2. Decision and alternatives

### 2.1 Decision

| Data | Engine |
|---|---|
| Passages, file manifest, metadata, memory blocks | SQLite in WAL mode through `modernc.org/sqlite` (pure Go) |
| Embedding cache | Release 1: the existing per-vector files (`<sha>.bin`), with writes moved to per batch (3.3). A global SQLite cache is a later option, not part of R1. |
| Lexical index | SQLite FTS5, contentless, fed with the output of gleann's own tokenizer |
| Vectors | gleann's own DiskANN and HNSW files, unchanged |
| Graph | `symbols` and `edges` tables in the same database as the index, queried with SQL and recursive CTEs. No in-memory CSR at first; one is added only if a measured query needs it (3.7). |
| Kùzu | Kept behind `-tags kuzu` for one release, then removed |
| Access | Two small interfaces (`store.Passages`, `store.Lexical`) so an engine can be replaced without touching callers |

The maintainer approved this direction on 2026-11-16 (D4, "Decision proposed: adopt now"). Two refinements from the audit (section 10) are drafted into this text as recommendations and **await the maintainer's confirmation**: SQL only for the graph (audit finding 12) and keeping the per-file embedding cache in R1 (finding 13). Everything below is written for those two choices; if the maintainer prefers the original CSR or the global cache, the affected places are 3.1, 3.3, 3.7, 6.2, 6.4, 6.5, 6.6 and step 5 and 7 of section 8.

### 2.2 Alternatives considered

| Option | Result | Reason |
|---|---|---|
| **modernc.org/sqlite** | Chosen | Pure Go, builds with `CGO_ENABLED=0` for linux, darwin and windows on amd64 and arm64 (compiled for all six in the spike, 9.9-11 MB each; run only on linux/amd64). FTS5 present in the default build. Measured numbers in 1.1. |
| ncruces/go-sqlite3 (SQLite as WASM) | Rejected | Tested locally: with `go-sqlite3-wasm/v6 v6.5.35401` the default build answers `no such module: fts5`. FTS5 would need a custom WASM build. A survey claim that it bundles FTS5 is wrong for the default build. |
| bbolt plus a purpose-built postings file | Fallback | Same asymptotic fix, more code to write and own (segments, merge, WAND). Kept as the plan if the reversal gates in 7.2 fail, behind the same interfaces. |
| Kùzu successor with cgo (LadybugDB or similar) | Rejected for now | Needs cgo and a shared library, so the graph stays absent from `go install`. Go binding and platform libraries not verified. |
| Bleve, Pebble, Badger | Ruled out | Single-process locking, per the (unverified) survey in `findings/gemini-db-alternatives.md`. Multi-process access is a requirement because agents write memory from several processes. |
| mattn/go-sqlite3, DuckDB, LMDB, tantivy | Ruled out as default | Need cgo. mattn/go-sqlite3 stays possible as the optional cgo tag in section 5. |
| HydraDB | Not pursued | Server process, Rust and C, Elastic License. Conflicts with the standalone-binary rule (AGENTS.md section 1). |

The survey is a Gemini Flash web search and is unverified. The only claim from it that was tested locally (ncruces and FTS5) was wrong.

Independent support: LEANN (the reference project) uses FTS5 as its only lexical backend (`api.py:336-349, 434-436, 689-701` at upstream 07796bd, read but not executed). It indexes raw text with `unicode61`; on gleann's tasks that variant measured worse than FTS5 fed with gleann's tokenizer (NL R@10 0.62 against 0.68), so gleann keeps its tokenizer in front.

### 2.3 What the spike showed about quality (gate 1, lexical leg only)

Corpus: gleann HEAD, 6,292 chunks of 40 lines. Tasks: `gleann_nl.json` (121) and `gleann.json` (20). File-level metrics.

| Engine | NL MRR / R@1 / R@10 | Symbol MRR / R@1 / R@10 | Index on disk |
|---|---|---|---|
| gleann `modules/bm25` (today) | 0.396 / 0.26 / 0.70 | 0.613 / 0.45 / 0.90 | none (RAM) |
| FTS5 `unicode61` on raw text | 0.367 / 0.26 / 0.62 | 0.696 / 0.60 / 0.85 | 12.8 MB |
| FTS5 fed with gleann's tokenizer | 0.397 / 0.28 / 0.68 | 0.633 / 0.50 / 0.90 | 12.0 MB |
| same, contentless | 0.397 / 0.28 / 0.68 | 0.633 / 0.50 / 0.90 | 4.5 MB (text is 7.3 MB) |

Paired over the 141 tasks, FTS5 with gleann's tokenizer against today: mean reciprocal-rank difference +0.004; 9 tasks better, 22 worse, 110 equal. Equal on average, more tasks moved down than up, by small amounts. This is inside what 141 tasks can resolve; it is not a proven tie. The hybrid pipeline was not run; it is the first gate in 7.2.

---

## 3. Architecture

### 3.1 Package layout

```
internal/store/            SQLite handle, pragmas, migrations, transactions
  store.go                   Open/Close, writer + reader pools, schema version
  passages.go                Passages implementation
  lexical.go                 Lexical implementation (FTS5)
  files.go                   file manifest rows
  legacy_bbolt.go            read-only reader of format-1 passages (removed in release 3)
internal/analysis/         tokenizer / analyzer (moved from modules/bm25.Tokenize; section 4)
internal/graphstore/       GraphStore interface, SQL and recursive-CTE implementation
  sqlite.go
  kuzu_adapter.go            build tag `kuzu`; removed in release 3
internal/memstore/         memory blocks and entity tables (global memory database)
```

Why inside the main module: until 2257ec7, `modules/bm25`, `modules/hnsw`, `modules/diskann` and `modules/chunking` were separate Go modules joined by `replace` directives, which broke `go install` of the root module. They are now packages of the root module; only `modules/settings` stays separate because the plugins import it. New code goes into `internal/` of the root module and must not reintroduce a nested module. Extraction can happen later if a second program needs it. The graph store is estimated at 500-800 lines (D4) and changes together with the indexer.

`pkg/gleann` already has the seams: `Scorer` and `TopKScorer` (`pkg/gleann/interfaces.go:119-131`) and `GraphDB` (`interfaces.go:217-232`). The lexical leg needs no change in the searcher call sites (`searcher.go:395-420`) if `store.Lexical` can be wrapped as a `TopKScorer`. That is why the first gate can run before the passage store changes (section 8, step 3).

One change is needed in `Searcher.Load`, though. Whenever a scorer is set, `Load` reads every passage and calls `scorer.AddDocuments` (`searcher.go:194-231`, streaming above 100,000 passages, capped by `MaxBM25Passages`). A persisted lexical index wrapped as a scorer would still pay that 4.4 s and 493 MB at 100K, which defeats the 44 ms cold open in 1.1. The scorer side therefore gets a small optional interface in `pkg/gleann/interfaces.go` next to `Scorer` (for example `PersistedScorer` with a method `Persisted() bool`); `Load` skips the passage scan when the scorer reports it is persisted, and `MaxBM25Passages` does not apply to it.

### 3.2 Interface sketches (signatures only)

```go
package store

// Passages is the record store for one index.
type Passages interface {
	// ApplySource replaces everything belonging to one source in one
	// transaction, whatever the number of passages: deletes its old passages,
	// inserts the new ones, updates the lexical rows and the file manifest
	// row. It returns the stable IDs.
	ApplySource(ctx context.Context, d SourceDelta) (ids []PassageID, err error)
	RemoveSource(ctx context.Context, source string) (removed int, err error)

	// ReassignSlots rewrites vec_slot for many passages in one transaction.
	// The vector builder calls it when compaction renumbers the vector file
	// (builder.go:440-529), so passage rows and vector positions change together.
	ReassignSlots(ctx context.Context, oldToNew map[int64]int64) error

	Get(ctx context.Context, id int64) (Passage, error)
	GetMany(ctx context.Context, ids []int64) ([]Passage, error)
	BySlot(ctx context.Context, slots []int64) ([]Passage, error)
	ForEach(ctx context.Context, fn func(Passage) error) error
	Count(ctx context.Context) (int, error)

	Files(ctx context.Context) (map[string]FileRow, error)
	Meta(ctx context.Context, key string) (string, error)
	SetMeta(ctx context.Context, key, value string) error
	Close() error
}

// Lexical is the persisted keyword index over the same database.
type Lexical interface {
	// Search returns passage IDs and scores, best first.
	Search(ctx context.Context, q LexQuery, k int) ([]ScoredID, error)
	Rebuild(ctx context.Context, an analysis.Analyzer) error // from passages.text, no embedding
	Stats(ctx context.Context) (LexStats, error)
}

package graphstore

type GraphStore interface {
	// The read methods are answered by SQL; Callers, Callees, Impact and Path
	// use recursive CTEs with a depth cap and cycle guard (3.7).
	Symbols(q string) ([]Symbol, error)
	Callers(fqn string, depth int) ([]Hop, error)
	Callees(fqn string, depth int) ([]Hop, error)
	Impact(fqn string, depth int) (*Impact, error)
	Path(from, to string) ([]Step, error)
	// ApplyFileDelta is atomic: either the file's old symbols and edges (and
	// the inbound edges to its old symbols) are retracted and the new ones
	// inserted, or nothing changes.
	ApplyFileDelta(ctx context.Context, d FileDelta) error
	RemoveFile(ctx context.Context, file string) error
	Stats() (Stats, error)
}
```

`GraphStore` is a superset of the methods the existing `GraphDB` interface needs for code symbols. Document hierarchy methods (`DocumentContext`, `DocumentTOC`, `ListDocuments`, `FullDocument`, `DocumentSymbols`, `interfaces.go:221-225`) go to a second small interface backed by plain tables (section 3.4).

The existing hook `GraphDBOpener` is a package-level mutable variable set by `main` (`pkg/gleann/registry.go:14`, assigned in `cmd/gleann/graph_cgo.go:23`). AGENTS.md section 4 asks for explicit dependencies; the new store is passed in through the searcher constructor and `GraphDBOpener` disappears with Kùzu.

### 3.3 One SQLite file per index; one global file for memory

**Index data: one file per index**, `<IndexDir>/<name>/<name>.db`, containing passages, lexical index, manifest, symbols, edges, document hierarchy. The complete schema (graph and document tables included) is created by the first format-2 release, empty until the graph is ported (3.4).

| Reason | Evidence |
|---|---|
| The index is already a directory that is deleted and rebuilt as a unit | `RemoveIndex` removes `<name>/` (`searcher.go:946-950`); WP-2.3 builds into `<name>.build-<pid>/` and renames it. One file travels with the directory. |
| One transaction can cover a file's passages, its lexical rows, its manifest hash and its graph delta | AGENTS.md section 2.3: "if graph update fails, the manifest must not advance." Today they are in three places (`passages.db`, `manifest.json`, `<name>_graph`). |
| Writers of different indexes do not wait for each other | The measured writer wait (3.0 s and 4.6 s) was per database. |
| Corruption or deletion affects one index | Quarantine of a damaged index does not touch others. |

**Memory: one global file**, `~/.gleann/memory/memory.sqlite`, for blocks, summaries and the entity graph, with a `ns` column for the logical index name where the entity graph needs it. Memory is shared by every project and every agent process; multi-process access to one file is exactly what WAL measured well (section 1.1). The entity graph moves here from the per-name Kùzu database `<IndexDir>/<name>_memory` (`internal/server/memory_handler.go:92`, `internal/mcp/memory_tools.go:66`).

**Embedding cache: stays as per-vector files in R1** (`~/.gleann/cache/embeddings/<sha256(model 0x00 text)>.bin`, `cache.go:41-47,197,206-219`). The cache is global and keyed by model and text, so entries are shared between indexes. The defect in W2 is the write timing, not the format: `Compute` stores results only after the whole inner call returns (`cache.go:128-140`), so a build killed halfway loses all of it. R1 fixes that by splitting the uncached texts into batches, calling the inner embedder per batch and writing each batch's files before the next starts. Per-vector files give lock-free concurrent reads and independent writes from parallel builds; a single global SQLite file would make every build and every agent process queue on one write lock (cache size at 1M chunks, 1.5-6 GB of blobs, is an estimate, not measured). A global `embeddings.sqlite` (or one file per model) remains a **later option**, to be decided only with a measurement of many small files against SQLite writes under parallel builds; see open question 9.16.

**Why new names for new files.** New SQLite files get names that older binaries do not open as bbolt: `<name>.db` replaces `<name>.passages.db`, memory uses `memory.sqlite`. Reusing an old name is unsafe in two ways. `internal/vault/tracker.go:50-57` and `internal/graph/indexer/filehash.go:77-84` delete a file that bbolt cannot open and create a new one, so an older binary would destroy a SQLite file opened under a bbolt name. And an older binary that finds no `<name>.passages.db` creates an empty one (`passage.go:68-71`, `bbolt.Open` without `ReadOnly`), reads zero passages and can rewrite the vector file from only the changed files (`builder.go:455-520`). Section 6.4 therefore leaves a guard file at the old names.

### 3.4 Schema (index database)

```sql
-- format and analyzer identity
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;
-- keys: store_format ("2"), analyzer ("gleann-tok/1"), language, vector_generation, ...

-- one row per indexed source file (replaces <name>.manifest.json and the graph hash store)
CREATE TABLE files (
  path       TEXT PRIMARY KEY,
  size       INTEGER NOT NULL,
  mtime_ns   INTEGER NOT NULL,
  hash       TEXT NOT NULL,          -- same value as ManifestEntry.Hash
  graph_hash TEXT,                   -- hash the graph rows were built from
  graph_dirty INTEGER NOT NULL DEFAULT 0,
  indexed_at INTEGER NOT NULL
) WITHOUT ROWID;
-- files whose graph rows need re-extraction (6.5); partial, so the scan is cheap
CREATE INDEX files_dirty ON files(path) WHERE graph_dirty = 1;

CREATE TABLE passages (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,  -- internal, never reused; FTS rowid
  pid       TEXT NOT NULL UNIQUE,               -- stable ID (below)
  source    TEXT NOT NULL,
  chunk_no  INTEGER NOT NULL,
  vec_slot  INTEGER,                            -- position in the vector file; NULL = lexical only
  kind      TEXT NOT NULL DEFAULT 'code',       -- code | prose; selects the analyzer rules (4.1)
  text      TEXT NOT NULL,
  meta      TEXT NOT NULL DEFAULT '{}'          -- remaining Passage.Metadata as JSON
);
CREATE INDEX passages_source ON passages(source, chunk_no);
CREATE UNIQUE INDEX passages_slot ON passages(vec_slot) WHERE vec_slot IS NOT NULL;

-- contentless: the text is stored once, in passages
CREATE VIRTUAL TABLE lex USING fts5(
  tokens,
  content = '',
  contentless_delete = 1,
  tokenize = "unicode61 remove_diacritics 0 tokenchars '_'"
);
-- lex.rowid = passages.id; `tokens` = space-joined output of the gleann analyzer

-- first-party declarations only; external symbols are never rows here
CREATE TABLE symbols (
  fqn         TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  file        TEXT NOT NULL,         -- declaration anchor (RFC 001: no phantom symbols)
  start_line  INTEGER NOT NULL,
  end_line    INTEGER NOT NULL,
  start_col   INTEGER NOT NULL,
  byte_offset INTEGER NOT NULL,
  is_test     INTEGER NOT NULL DEFAULT 0,
  weight      REAL NOT NULL DEFAULT 1.0,
  doc         TEXT NOT NULL DEFAULT ''
) WITHOUT ROWID;
CREATE INDEX symbols_file ON symbols(file);
CREATE INDEX symbols_name ON symbols(name);

CREATE TABLE edges (
  src        TEXT NOT NULL,
  dst        TEXT NOT NULL,
  kind       TEXT NOT NULL,          -- CALLS | IMPLEMENTS | REFERENCES
  file       TEXT NOT NULL,          -- file that contains the reference (retraction owner)
  dst_external INTEGER NOT NULL DEFAULT 0, -- 1: dst is a third-party or standard-library symbol with no row in symbols
  confidence TEXT NOT NULL DEFAULT 'extracted',
  PRIMARY KEY (src, dst, kind, file)
) WITHOUT ROWID;
CREATE INDEX edges_dst  ON edges(dst, kind);
CREATE INDEX edges_file ON edges(file);

-- document hierarchy (hierarchical GraphRAG), plain tables
CREATE TABLE doc_nodes (
  id TEXT PRIMARY KEY, type TEXT NOT NULL,       -- folder | document | heading | chunk
  parent TEXT, name TEXT, level INTEGER,
  rpath TEXT, hash TEXT, summary TEXT,
  text TEXT, start_char INTEGER, end_char INTEGER
) WITHOUT ROWID;
CREATE INDEX doc_nodes_parent ON doc_nodes(parent);
CREATE TABLE doc_explains (chunk_id TEXT NOT NULL, fqn TEXT NOT NULL, PRIMARY KEY (chunk_id, fqn)) WITHOUT ROWID;
```

Notes on the schema.

* **Compared with Kùzu's tables** (`internal/graph/kuzu/db.go:205-219`): `Symbol` there has `fqn, kind, file, line, name, doc, weight, is_test` and no end line, column, byte offset or `is_external`. RFC 001 requires those anchors; the table above carries them so a later change does not need a migration. The `DECLARES` relation becomes the `symbols.file` column, because RFC 001 allows one declaration anchor per symbol. If RFC 001 settles on a multi-file declaration for some languages, a `declares(file, fqn)` table is added. **Open point.**
* **Document hierarchy** is one table with a `parent` column instead of six node tables and six relation tables (`db.go:223-300`). The queries in `internal/graph/kuzu/query.go:331-700` walk Folder to Document to Heading to Chunk by single hops, which a parent column answers with indexed lookups. Whether to keep separate heading-to-chunk and document-to-chunk parents is **open**.
* **Entity graph** (memory) schema is in 3.5.
* The first-party symbol invariant (every symbol has an anchor, AGENTS.md section 2.2) is enforced by `NOT NULL` on `file`, `start_line`, `byte_offset`. **External symbols are not rows in `symbols`.** An earlier draft stored them with `file = ''`; that collides on the primary key when two files reference the same external symbol, and `DELETE FROM symbols WHERE file = ?` never retracts them. Instead an edge to an external target carries `dst_external = 1` and the target's qualified name in `dst`; the edge is owned by the referencing file and is retracted with it. This keeps external references explicitly tagged and separated from project declarations, and creates no phantom declaration. **Open** (9.10): whether external targets need attributes of their own (package, version), in which case an `external_symbols(file, fqn)` table owned by the referencing file is added, or whether RFC 001 prefers a count only for unresolved calls.
* **Full schema from the first format-2 release.** The graph and document tables (`symbols`, `edges`, `doc_nodes`, `doc_explains`) are created in R1 and stay empty until the graph is ported (R2), so opening an R1 index in R2 needs no schema migration. These tables hold derived data that `gleann graph build` regenerates, so if RFC 001 changes their columns before R2, the tables are dropped and recreated (`meta.graph_schema`) and the graph is rebuilt; no user data is lost.

#### Stable passage IDs (WP-2.1)

* `pid = hex(sha256(source 0x00 sha256(text) 0x00 dup_index))[:32]`, where `dup_index` is the order of this text among identical texts of the same source (normally 0).
* The ordinal `chunk_no` is **not** part of the ID. An insertion at the top of a file shifts every ordinal but leaves unchanged chunks with the same `pid`, so their vectors and cache entries remain valid.
* The integer `id` is internal, assigned by `AUTOINCREMENT`, never reused. Results are ordered by score and then by `pid`, so the same corpus gives identical result lists twice (IDX-06).
* `vec_slot` is the position in the vector file. For migrated indexes `vec_slot = old Passage.ID`, so vector files are not rebuilt by migration (section 6). Until WP-2.2 ships segments, a changed file still triggers the existing rebuild path; this RFC does not change vector files. That path includes `compact` (`builder.go:440-529`), which rewrites the vector file densely from 0. After a compaction the stored `vec_slot` values would point at the wrong passages, so every update publishes the new vector file in this order (implemented in step 4, review fix): (1) write the new vector file under `<name>.index.tmp`; (2) in ONE transaction, apply every database change of the update (removed and changed sources, `ReassignSlots`, new passages) and set `meta.pending_vector_file` and `meta.pending_vector_generation`; (3) rename the file into place; (4) in a second transaction set `meta.vector_generation` and delete the pending keys, then mirror the generation into `meta.json`. Every open of a format-2 index runs a recovery first: a pending key with the temp file present rolls the rename forward; a pending key without it finishes step 4; no pending key and a stale temp file means the crash came before the commit, so the temp file is deleted. A crash at any point therefore leaves slots and vector file in agreement. `index doctor` reports a pending publication and a generation that differs between the database and `meta.json`. The first draft changed the slots in several transactions before the rename; a crash between them made vector hits return the wrong passages silently (found in review, crash tests added).
* **Open:** whether the chunk text embedded by the chunker includes context (neighbouring lines, file header) such that an unchanged chunk can still change its text. If so, the stable-ID gain is smaller; it needs a measurement on a real edit history.

### 3.5 Schema (memory database)

```sql
-- Not WITHOUT ROWID: the FTS5 table needs an integer rowid. An explicit INTEGER PRIMARY KEY
-- is used (not the implicit rowid) because implicit rowids may change on VACUUM.
CREATE TABLE blocks (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT NOT NULL UNIQUE, tier TEXT NOT NULL, label TEXT NOT NULL, content TEXT NOT NULL,
  source TEXT NOT NULL, tags TEXT NOT NULL DEFAULT '[]', metadata TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, expires_at INTEGER,
  char_limit INTEGER NOT NULL DEFAULT 0, scope TEXT NOT NULL DEFAULT '',
  repo TEXT NOT NULL DEFAULT '', paths TEXT NOT NULL DEFAULT '[]', symbols TEXT NOT NULL DEFAULT '[]',
  commit_hash TEXT NOT NULL DEFAULT '', suspect INTEGER NOT NULL DEFAULT 0, stale_reason TEXT NOT NULL DEFAULT '',
  confirms INTEGER NOT NULL DEFAULT 0, conflicts INTEGER NOT NULL DEFAULT 0,
  access_count INTEGER NOT NULL DEFAULT 0, last_accessed_at INTEGER
);
CREATE INDEX blocks_tier ON blocks(tier);
CREATE INDEX blocks_scope_repo ON blocks(scope, repo);
CREATE VIRTUAL TABLE blocks_lex USING fts5(tokens, content = '', contentless_delete = 1,
  tokenize = "unicode61 remove_diacritics 0 tokenchars '_'");   -- rowid = blocks.seq

CREATE TABLE summaries (key TEXT PRIMARY KEY, data TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE memory_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL) WITHOUT ROWID;

CREATE TABLE entities (ns TEXT NOT NULL, id TEXT NOT NULL, type TEXT NOT NULL, content TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}', PRIMARY KEY (ns, id)) WITHOUT ROWID;
CREATE TABLE relations (ns TEXT NOT NULL, src TEXT NOT NULL, dst TEXT NOT NULL, relation_type TEXT NOT NULL,
  weight REAL NOT NULL DEFAULT 1.0, attributes TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (ns, src, dst, relation_type)) WITHOUT ROWID;
CREATE INDEX relations_dst ON relations(ns, dst);
CREATE TABLE entity_archive (archive_id TEXT PRIMARY KEY, ns TEXT NOT NULL, id TEXT NOT NULL,
  version_id TEXT NOT NULL, type TEXT, content TEXT, attributes TEXT, valid_to TEXT) WITHOUT ROWID;
```

* Columns mirror `memory.Block` (`pkg/memory/block.go:41-76`); `summaries` key mirrors `<created>_<conversation_id>` (`store.go:381`); the summary body stays JSON (`block.go:113-121`).
* Entity traversal (`MATCH (start:Entity {id})-[:RELATES_TO*0..N]->(n)`, `memory_service.go:230-255`) becomes a recursive CTE capped at depth 10, as documented in `docs/architecture.md:198`. The measured recursive-CTE cost at 1M edges and depth 3 is p50 388 µs, far below what an entity graph of agent notes needs. Whether the entity graph stays at all is a product question (section 9).
* `Store.Add` today reads then rewrites an existing block (`store.go:106-170`, deduplication by ID). With several processes that sequence must run inside one `BEGIN IMMEDIATE` transaction.

### 3.6 Pragmas, transactions and writers

| Setting | Value | Status |
|---|---|---|
| `journal_mode` | `WAL` on local file systems; `DELETE` with a warning on network file systems (below) | `WAL` used in all spike runs (`spike/main.go:326`, `:351`). The fallback is **not measured**. |
| `busy_timeout` | `10000` ms | Measured in the writers test. |
| write transactions | `BEGIN IMMEDIATE` (driver `_txlock=immediate`) | Used in the writer child process (`spike/main.go:351`). Avoids lock-upgrade failures that `busy_timeout` cannot retry. |
| `synchronous` | `NORMAL` for normal operation; `OFF` only while building into a temporary directory that is fsynced and renamed (WP-2.3) | The spike used `OFF` for build timing (`spike/main.go:136`). `NORMAL` is **not measured**. |
| `foreign_keys` | `ON` where used | Not measured. |
| `wal_checkpoint(PASSIVE)` | after bulk builds; a busy result is logged and ignored, never fails the build | The spike used `TRUNCATE` (`spike/main.go:152`). `TRUNCATE` needs exclusive access and cannot complete while a reader in `gleann serve` or an MCP process holds a read transaction, so `PASSIVE` is used; the WAL file may stay large until the next checkpoint. Not measured. |
| `cache_size`, `mmap_size`, `wal_autocheckpoint` | defaults | **Open**; tune from the scale benchmark. |
| connections | one `*sql.DB` for writes with `MaxOpenConns(1)`, one for reads | Design choice, not measured. |

**Batch-commit rule.** An index build or sync never holds one transaction for the whole run. **Each source file is exactly one transaction, whatever its number of passages**, containing that file's passages, lexical rows, manifest row, and graph delta. A file is never split across transactions: if it were, the first transaction would delete the old passages and insert only part of the new ones, and a crash would leave the file truncated. (An earlier draft cut at 1,000 passages; that is withdrawn. Memory use for a very large generated file is not measured.) Reason for per-file rather than per-run: measured, a long write transaction made small writers in other processes wait 3.0 s and 4.6 s. With per-file commits, memory writes from another agent process wait milliseconds. A side effect: the `files.hash` row is written in the same transaction as the data it describes, so a killed build leaves a consistent, resumable state (IDX-10).

In a multi-file sync, file 1 therefore advances its manifest row before file 2 is processed. That is intended: AGENTS.md section 2.3 ("if graph update fails, the manifest must not advance") is satisfied per file, because a file's hash is committed only together with its graph delta. If file 2 fails, file 1 stays committed and file 2 keeps its old hash, so the next sync retries exactly file 2. A sync-wide generation commit that held back all hashes until every file succeeded was considered and not adopted: it would make one bad file discard the progress of all others and would need one long transaction, which the writer measurements argue against. The vector file is separate; it is built into a temporary file, renamed, and then `meta.vector_generation` is set. Passages with `vec_slot IS NULL` are served lexically; this is the existing `LexicalOnly` / `VectorReady` staging (`types.go:104-105`, `builder.go:90-91`).

**Single writer per index.** SQLite serialises writers within the busy timeout. Two processes running `index sync` on the same index would interleave batches and both succeed, which the spike did not test beyond the 3 s transaction case. Therefore a second sync of the same index fails fast with a readable message, using an OS file lock on `<IndexDir>/<name>.lock` (platform guard: `flock` on unix, `LockFileEx` on windows, in files with build constraints). This matches AGENTS.md section 2.4 (CLI fails fast on a locked database). The lock covers builds and syncs only; searches and memory-style small writes do not take it.

* **No stale locks.** These locks belong to the open file handle, so the operating system releases them when the process exits or is killed. A heartbeat row and stale-lock recovery are not needed (open question on lock strategy closed).
* **Lock file beside the index directory, not inside it.** `withIndexRollback` renames the whole `<name>/` directory aside (`internal/indexing/sync.go:474-496`). On Windows a directory containing an open file cannot be renamed, so a lock inside `<name>/` would block the rebuild path. The lock file is `<IndexDir>/<name>.lock`; `RemoveIndex` removes it together with the other companion files.
* **Renames only without open handles.** Any step that renames the index directory or the database files (`withIndexRollback`, `index migrate` step 4, `migrate --rollback`) runs under the index's exclusive lock after every database handle of this process is closed (including the `-wal` and `-shm` files), and fails with a readable message if another process still has the index open. This is by reasoning about Windows file semantics; it is not run here and needs the Windows CI test in 7.1.

**Network file systems.** SQLite documents that WAL does not work over network file systems (it relies on shared memory). At open, the store inspects the file system of the database path (`statfs` type on linux and darwin, UNC path or remote drive type on windows). If it is a network file system, the store uses `journal_mode = DELETE` instead of WAL and logs a warning that concurrent access from several processes is not safe there. The `DELETE` fallback loses the "readers never block" property; locking on NFS is itself unreliable, so the warning says that the location is unsupported for multi-process use. Detection is **not measured** on any platform and is covered by open question 9.9.

### 3.7 Graph queries: SQL only; a CSR is a possible later optimisation

Graph reads are answered by SQL on `symbols` and `edges`. Direct neighbours are indexed lookups (`edges_dst`, and the primary key for outgoing edges). Multi-hop queries (callers or callees to a depth, impact, shortest path) use recursive CTEs with `UNION` (not `UNION ALL`, so cycles terminate) and an explicit depth cap. Measured on 200K nodes and 1M edges: callers to depth 3 p50 388 µs, p95 735 µs; direct callers 18 µs (1.1). These are interactive times for the CLI, MCP tools and REST API.

Reasons for not building an in-memory CSR first: it would be a second representation of the same graph; it must be rebuilt after commits that change the graph; and a long-running `gleann serve` or MCP process would not learn about a commit made by a CLI `index sync` in another process, so it would serve stale answers unless a change-detection mechanism is added. The CSR would be faster (8.6 µs p50, 19 µs p95 at depth 3, 14 ms to load, 5 MB heap) but the cost of the CTE is already low.

**Possible later optimisation (not planned).** If a measured query needs it (for example the community and PageRank passes, or Impact on very large graphs), a read-only CSR can be built from the tables on demand, keyed by a `meta.graph_generation` counter that every graph commit increments, so a process notices a stale CSR by comparing one integer. Rebuild after a change (14 ms measured at 200K nodes and 1M edges) is preferred over patching in memory. This needs its own measurement and is not part of R1 or R2. The community and PageRank code (`louvain.go`, `pagerank.go`) already works on a plain in-memory graph value and can be fed by a `SELECT` over the tables without a CSR.

This choice follows audit finding 12 and **awaits the maintainer's confirmation** (2.1).

---

## 4. Tokenizer contract

The tokenizer, not FTS5, decides what is a term. FTS5 receives pre-tokenised text and only has to split on spaces.

### 4.1 Contract

1. `analysis.Analyzer` has one method for documents and one for queries, `Tokens(text string, kind Kind) []string` and `QueryTokens(text string) [][]string` (each query term as a group of alternative forms: base form, then the Turkish-folded form; `MatchExpr` joins them). The `kind` argument carries item 3's prose/code switch; implemented in `internal/analysis` (step 2). Both are deterministic and versioned: `meta.analyzer` records its identifier (`gleann-tok/1`). A mismatch between the stored analyzer and the running one makes `Lexical.Rebuild` run from `passages.text` and `passages.kind` (no embedding, no source files). At 100K chunks a contentless build measured 2.64 s. Updating a passage's lexical row needs no re-tokenising of the old text: `DELETE FROM lex WHERE rowid = ?` and `INSERT OR REPLACE` were measured to work with `contentless_delete = 1` (1.1), so the FTS5 `'delete'` command with the old text is not used.
2. **Base behaviour = today's tokenizer** (`modules/bm25/bm25.go:359-383`): split on non-letter, non-digit; lowercase; drop words of one character and the stop list (`bm25.go:385-395`); for camelCase, PascalCase and letter-digit transitions add the sub-words (`splitCamelWords`, `bm25.go:318-355`). This is what gate 1 measured against FTS5.
3. **Turkish folding only for Turkish prose.** The rules apply when the index language is `tr` **and** the passage's `kind` is `prose` (documents, markdown, text; `passages.kind`, 3.4). Code passages always use the base tokenizer, so identifiers such as `ID`, `INFO`, `INIT` stay `id`, `info`, `init` even in an index with Turkish documentation. The Turkish rules are: `İ→i`, `I→ı`, and for every token containing `ı`, `ş`, `ğ`, `ç`, `ö` or `ü` an additional ASCII variant token (`isiklari` for `ışıkları`). The language is a per-index setting in `meta.language` (`auto` is a possible default); `kind` is set by the indexer from the source file type, so `Lexical.Rebuild` from `passages.text` has what it needs. A query has no kind, so `QueryTokens` on a `tr` index emits for each token both the base form and, when it differs, the Turkish-folded form, joined with `OR`; a query `ID` then matches `id` in code and `ıd` in Turkish prose. Gate 2 (spike): with Turkish rules and the ASCII variant, all six probe queries (İSTANBUL, istanbul, ISTANBUL, IŞIKLARI, isiklari, NÖBETÇİ) matched; raw FTS5 `unicode61` matched one; today's tokenizer matched four. The spike applied `I→ı` to all text; the per-kind switch and the two-form query are **not yet measured** and belong to the XQuAD-tr / TurHistQuad paired bench (7.2).
4. **CJK.** A run of Han, Hiragana, Katakana or Hangul characters is emitted as unigrams and bigrams in documents, not left as one token. On the query side a run of two or more characters is searched by its bigrams only (a single character by its unigram): OR-ing a query's unigrams would match every passage containing any common character of it (review finding at step 2). The spike measured FTS5 `trigram` at 3.4 times slower to build and 2.5 times larger than `unicode61` at 100K (26.2 s against 3.4 s, 424 MB against 172 MB), which is why bigrams are produced by the analyzer. **The CJK emission itself is not measured**; IDX-08 is the test.
5. **FTS5 tokenizer setting:** `unicode61 remove_diacritics 0 tokenchars '_'`. The gate 1 quality numbers used `unicode61 tokenchars '_'` with the default `remove_diacritics` (`spike/main.go:182`). The setting in the DDL differs, so gate 1 must be re-run with the final setting in step 3 of section 8. **Open.**
6. **Field weights** (symbol name, path, body; WP-2.4) would need extra FTS5 columns and `bm25(lex, w1, w2, w3)`. Not part of the first version; added only if the paired bench improves.

### 4.2 Query construction

| Step | Description | Status |
|---|---|---|
| 1 | Tokenise the query with `QueryTokens`, deduplicate, quote each token, join with `OR`, rank with `bm25(lex)`. **If `QueryTokens` returns no token (a query of only stop words, punctuation or one-character words), return an empty result without running SQL**: `MATCH ''` fails with `fts5: syntax error near ""` (measured, 1.1). When quoting a token, double any embedded `"` (FTS5 string syntax); the analyzer already splits on non-letters so a quote should not occur, but the store must not depend on that. | Build of the expression **measured** (`spike/main.go:94-105`, `drivers/main.go:86`); empty-query failure **measured**; quote escaping to be covered by a unit test. |
| 2 | Try all tokens with `AND` first; fall back to `OR` only when fewer than `k` results return. | **Not measured.** Proposed mitigation for common terms at 1M+ (D4). |
| 3 | Drop query terms whose document frequency is above a cut (fraction of `N`, to be chosen). | **Not measured.** The cut value is **open**; it changes ranking and must be checked with the paired bench. |
| 4 | Identifier-like queries (`IsIdentifierQuery`, RFC 002) keep their current handling before the lexical leg. | Unchanged. |

Known limit, accepted by the maintainer: FTS5 has no early-termination ranking, so very common terms cost time at 1M+ (p50 180 ms at 1M, a pessimistic corpus). If steps 2 and 3 do not suffice, `store.Lexical` allows a purpose-built postings index with WAND later without migrating the record store again.

Scoring differences: FTS5's `bm25()` uses its own constants; gleann's scorer uses `k1 = 1.5` (`bm25.go:38`). Gate 1 already includes this difference. There is no knob to align them in FTS5, so exact score parity is not a goal; the quality gate is.

---

## 5. Build tags and platform impact

| Build | Record store and lexical | Graph |
|---|---|---|
| Default (`CGO_ENABLED=0`, no tags) | modernc SQLite | SQL tables and recursive CTEs in the same SQLite file (3.7). Graph commands work in every build, including `go install` and windows. |
| `-tags sqlitecgo` (optional, not required for release) | C SQLite through a cgo driver, same schema and file format | same |
| `-tags treesitter` | unchanged | Tree-sitter parsers only, after the Kùzu code is split out (below). |
| `-tags kuzu` (one release) | unchanged | Kùzu adapter, read-only export for migration, then removed. |
| `-tags eif`, `native`, `faiss` | unchanged | unchanged |

* **AGENTS.md section 1.3 must be amended.** Today `treesitter` activates both Tree-sitter and the Kùzu database. This RFC separates them: `kuzu` for the Kùzu code, `treesitter` for parsers. Every Kùzu consumer file (section 6.2) already has a `//go:build treesitter` constraint and needs the new tag; the stub rule still applies (the tag off must produce a clear error, never a compile failure).
* **Extractors.** The extractors already return plain Go structs, not Cypher: `collectGoCallQueries` returns `[]kuzu.SymbolNode, []kuzu.EdgeCalls` (`internal/graph/indexer/go_calls.go:18`; only its doc comment at `:17` still says "Cypher queries"), and `collectTSCallQueries` has the same return types (`ts_calls.go:136`). The blocker is where the types live and the tag: `SymbolNode` and `EdgeCalls` are defined in `internal/graph/kuzu/writer.go:27,45`, which is `//go:build treesitter`, and `go_calls.go` imports that package and carries the same tag even though it uses `go/ast` only. The shared work of WP-G1/G2 and RFC 001 is to move these types into a tag-free package (the `FileDelta` input of `ApplyFileDelta`), drop the Kùzu import from the extractors, and remove the tag from the pure-Go ones. Without it the default build would have a graph store but only whatever extractors are untagged. I did not check the other extractors (`go_implements.go` has the same tag).
* **Binary size.** A stripped program that only opens SQLite measured 6.6 MB; the gleann lite binary is 72.8 MB unstripped. The expected cost is under 10%. The real gleann binary with the module linked was not measured.
* **Platforms.** The spike compiled with `CGO_ENABLED=0` for linux, darwin and windows on amd64 and arm64 (9.9-11 MB). It ran only on linux/amd64. CI today runs tests on ubuntu, macos and windows (`.github/workflows/ci.yml:21`); linux/arm64 is cross-built for release only (`release.yml:57`) and windows/arm64 is not tested. Running on windows/amd64 and linux/arm64 is a reversal gate (7.2).
* **Optional cgo tag.** Published reports say modernc is 1.3-2x slower on reads and 2-5x on writes than C SQLite (2024 benchmarks, not re-measured). If the scale benchmark shows this matters, `-tags sqlitecgo` links the C library behind the same schema. The default stays pure Go (AGENTS.md section 1.3).
* **No Python, no Docker, no server process** is introduced.

---

## 6. Migration and incremental strategy

### 6.1 Inventory: what exists today

Files per index `<name>` under `<IndexDir>`:

| Path | Format | Owner | Notes |
|---|---|---|---|
| `<name>/<name>.meta.json` | JSON `IndexMeta` | `pkg/gleann` (`types.go:81-107`, written `builder.go:109`) | catalog entry; `ListIndexes` reads it (`searcher.go:867`). Has `Version` ("1.0.0"), compared only to warn (`searcher.go:94-98`). |
| `<name>/<name>.passages.db` | bbolt, bucket `passages`, key = big-endian ID | `pkg/gleann` (`passage.go:16,48,70,127`) | IDs from `NextSequence`; vector position = ID (`index_health.go:73`). Old `.passages.jsonl`/`.idx` are removed if found (`passage.go:541-542`). |
| `<name>/<name>.index` | single vector file (HNSW or DiskANN) | `modules/hnsw`, `modules/diskann` (loaded `searcher.go:145-170`) | unchanged by this RFC. |
| `<name>/<name>.manifest.json` | JSON Merkle manifest | `pkg/gleann/manifest.go:15-30`, path `internal/indexing/build.go:314` | `Version` "1.0.0" (`manifest.go:123`). |
| `<name>.sync.json` (in `<IndexDir>`) | JSON | only removed by `RemoveIndex` (`searcher.go:968`); no writer found in the code | legacy. |
| `<name>_graph` | Kùzu database (directory, or a file in some layouts) | `internal/graph/kuzu` (`db.go:96`), path `searcher.go:175-190` | also searched at `<name>/.kuzu` (legacy). |
| `<name>_graph.wal`, `<name>_graph.shadow` | Kùzu WAL files | Kùzu | removed by `RemoveIndex` (`searcher.go:961`). |
| `<name>_graph_file_hashes.db` or `<name>_graph/file_hashes.db` | bbolt, bucket `indexed_files_v1` | `internal/graph/indexer/filehash.go:32,60-84` | file hash per source for graph sync. |
| `<name>_memory` | Kùzu database (entity graph) | `internal/server/memory_handler.go:92`, `internal/mcp/memory_tools.go:66` | only in treesitter builds. **Not removed by `RemoveIndex`** (`searcher.go:945-975` removes `<name>/`, `<name>_graph`, its `.wal`, `.shadow` and `_file_hashes.db` companions, and `<name>.sync.json`, nothing named `_memory`). |
| `<name>_memory_vectors` | a normal gleann index | `internal/mcp/memory_syncer.go:22`, `internal/graph/kuzu/leann_syncer.go` | vector copy of entity content. Also not removed by `RemoveIndex`. |
| `<name>.sync-bak` | directory | `internal/indexing/sync.go:474-496` (`withIndexRollback`) | transient. |

Global files:

| Path | Format | Owner | Notes |
|---|---|---|---|
| `~/.gleann/memory/memory.db` (or `$GLEANN_MEMORY_DIR/memory.db`) | bbolt, buckets `blocks`, `summaries`, `meta` | `pkg/memory/store.go:19-21,38-47,57` | values are JSON `Block` / `Summary`. |
| `~/.gleann/cache/embeddings/<sha256(model 0x00 text)>.bin` | raw little-endian float32 | `internal/embedding/cache.go:41-47,197,206` | L1 is an in-process otter cache. |
| `~/.gleann/vault/vault.db` | bbolt, buckets `files`, `paths` | `internal/vault/tracker.go:22-23,34,49` | hash to file path tracker for the vault feature. |
| `~/.gleann/conversations/*.json` | JSON | `pkg/conversations/store.go:53-55` | chat history. |

Bbolt users: passages, memory, vault tracker, graph file hashes (four). Kùzu users: code graph, document hierarchy, memory entity graph (three databases per index family).

The orphaned `_memory` and `_memory_vectors` directories after `index remove` are an existing bug independent of this RFC; the fix (remove both in `RemoveIndex`, and the new `<name>.lock`) goes in as its own small change, and it needs a decision on whether entity data should die with the index (9.4).

### 6.2 Inventory: Kùzu consumers and the queries they issue

All of these files carry `//go:build treesitter` today.

| Consumer | Where | Cypher features used | Plan |
|---|---|---|---|
| Symbol lookup (`Callees`, `Callers`, `SymbolsInFile(Detailed)`, `ResolveSymbolCandidates`, `SymbolSearch`) | `internal/graph/kuzu/query.go:56-283, 1036` | single-hop `MATCH`, property filter, `ORDER BY`, `lower(x) CONTAINS` | **Tables.** Name search becomes `LIKE` on indexed columns. |
| `Impact`, `Neighbors`, `ShortestPath` | `query.go:723, 823, 939` | only single-hop queries; the multi-hop walk is breadth-first in Go (`query.go:728-790`) | **Recursive CTE** with depth cap and cycle guard (3.7). Measured depth-3 callers: 388 µs p50, 735 µs p95 at 1M edges (CSR would be 8.6 µs, 3.7). |
| `Stats` | `query.go:1058-1115` | `count()` | **Tables** (`SELECT count(*)`). |
| Document hierarchy (`DocumentSymbols`, `DocumentContext`, `DocumentTOC`, `ListDocuments`, `FullDocument`) | `query.go:331-700`, writers `internal/graph/indexer/doc_indexer.go` | `OPTIONAL MATCH`, `count()`, `ORDER BY`, single-hop paths over six relations | **Tables** (`doc_nodes`, `doc_explains`). Hierarchy is read by indexed `parent` lookups. |
| Incremental retraction (`IncomingEdges`, `ExistingSymbolFQNs`) | `internal/graph/kuzu/incremental.go:23,111` | `MATCH` with `IN` list | **Ported** into `ApplyFileDelta` (section 6.5). |
| Writers (`UpsertSymbol`, `AddCalls`, CSV `COPY FROM`, `DeleteFileQueries`) | `kuzu/writer.go`, `internal/graph/indexer/indexer.go:127,143,549,566,825,892`, `go_calls.go`, `ts_calls.go`, `go_implements.go` | `COPY FROM` CSV, `MERGE`, `DELETE` | **Replaced** by `ApplyFileDelta` and batched `INSERT`. The extractors already return structs (5), so the work is moving the struct types out of the tagged `kuzu` package (RFC 001 / WP-G2). |
| Community and PageRank loader | `internal/graph/community/kuzu_loader.go:16-82` | four full scans (`Symbol`, `CALLS`, `DECLARES`, `IMPLEMENTS`) | **Table scans.** The loader reads `symbols` and `edges` with `SELECT` into the plain graph value that `louvain.go` and `pagerank.go` already work on. |
| Graph CLI: `viz`, `report`, `communities`, `export`, `wiki`, `map`, `risk`, `toc`, `explain`, `path`, `query`, `deps`, `callers`, `build`, `hook` | `cmd/gleann/graph_cgo.go:34-1075` | scans via `Conn().Query` at `:287`, `:302`; rest through `kgraph.DB` methods | **Tables.** `export --format cypher` writes Neo4j-style text from the in-memory graph (`:592`), not from a database; unaffected. |
| MCP graph tools | `internal/mcp/server.go:1109,1252,1318,1382,1506,1640,1696` (`gleann_graph_neighbors`, `gleann_document_links`, `gleann_read_full_document`, `gleann_document_toc`, `gleann_impact`, `gleann_graph_stats`, `gleann_symbols_in_file`), `internal/mcp/tools_graph.go:105,181,365,669` (`gleann_communities`, `gleann_risk_analysis`, `gleann_repo_map`, `gleann_navigate_symbol`) | method calls through `gleann.GraphDB`; four aggregate queries with `count(caller) ... GROUP BY ... ORDER BY ... LIMIT` (`tools_graph.go:255-291, 477`) and a fallback `MATCH (s:Symbol)` (`:526`) | **Tables.** The aggregates are in-degree counts: a `GROUP BY` on `edges`. |
| Memory entity service | `internal/graph/kuzu/memory_service.go:49-340`, MCP `inject_knowledge_graph`, `delete_graph_entity`, `traverse_knowledge_graph` (`internal/mcp/memory_tools.go:97,198,245`), REST `/api/memory/*` (`internal/server/memory_handler.go`) | `MERGE`, `DELETE`, variable-length `-[:RELATES_TO*0..N]->` (`:230-255`), archived versions | **Tables** (`entities`, `relations`, `entity_archive`) and a recursive CTE with the existing depth cap of 10. |
| REST graph API | `internal/server/graph_handler.go:167-460`, `internal/server/graph_cgo.go:59-126`, `a2a_graph_cgo.go:128-142` | predefined queries; **`type: "cypher"` accepts raw Cypher** from the client (`graph_handler.go:247`, `RawCypher` at `internal/server/graph_cgo.go:125`; documented in `openapi_schemas.go:187-190`). The web UI offers it too: `ui/src/components/Graph.tsx:120` has a "Raw Cypher" option in the query-type list. | Predefined queries port. **Raw Cypher is retired**; the endpoint returns 400 with a message for one release, and `Graph.tsx` changes in the same release (remove the option or replace it with the structured symbol and path queries) so the console does not fail. **Open point 9.3**: confirm nobody depends on it. |
| Searcher graph context | `pkg/gleann/searcher.go:495, 592-680, 1031-1072` | `Callers`, `Callees`, `SymbolsInFile`, `DocumentContext` via `GraphDB` | **Interface unchanged.** |
| `def` resolver, ReAct, TUI, benchmark | `pkg/gleann/def.go:106`, `react.go:185`, `internal/tui/chat.go:2350`, `cmd/gleann/cmd_benchmark.go:254` | via `GraphDB()` | **Interface unchanged.** |
| Memory vector sync | `internal/graph/kuzu/leann_syncer.go`, `internal/mcp/memory_syncer.go`, `internal/server/memory_syncer.go` | none (builds a gleann index of entity content) | Kept; it calls the index builder. |

Result: apart from the REST raw-Cypher endpoint, nothing user-visible depends on Cypher. Variable-length paths exist only in the memory entity traversal.

### 6.3 Index format version

* **Today:** `IndexMeta.Version` is the constant `"1.0.0"` (`builder.go:87`); `Load` logs a warning if it differs and tries to read anyway (`searcher.go:94-98`). It does not identify the storage format, and no code distinguishes formats. The manifest has its own constant `"1.0.0"`.
* **New:** an integer `store_format` in `meta.json` (absent means 1, new indexes write 2). The same value is in `meta` inside the database. `meta.json` stays: it is the catalog that `ListIndexes` and `GetIndexMeta` read without opening any database.
* **Detection of old indexes:** `store_format` absent or 1, or `<name>.passages.db` present and `<name>.db` absent. `index doctor`/`repair` (`pkg/gleann/index_health.go`) cross-checks the field against the files.
* The existing `Version` check is left alone (changing it would make old binaries warn on every load). It cannot protect a migrated index: an older binary only warns on a different version (`searcher.go:94-98`) and then carries on. The protection against an older binary is the guard file in 6.4 step 4.

### 6.4 `gleann index migrate`

`gleann index migrate <name>` (and `--all`), implemented as copy, verify, switch.

1. Take the index's exclusive lock (`<IndexDir>/<name>.lock`, 3.6); open the old store read-only (`legacy_bbolt.go`); refuse if another process holds the index.
2. Create `<name>/<name>.db.tmp`. For each passage: insert with `id = old ID`, `vec_slot = old ID`, `source` and `chunk_no` from `Metadata["source"]` and `chunk_index` (`internal/indexing/read.go:289-291`; passages without them get `chunk_no` by order within the source), compute `pid`, tokens with the stored analyzer and language (the language is guessed from the corpus unless `--language` is given). Insert `files` rows from the old manifest. Commit in batches.
3. Verify: passage count equals `meta.json` `num_passages`; every `vec_slot` is within the vector file's length; `Lexical.Search` on 20 sampled passages returns the sampled passage in its top 10; no embedding is computed.
4. Switch, with every database handle closed (Windows cannot rename a file that is open): rename `<name>.db.tmp` to `<name>.db`; set `store_format = 2` in `meta.json`; rename `<name>.passages.db` to `<name>.passages.db.v1` and `<name>.manifest.json` to `<name>.manifest.json.v1`; then **write a guard file** at `<name>.passages.db` and at `<name>.manifest.json`. The guard is a short non-empty text file ("this index uses store format 2; use gleann version X or newer"). It must not be empty and must not look like a bbolt file: `bbolt.Open` without `ReadOnly` creates and initialises a missing or empty file (`passage.go:68-71`), whereas a non-empty file that is not a bbolt database makes it fail with an error. Measured with the v1.9.0-equivalent baseline binary (2026-11-16): its search fails on the guard as intended, but its `index sync` fails the incremental update on the guard, falls back to a full rebuild and recreates the index as format 1 from the source files, removing the guard, `<name>.db` and the `.v1` files. No source data is lost, because an index is derived data, and the result is a valid format-1 index that the new binary reads; but the guard does not stop an older binary's sync. The memory database's guard (6.4 Memory) does stop older binaries, because memory has no rebuild path. The switch itself is ordered so every intermediate state is recoverable: the old files are renamed to `.v1` and the guards written first, and renaming `<name>.db.tmp` to `<name>.db` and setting `store_format = 2` come last; an error undoes the completed steps in reverse order, and a re-run of `migrate` or `migrate --rollback` resolves any state a crash leaves. Rollback clears `store_format` in `meta.json` before it removes `<name>.db`, and an index whose `meta.json` says format 2 without a `<name>.db` is refused with a message instead of opening an empty database.
5. The vector file `<name>.index` is untouched.

**Read-old, write-new.** In release 1 a format-1 index is searchable through `legacy_bbolt.go` (passages from bbolt, BM25 built in RAM as today). A write operation (`index sync`, `rebuild`, `graph build` for that index) on a format-1 index runs the migration first; `--no-migrate` makes it refuse instead. **Open point 9.1:** whether implicit migration on sync is acceptable or the command should require an explicit `migrate`.

**Rollback.** Step 4 renames the old files rather than deleting them, and the guard files make an older binary fail loudly instead of serving stale data. `gleann index migrate --rollback <name>` runs under the same exclusive lock with all handles closed, replaces the guard files by the `.v1` files and deletes `<name>.db`. Changes made after the switch (syncs) are lost on rollback; the old manifest would then be stale, so the next old-binary sync re-embeds the changed files (slower, still correct). The `.v1` files are removed by `gleann index migrate --cleanup`, and by release 3 after a warning.

**Memory.** The copy must not be interruptible into a state that looks complete, and two processes must not run it at once.

1. On open, if `memory.db` exists (a real bbolt file, not the guard) and `memory.sqlite` does not, take an exclusive lock on `memory.migrate.lock` in the memory directory. Re-check after taking the lock; if another process finished meanwhile, continue as a normal open.
2. Delete any stale `memory.sqlite.tmp` and copy `blocks`, `summaries`, `meta` from `memory.db` into new tables in `memory.sqlite.tmp` (block IDs and timestamps preserved). If the old file is locked by a running process (`store.go:57` timeout), fail with the existing "locked by another process" message.
3. Verify the row counts of every table against the source; record `memory_meta.migrated_from = "memory.db"` and the counts.
4. Close the database and rename `memory.sqlite.tmp` to `memory.sqlite`.
5. Rename `memory.db` to `memory.db.v1` and write a guard file at `memory.db` (same reasoning as for the passage store: an older binary would otherwise create an empty `memory.db`, `store.go:57`).

A crash before step 4 leaves only the `.tmp` file, which the next start discards and redoes; `memory.sqlite` never exists in a partial state. A crash between steps 4 and 5 leaves both; `memory_meta.migrated_from` marks `memory.sqlite` as authoritative and the next open completes step 5. The short-term tier is in memory only and is not persisted.

**Embedding cache.** Not migrated in R1: the per-vector files stay as they are (3.3). If a global SQLite cache is adopted later, the lazy import on miss (look up `<sha>.bin` in the old directory, import it, `gleann cache gc` deletes imported files; key stays `sha256(model 0x00 text)`, a 32-byte blob) is the migration route.

**Graph and entity data.**

* **Code graph and document hierarchy are derived data.** In every build they can be rebuilt from source with `gleann graph build`. In a binary built with `-tags kuzu`, `migrate` instead exports the existing Kùzu database read-only (`kuzu.OpenReadOnly`, `db.go:162`) into `symbols`, `edges`, `doc_nodes`, so no rebuild or plugin run is needed.
* **Memory entities are user-injected data and cannot be derived.** They are exported only by a `kuzu` build. The lite release prints an instruction and keeps `<name>_memory` untouched.
* The file hash store (bbolt) is replaced by `files.graph_hash`; it is not migrated (a missing value means "rebuild this file's graph rows").
* Kùzu directories and `_graph_file_hashes.db` are never deleted by `migrate`; `index remove` still removes them (`searcher.go:953-965`).

**Vault tracker and conversations** stay as they are in this RFC (open point 9.5, with a recommendation to move the vault tracker to SQLite so bbolt can leave `go.mod`).

### 6.5 Referential integrity on sync (RFC 001 and AGENTS.md section 2.3)

`ApplyFileDelta(file, symbols, edges)` runs in one transaction together with the file's passages and its `files` row:

1. Before deleting, read into memory the edges whose `dst` is declared in `file` and whose `file` is a different file (the port of `IncomingEdges`, `incremental.go:23`): `SELECT ... FROM edges WHERE dst IN (SELECT fqn FROM symbols WHERE file = ?) AND file <> ?`.
2. Delete the file's own rows and the inbound edges that point at its old symbols (no foreign key cascades them, so this is explicit): `DELETE FROM edges WHERE dst IN (SELECT fqn FROM symbols WHERE file = ?) AND file <> ?`, then `DELETE FROM edges WHERE file = ?`, then `DELETE FROM symbols WHERE file = ?`. The first statement must run before the `symbols` delete, because it reads `symbols`.
3. Insert the new symbols and edges.
4. For each inbound edge read in step 1: if its `dst` exists in `symbols` after step 3, re-insert it (port of `ExistingSymbolFQNs`, `incremental.go:111`); otherwise leave it deleted (step 2 already removed it, so no dangling edge remains) and set `files.graph_dirty = 1` on the edge's own `file`, so the next sync re-resolves that file.
5. Commit. If any step fails the transaction rolls back and `files.hash` does not advance.

**Consumer of `graph_dirty`.** At the start of every `index sync` (`internal/indexing/sync.go`), the set of files to process is the changed files plus `SELECT path FROM files WHERE graph_dirty = 1` (served by the partial index `files_dirty`, 3.4). Each such file goes through `ApplyFileDelta` again even if its content hash is unchanged; the same transaction sets `graph_dirty = 0` on success.

Edges with `dst_external = 1` have no row in `symbols`, so steps 1 and 4 skip them: they are retracted only with their owning file in step 2.

`PRIMARY KEY (src, dst, kind, file)` makes duplicate edge inserts an `INSERT OR IGNORE`, which removes the Kùzu `duplicated primary key` failure mode of RFC 001 for edges. A symbol `fqn` declared in two different files (for example build-tag variants) violates the primary key of `symbols`; that case is reported as an error by `ApplyFileDelta` rather than ignored, and the extraction rule that avoids it belongs to RFC 001. Reads need no rebuild step after a commit, because they run against the tables (3.7).

### 6.6 Release sequencing

| Release | Ships | Kùzu | bbolt |
|---|---|---|---|
| **R1** (next minor after v1.9.0) | `internal/store`, format 2 for new indexes with the **complete schema** (graph and document tables created, empty), FTS5 lexical leg, stable IDs, `index migrate`, memory on SQLite, embedding cache kept as files with per-batch writes. Graph code unchanged. | default (`treesitter`) | legacy reader for format-1 passages; vault tracker and graph hash store unchanged |
| **R2** | `internal/graphstore` in every build (SQL and recursive CTEs, no CSR), extractor types moved out of the tagged package, graph tables populated, Kùzu moved to `-tags kuzu` and a release asset built with it for migration; REST raw Cypher and the UI option retired | `-tags kuzu` only | unchanged |
| **R3** | Kùzu code and `go-kuzu` removed; `legacy_bbolt.go` removed; `.v1` cleanup | removed | removed from the core path (vault tracker per open point 9.5) |

R1 and R2 can be reordered if WP-G1 is ready first, but R2 must not ship before R1's `files` table exists, because `ApplyFileDelta` uses it. A global SQLite embedding cache is not in any of the three releases (3.3, open question 9.16).

---

## 7. Test and verification matrix

### 7.1 Tests

| Layer | What |
|---|---|
| Unit, `internal/store` | Open and pragma application; `ApplySource` replaces a source atomically (kill the process mid-transaction in a child test and reopen); `pid` stability when a line is inserted at the top of a file; `RemoveSource` leaves no row in `passages`, `lex` or `files`; a file with more than 1,000 passages is applied in one transaction and a kill mid-transaction leaves the old passages intact; `Lexical.Rebuild` after an analyzer change; contentless delete and `INSERT OR REPLACE` as regression tests (measured once by hand, 1.1); an empty query and a query of only stop words return no results without an SQL error; a token containing `"` does not break the expression; `ReassignSlots` after a simulated compaction; two writer processes and a reader (re-run of the spike writers test as a regular test); the journal-mode fallback on a network file system (mocked file-system type). |
| Unit, `internal/analysis` | Golden tokens for camelCase, letter-digit, stop words, punctuation; Turkish rules only when `language = tr` and `kind = prose`, and `ID` in a code passage of a `tr` index stays `id`; query-side two-form emission; ASCII variants; CJK uni- and bigrams; same output as `bm25.Tokenize` for English text (so gate 1 numbers stay valid). |
| Unit, `internal/graphstore` | Recursive-CTE answers equal a reference breadth-first search on random graphs (including cycles); `ApplyFileDelta` rollback; inbound edges to a retracted symbol are deleted, restored when the symbol reappears, and set `graph_dirty` on the source file when it does not; the next sync re-extracts dirty files and clears the flag; an external target is retracted with its referencing file; no edge with a missing internal end after any sequence of deltas. |
| Searcher | With a persisted scorer, `Load` does not iterate passages (assert zero `ForEachPassage` calls) and ignores `MaxBM25Passages`. |
| Migration | Fixture format-1 index (bbolt, manifest, vector file) migrates; search results before and after are the same list for the 391 tasks' queries with dense search off; rollback restores byte-identical old files; migrate of an index with non-dense IDs (the health check's failure case, `index_health.go:73`) is refused with a message; a v1.9.0 binary's search on a migrated index fails on the guard (its sync rebuilds the index as format 1 from source, measured; see 6.4 step 4); memory migration killed at each of its five steps ends in a complete `memory.sqlite` or a clean retry, and two processes starting at once migrate once. |
| Windows (CI) | Index directory rename (`withIndexRollback`, migrate switch, rollback) succeeds after all handles are closed and fails with a clear message when another process holds the index; the `LockFileEx` lock is released when the holder is killed. Not run in this review; windows is the platform where these differ. |
| Build tags | `go build ./cmd/gleann`, `-tags eif`, `-tags native`, `-tags faiss`, `-tags treesitter`, `-tags kuzu` each succeed (`native` and `faiss` where CI has the Rust toolchain and libfaiss; whether it does was not verified); the stub for `kuzu` off returns a descriptive error (AGENTS.md section 4.3). |

### 7.2 Gates (reversal conditions, from D4)

The decision is reversed, and `store.Lexical` is implemented as a postings file with bbolt kept for passages, if at the first milestone (step 3 of section 8, about one week in):

1. hybrid `gleann bench` on the 391 tasks loses more than the noise band: **MRR below 0.48** (the default hybrid band is 0.48-0.50; RFC 002 records hybrid MRR 0.485), or
2. the binary does not run correctly on **windows/amd64** or **linux/arm64** in CI.

Further gates for the rest of the work (not reversal conditions):

* Gate 1 is re-run with the final FTS5 tokenizer setting (4.1 item 5).
* XQuAD-tr / TurHistQuad paired bench for the Turkish rules (WP-2.4).
* Binary size delta of the real binary against 72.8 MB unstripped.

### 7.3 E2E cases (`gleann-rag-test/e2e/TEST_MATRIX.md`)

| ID | Case | Provided by |
|---|---|---|
| IDX-05 | one changed file embeds O(changed) texts | Vector segments, WP-2.2 (separate RFC). This RFC supplies per-file transactions and stable IDs. |
| IDX-06 | passage IDs stable across builds | Step 4 (stable IDs); the `Known WP2.1` marker is removed there. |
| IDX-07 | Turkish case folding (`IŞIKLARI` matches `ışıkları`) | Step 2 (analyzer). |
| IDX-08 | CJK substring lexical match | Step 2 (analyzer, bigrams). |
| IDX-10 | kill at each build phase; old index intact; re-run resumes | Per-file transactions in step 4; kill points and build directory from WP-2.3. |
| IDX-11 | sync of one file at 100K passages: wall time and embed calls in budget | Needs WP-2.2; the lexical and passage part is measured here. |
| IDX-14 | cold open of a 100K index within time and RSS budget | Step 3. Today: 4.4 s load and 493 MB heap for BM25 alone at 100K; target taken from the measured FTS5 rows (cold open 44 ms, 28 MB process at 100K). |
| MEM-04 | five concurrent writer processes (5 writers x 50 writes, WP-4.8) | Step 6. |
| MEM-06 | scope isolation between two git repos | `scope` and `repo` columns, step 6. |
| GRAPH-01..03 | callers; builtins are not symbols; rename leaves no orphan edge | Step 7, on the lite build. GRAPH-02 also depends on RFC 001 extraction rules. |
| GRAPH-04 | fixture repos per language | RFC 001 / WP-G3, G4; this RFC provides the store. |
| GRAPH-05 | graph commands work in the lite build and on windows | Step 7. |
| GRAPH-06 | sync failure leaves graph and manifest unchanged | Step 7 (single transaction, section 6.5). |

### 7.4 Scale benchmark

At 100K and 1M passages, on a corpus with a natural term distribution as well as the replicated one (the replicated corpus is a pessimistic case): cold open, p50 and p95, heap and peak RSS, build time, disk, for the new store against the numbers in 1.1; sync of one file; 4 writers plus a reader. Include linux/arm64 and windows/amd64 (and a C-SQLite build if the cgo tag is added). This is the WP-1.4 benchmark and the evidence for the document-frequency cut.

---

## 8. Phased implementation plan

Each step is a separate small commit series on a feature branch (`feature/storage`), shippable on its own, behind the format version where it changes files.

| Step | Work | Ships | Verified by |
|---|---|---|---|
| 0 | Add `modernc.org/sqlite` to the root `go.mod` (pinned to v1.59.0, the newest release with a `go 1.25` directive; v1.60 and later require Go 1.26. The spike measured v1.62.0; the store tests re-check the FTS5 behaviour on v1.59.0); CI job that builds and runs a small SQLite and FTS5 program on windows/amd64 and linux/arm64 (arm64 runner or emulation: **open**, availability not verified). | Nothing user-visible | The CI job is green, or the reversal gate fires early. |
| 1 | `internal/store` core: open, pragmas, migrations, `meta`, `files`, `passages` with `ApplySource`, per-index lock file. Not wired. | No | Unit tests, writers test. |
| 2 | `internal/analysis` (moved tokenizer, Turkish rules by language, ASCII variants, CJK bigrams) and `Lexical` over FTS5. | No | Analyzer unit tests; IDX-07, IDX-08 at unit level. |
| 3 | **Gate step.** Feed `Lexical` from the old bbolt passages (same IDs) and expose it as a `TopKScorer` through the `PersistedScorer` path, so `Searcher.Load` skips the in-RAM BM25 build (3.1); behind a config switch. Run `gleann bench` on the 391 tasks and the scale benchmark. | Hidden switch | Gates 7.2 (MRR 0.48, windows/amd64, linux/arm64); IDX-14 (cold open must not include the 4.4 s tokenising pass). |
| 4 | Passage store switch: format 2 for new indexes with the complete schema, `pid`, stable IDs, per-file transactions (one per source file), `ReassignSlots`, `legacy_bbolt.go`, `gleann index migrate` with guard files, doctor changes, remove `MaxBM25Passages` cut. | **R1** | Migration tests including the v1.9.0 guard check; IDX-06; IDX-10 (with WP-2.3 hooks). |
| 5 | Embedding cache stays as files; `Compute` writes per batch instead of after the whole inner call (`cache.go:128-140`). | R1 | Cache unit tests; a build killed halfway resumes with fewer embed calls. |
| 6 | Memory on SQLite: tables, import of `memory.db` through the locked, verified `.tmp` copy (6.4), `Add` in one transaction, no mandatory daemon (WP-4.8); recall by FTS is WP-4.1 and not here. | R1 | MEM-04, MEM-06; migration kill test. |
| 7 | `internal/graphstore`: populate the graph tables, SQL and recursive CTEs, `ApplyFileDelta`, port of the consumers in 6.2, entity tables; extractor types moved out of the tagged package (with RFC 001 owners). No CSR. | **R2** | GRAPH-01..06 on the lite build. |
| 8 | *(v1.20: merged into step 9, no bridge release; see RFC 004)* Kùzu to `-tags kuzu`; export-to-SQLite in `migrate`; REST raw Cypher retired together with the `Graph.tsx` option; AGENTS.md section 1.3 updated. | R2 | Build-tag matrix; migration from a real Kùzu database. |
| 9 | *(done in v1.20.0, RFC 004 A3/B2)* Remove Kùzu, `go-kuzu`, `legacy_bbolt.go`, `.v1` cleanup. | **R3** | `go mod tidy` shows no kuzu; build matrix. |
| 10 | Optional: `-tags sqlitecgo`, AND-first query and df cut if the scale benchmark requires them, field weights if the paired bench improves, a CSR (3.7) or a global SQLite embedding cache (3.3) only if a measurement shows a need. | As needed | Scale benchmark; paired bench. |

Step 3 is the milestone that decides whether steps 4 onward continue as written.

---

## 9. Open questions

Answered by the audit and removed from this list: contentless deletes (measured, 1.1), the single-writer lock strategy (OS locks, 3.6), CSR refresh (no CSR, 3.7), the REST/UI Cypher dependency check moved into 9.3.

1. **Implicit migration.** Should `index sync` of a format-1 index migrate automatically (with a backup), or require an explicit `gleann index migrate`? MCP watchers and `auto_watch` indexes run syncs unattended.
2. **Stable ID definition.** Is `sha256(source, sha256(text), dup_index)` right, or should `(source, chunk_no)` be the ID with a content hash as a separate column? The first keeps IDs across insertions; the second is simpler. Do chunk texts carry context that changes when neighbours change?
3. **Raw Cypher over REST and in the web UI.** `POST /api/graph/{name}/query` with `type: "cypher"` (`graph_handler.go:247`) and the "Raw Cypher" option in `ui/src/components/Graph.tsx:120` are the user-visible Cypher. Retire both together, or provide a different escape hatch (a structured symbol and path query)?
4. **Entity graph and orphans.** Keep the memory entity graph and its three MCP tools at all, given that WP-4.x reworks memory? If kept, per-namespace rows in the global memory database (as drafted) or one file per namespace? Should `index remove` delete `<name>_memory` and `<name>_memory_vectors` (they are currently left behind, 6.1)?
5. **Vault tracker and conversations.** The conversation JSON files are out of scope. Recommendation (audit finding 24, awaiting maintainer decision): move the vault tracker (`vault.db`, bbolt) to a small SQLite table in R2 or R3 so that `go.etcd.io/bbolt` leaves `go.mod`; otherwise bbolt stays after R3 for that one use.
6. **Analyzer kind and language.** The language is per index and the Turkish rules apply to `prose` passages only (4.1). How is `kind` assigned for ambiguous files (markdown with code, comments in code, notebooks)? Is the two-form query (base plus Turkish-folded) accurate enough on the XQuAD-tr / TurHistQuad bench, or is a per-passage language needed?
7. **FTS5 tokenizer setting.** `remove_diacritics 0` against the default; gate 1 used the default. Re-run decides.
8. **Document-frequency cut and AND-first.** Cut value and the effect on recall; neither was measured. Are they needed before 1M, or only at scale?
9. **Network file systems.** The store falls back to `journal_mode = DELETE` with a warning (3.6). Open: which detection works on each platform (linux and darwin `statfs`, windows UNC and remote drive), and should a detected network file system be refused instead for the memory database, where several agent processes write?
10. **Symbols table.** `DECLARES` as a column or a table for multi-file declarations? Do external targets need rows of their own (an `external_symbols(file, fqn)` table owned by the referencing file) or only the `dst_external` flag on edges, or a count only, as RFC 001 suggests for unresolved calls? Both depend on RFC 001; this RFC should not freeze them before the contributor of issue #12 has reviewed.
11. **Kùzu bridge release.** Ship a `kuzu`-tagged binary as a release asset in R2 so lite users can export their entities, or document building from source?
12. **arm64 CI.** Which runner: native arm64, or emulation? Availability and cost were not verified.
13. **Pragmas.** `synchronous = NORMAL` against `FULL` for the memory database, where losing the last write on power failure may matter more than for a rebuildable index. `cache_size` and `mmap_size` from the scale benchmark. WAL size after `wal_checkpoint(PASSIVE)` under long readers.
14. **Go version.** The spike ran on Go 1.24.2; the module declares 1.25.9. Re-run the benchmarks on the declared version before the numbers in this document are used as budgets.
15. **Windows behaviour to verify in CI.** Directory and file rename after closing handles (`withIndexRollback`, migrate switch, rollback), the `LockFileEx` lock released on process death, and the guard file against an older binary. These follow from Windows file semantics and were not run here (7.1).
16. **Pending maintainer confirmation.** (a) Graph through SQL and recursive CTEs only, no CSR (audit finding 12, 3.7): confirm, or restore the CSR with the generation-counter refresh described in 3.7. (b) Embedding cache stays as per-vector files in R1 with per-batch writes (audit finding 13, 3.3): confirm, and decide what measurement (many small files against one SQLite file under parallel builds) would justify a global `embeddings.sqlite` later.

---

## 10. Audit log

* **Date:** 2026-11-16
* **Auditor:** Gemini 3.8 Flash (agy), 25 findings
* **Verifier:** orchestrator (each finding checked against this text and the code at main, or by running SQLite on modernc.org/sqlite v1.62.0)

| # | Finding | Verdict | What changed in the RFC |
|---|---|---|---|
| 1 | `blocks` is WITHOUT ROWID but `blocks_lex` uses its rowid | Confirmed | 3.5: integer `seq` primary key, `id` unique, table not WITHOUT ROWID |
| 2 | Inbound edges to retracted symbols are never deleted | Confirmed | 6.5: explicit delete of inbound edges before the symbols delete, restore or mark dirty |
| 3 | External stubs with `file = ''` collide and are never retracted | Confirmed | 3.4: `symbols` first-party only, `edges.dst_external`; 6.5; open question 9.10 |
| 4 | "Per 1,000 passages" splits a source across transactions | Confirmed in part | 3.6: one transaction per source file whatever its size. The stronger claim (the manifest must not advance per file across a multi-file sync) was **not accepted**: committing hash, data and graph delta together per file satisfies AGENTS.md 2.3 for each file and keeps builds resumable; holding all hashes until the whole sync succeeds would discard the progress of every other file and needs one long transaction. Explained in 3.6. |
| 5 | No way to update `vec_slot` after compaction | Confirmed | 3.2 `ReassignSlots`; 3.4 publication order and rule that compaction waits for it |
| 6 | Older binary creates an empty bbolt store at the renamed name and wipes the vectors | Confirmed, plausible | 3.3, 6.3, 6.4 step 4: guard file at the old names; v1.9.0 check in 7.1 |
| 7 | Memory migration can be raced or interrupted into a truncated store | Accepted | 6.4: locked copy into `.tmp`, verify counts, rename, then retire the old file with a guard |
| 8 | Windows rename fails with open handles | Accepted | 3.6 (lock beside the directory, renames only without handles), 6.4, 7.1 Windows test, 9.15 |
| 9 | WAL on network file systems | Accepted | 3.6: detect and fall back to `journal_mode = DELETE` with a warning; 9.9 |
| 10 | `Searcher.Load` still tokenises all passages when a scorer is set | Confirmed | 3.1 `PersistedScorer`, step 3 of section 8, test in 7.1 |
| 11 | Empty query gives an FTS5 syntax error; quotes in tokens | Confirmed by running | 1.1 measured fact, 4.2 step 1, tests in 7.1 |
| 12 | Drop the in-memory CSR; use recursive CTEs | Accepted as recommendation, maintainer decision | 2.1, 3.1, 3.2, new 3.7, 5, 6.2, 6.5, section 8; CSR kept as a possible later optimisation; **awaits the maintainer's confirmation** (9.16) |
| 13 | Keep the file embedding cache and fix the write timing | Accepted as recommendation, maintainer decision | 2.1, 3.1, 3.3, 6.4, 6.6, section 8; global SQLite cache listed as a later option; **awaits the maintainer's confirmation** (9.16) |
| 14 | The web UI offers "Raw Cypher" | Confirmed | 6.2, section 8 step 8, 9.3 |
| 15 | `RemoveIndex` leaves `<name>_memory` and `<name>_memory_vectors` | Confirmed | 6.1 inventory corrected; independent fix noted; 9.4 |
| 16 | `go_calls.go` already returns structs; only the comment says Cypher | Confirmed | 5 Extractors, 6.2 writers row (the real blocker is the tagged `kuzu` package that defines the types) |
| 17 | Turkish folding per index would change code identifiers | Accepted | 3.4 `passages.kind`, 4.1 item 3 (rules for prose only, two-form query), 9.6 |
| 18 | Ambiguous `graph_cgo.go` citation | Confirmed | 6.2: `internal/server/graph_cgo.go:125` and `:59-126` |
| 19 | Build matrix omits `native` and `faiss` | Confirmed | 7.1 build tags row |
| 20 | `wal_checkpoint(TRUNCATE)` blocks under readers | Accepted | 3.6: `PASSIVE` |
| 21 | `graph_dirty` needs an index and a consumer | Accepted | 3.4 partial index `files_dirty`; 6.5 consumer in `index sync` |
| 22 | OS file locks are released on process death | Accepted | 3.6: no heartbeat or stale-lock handling; question closed |
| 23 | `contentless_delete = 1` delete and replace work | Confirmed by running | 1.1, 4.1 item 1, 7.1; question closed |
| 24 | Move the vault tracker to SQLite to drop bbolt | Accepted as an option | Open question 9.5 with the recommendation |
| 25 | Format 2 in R1 should already create the graph tables | Accepted | 3.3, 3.4, 6.6: complete schema from R1, graph tables rebuildable |
