# RFC 010: Metadata filter indexes — filtered search without a full-table scan

Status: implemented in lane `filt-index` (port of the 2026-11-21 design onto 3bcf70f).
The schema change (new SQLite indexes on `passages`) was approved by the maintainer on
2026-11-21 ("if it is needed"). This RFC documents the design, the measurements and the
rejected alternatives.

## 1. Problem & Motivation

A metadata filter is evaluated against the `meta` JSON column of `passages`. The
format-2 pre-selection (WP-2.5, `preselectFilterIDsLimit`) compiles the filter to a SQL
WHERE clause and reads the matching rows — but `json_type`/`json_extract` have no index,
so SQLite evaluates them on EVERY row. The cost of a filtered query therefore follows
the size of the index, not the number of matches:

Measured on the baseline (3bcf70f, `bench/vectordb/cmd/filtbench`, hnsw, 1000 queries,
k=10, alpha=1.0, `taskset -c 12-15`, recall@10 / p50 ms / p95 ms):

| index          | all (no filter) | 1% filter       | 10% filter       | 50% filter     |
|----------------|-----------------|-----------------|------------------|----------------|
| 10K x 384      | 0.9996 / 9.8 / 11.2 | 1.0000 / 24.5 / 26.8 | 0.9998 / 30.2 / 32.4 | 0.9966 / 11.3 / 12.7 |
| 100K x 768     | 0.9950 / 12.3 / 15.7 | 1.0000 / 125.1 / 129.3 | 0.9958 / 191.2 / 209.0 | 0.9887 / 15.9 / 18.8 |

(Paired-run medians on the mandated E-core pin 12-15; full tables in section 7.)

- A CPU profile of the old session (11f4538, 100K x 384, 10% selectivity) attributed
  **81.6%** of query time to `preselectFilterIDsLimit` (68.5% `SelectByPredicate`,
  11.8% JSON unmarshal of the rows).
- On a REAL code index (31,071 passages, ~16 meta keys per passage) the scan costs
  **72–103 ms** per filter query even though the index is small: ~2.5 µs per row of
  JSON evaluation (old-session SQL probe, `exp/realprobe.sh`).
- Because the scan is so expensive, `filterExactWorthIt` refused exact scoring above
  `filterExactScanMax` (200,000 passages) altogether: on larger indexes a selective
  filter kept the approximate loop's result (recall loss) or paid the full scan in the
  cap fallback.

The fix must not cost anything when filters are unused, must not break older binaries
sharing the database, and must not need a migration.

## 2. Design

### 2.1 Index shape and naming

One plain SQLite **expression index per filtered meta key**:

```sql
CREATE INDEX passages_mf2_<hex(key)> ON passages(
    T(M, '$."<key>"'), json_extract(M, '$."<key>"'))
  WHERE T(M, '$."<key>"') IS NOT NULL
-- M = CASE WHEN json_valid(meta) THEN meta ELSE '{}' END
-- T = json_type, except that a value SQLite and Go decode differently has the
--     type 'odd' (section 2.2b)
```

- The index holds one entry per passage that HAS the key: the JSON type of the value
  and the value itself, so `key = value` is an index range (`INDEXED BY` + type/value
  predicates). `vec_slot` is deliberately NOT in the index (not covering): it is read
  from the table row, because a covering index makes slot (re)assignment 4.5x slower
  (old probe: UPDATE of `vec_slot` over 100K rows 158+124 ms without, 673+694 ms with
  one covering index).
- **Naming**: prefix `passages_mf2_` + the key in hex. The `2` versions the index
  definition: a later change of the indexed expressions takes a new prefix, so an
  index is never used with a query it was not built for. Hex keeps any key bytes safe
  in an identifier. Version 1 (plain `json_type`, no `odd` type) never reached a
  release and backward compatibility with databases of it is not required: no
  migration and no cleanup code exists for it.
- **Key safety** (`store.MetaIndexable`): an expression index is only used by a query
  that spells the SAME expression, so the JSON path cannot be a bound parameter — the
  key becomes part of the SQL text. Only letters, digits, `_`, `-`, `.` are accepted
  (Unicode letters included), at most 64 bytes, valid UTF-8: none of these can end the
  SQL string literal or the quoted JSON path label. Anything else falls back to the
  table pass (which binds the path as a parameter, as before).
- The `metaJSONExpr` guard (`CASE WHEN json_valid(meta) ...`) makes text that is not
  JSON count as `{}` — exactly what the Go engine sees (`json.Unmarshal` fails, the
  passage has no keys). Without it, `json_type` would raise "malformed JSON" and, once
  an index exists, fail every insert of such a row.

### 2.2 The damaged-rows index

SQLite and Go may decode the same meta text differently:

- text SQLite does not accept as JSON (Go accepts deeper nesting),
- a raw NUL byte: SQLite's JSON functions stop reading at it, so `{"lang":"go"}` + NUL
  + garbage is valid JSON with `lang=go` for SQLite, while `encoding/json` rejects the
  whole text and the engine sees no keys (the index would answer it as a sure match),
- strings with an escaped NUL (`\u0000`) or an escaped UTF-16 surrogate (`\uD83D...`):
  Go replaces a lone surrogate, SQLite keeps it.

The escaped-NUL check also keeps a key spelled `v\u0000` correct: SQLite reads it as key
`v` (plain type, not `odd`), so only the damaged range makes the engine verify it.
`encoding/json` writes none of these, so for metadata gleann wrote itself this set is empty.
A partial index lists exactly those rows:

```sql
CREATE INDEX passages_mfbad1 ON passages(id)
  WHERE NOT json_valid(meta) OR instr(CAST(meta AS BLOB), X'00') > 0 OR meta GLOB '*\u0000*' OR meta GLOB '*\u[dD][89a-fA-F]*'
```

Every compiled filter adds a branch over this index; its rows come back with their
`source`/`meta` and are ALWAYS verified by the Go filter engine, whatever the key
indexes said about them. The index self-heals: `MetaIndexes` counts a key index only
together with `passages_mfbad1`, and `EnsureMetaIndexes` recreates the damaged index
should it be gone.

### 2.2b Values SQLite and Go decode differently

Found by a differential test (`TestPreselectIndexed_DecodeFamily`, 58 failing filters
before the fix) over JSON that SQLite accepts (`json_valid` = 1) but that `encoding/json`
reads differently. They cannot be put in the damaged-rows index (a partial-index
predicate sees the whole text, not one key), so they are told apart per key:

| input | Go | SQLite | handling |
|---|---|---|---|
| number outside float64 (`1e400`, `-1e400`, 400 digits) | key present, value `nil` (`UnmarshalTypeError`, decoding goes on) | `real`/`integer`, `json_extract` = +-Inf | type `odd` (`json_extract IN (9e999,-9e999)`) |
| key twice in one object (`{"v":"a","v":"b"}`, also with a `\u0076` label) | last value wins | first value wins | type `odd` (`json_type(json_remove(M,P),P) IS NOT NULL`: `json_remove` deletes the first occurrence only) |
| string with bytes that are not UTF-8 | each bad byte reads as U+FFFD | bytes kept | by needle, not by row (below) |

Every eq/in key range set carries an unverified `odd` range (`exists` already covers the
key whatever its type). For the UTF-8 case no SQL predicate can find the rows, so the
needle decides: a needle that is not valid UTF-8 can never equal a decoded string and
is left out of the text range (an empty value list would select every text value, so
no branch is emitted when nothing is left); a needle containing U+FFFD makes the text
range of the key unverified (all text values are checked by the engine). Numbers that
do fit float64 (`9007199254740993`, 50 digits, `1E5`, `-0`, `1e-400`) already agree
with the engine or are verified (every non-small integer and every real is). The table
pass had the same exposure for duplicate keys and bad bytes (unchanged since v1.20.2)
and got the same two rules (`candidateIsText`, `valueNeedlesDecodeApart`).

Also fixed on the way: `storePassageToGleann` panicked on a meta text of `null`
(valid JSON, resets the map to nil); gleann never writes it.

### 2.3 When indexes are created

- **On demand**: the first search with an answerable filter (`eq`, `in`,
  `exists(true)`) on a meta key creates that key's index — one pass over the passages,
  once per key and database — when the index has at most `filterExactScanMax`
  (200,000) passages. Creation cost: one unindexed filter query's worth of work
  (measured: section 7).
- **Above 200,000 passages** only indexes that ALREADY exist are used; a missing one
  is not created by a search (the first pass over the table is the caller's decision).
  `LeannSearcher.EnsureFilterIndexes(ctx, fields...)` is the explicit opt-in
  (`filtbench -filter-index` uses it).
- **`source` filters need no filter index**: the `passages_source` index every
  database has answers them at any size. A passage WITHOUT a source column resolves
  `source` through its meta JSON (`source` key, then `file` key); those rows are
  nominated by the `source = ''` range and verified by the engine.
- **`kind`/`type`** stand in for each other: a filter on either indexes both keys; the
  alias's ranges are verification-only (the alias decides only when the field itself
  is absent).
- `EnsureMetaIndexes` runs on a connection of its own with `busy_timeout(0)` and
  `BEGIN IMMEDIATE`: while a build or sync holds the write lock it fails AT ONCE
  (SQLITE_BUSY) and the query is answered without the index — a search never queues
  behind a build. When every index exists it is a read-only no-op.

### 2.4 The cap

`store.MaxMetaIndexes = 16` key indexes per database. Field names reach
`EnsureMetaIndexes` from search requests, and every index costs one `json_type` + one
`json_extract` per written passage plus ~19 bytes per passage that has the key, so the
number must be bounded. The cap is checked BEFORE taking the write lock (a database
full of filter indexes refuses without write-lock traffic per query) and re-checked
UNDER the lock (two processes cannot both add the last allowed index). Over the cap:
clean error, the query falls back to the table pass (or the loop).

Write cost of the cap's extremes (BenchmarkApplySourceMetaIndexes, 200 files x 20
passages with the metadata shape of a code index, one op = 4000 passages):

| filter indexes | ns/op       | vs 0 indexes |
|----------------|-------------|--------------|
| 0              | 216,965,395 | —            |
| 1 (+ damaged)  | 290,706,553 | +34%         |
| 4 (+ damaged)  | 380,660,442 | +75%         |
| 16 (+ damaged) | 498,816,495 | +130%        |

### 2.5 Sure and maybe ranges (the query path)

`compileFilterIndex` compiles one filter to index ranges that together are a SUPERSET
of the engine's matches. Each range is either **sure** (its passages match whatever
the rest of their metadata says) or **maybe** (read with `source`/`meta`, verified by
the engine). What is sure follows from how the engine compares — `fmt.Sprint` of the
`json.Unmarshal`-decoded value against `fmt.Sprint` of the filter value:

- JSON **text** equal to the needle: sure.
- **true / false / null** (print as `true` / `false` / `<nil>`): sure.
- JSON **integers** with at most 6 digits, non-zero: sure. `fmt.Sprint` of a float64
  uses `%g`: plain digits up to 999999, exponent form from 1e+06 on
  (`1234567` prints as `1.234567e+06`), and `-0` prints as `-0` while SQLite reads 0 —
  so longer digit runs, zero and `-0` are maybe.
- any other **number** (real, big/zero integer): maybe, and only when the needle can
  be a printed number at all (digits, sign, point, exponent).
- **arrays / objects** (print as `[...]` / `map[...]`): maybe, only for a needle of
  that shape.

`preselectIndexed` reads each filter's ranges with `SelectByMetaIndex` (one SELECT per
branch, `INDEXED BY` — the statement FAILS instead of silently scanning when an index
is missing — joined by `UNION ALL`, `limit+1` rows), then combines the sets in Go:
`and` = intersection, `or` = union. A passage every (any) set is sure about is a
match; every other candidate and every damaged row is verified by the engine. More
sure passages than the limit proves the filter not selective without a table pass.
Readers check `sqlite_master` per query (`MetaIndexes`), so an index another process
dropped is never trusted.

`preselectFilterIDsLimit` gained a `tablePass` argument: try the filter indexes first;
when they cannot answer, the old table pass (`preselectScan`) runs only if `tablePass`
allows it. `filterFewMatches` now returns `(few, tablePass)` and `filterExactWorthIt`
no longer refuses large indexes: with a filter index, exact scoring of few matches
runs at ANY size; above 200K without one, the loop's result stands as before.

### 2.6 Old binaries with these indexes present

- SQLite maintains an expression index on EVERY write to `passages`, in the writer's
  own transaction, whatever binary does the write: an older gleann that knows nothing
  about filter indexes keeps them exact (plain INSERT/UPDATE/DELETE; `ReplaceAll`'s
  `DELETE FROM passages` keeps the truncate optimisation). No triggers are needed.
- Old readers never USE the indexes (their statements spell other expressions); they
  keep scanning — their behaviour is unchanged.
- A binary that rebuilds the database file from scratch simply loses the indexes; the
  next filtered query creates them again.
- Dropping one is always safe: readers check which exist per query and fall back.

## 3. Build tag & platform impact

None. Everything is pure Go over the bundled `modernc.org/sqlite` (expression indexes,
partial indexes, `json_valid`/`json_type`/`json_extract`, `GLOB` are all part of its
SQLite build). No CGo, no build tags, no platform-specific code; the pure-Go
`CGO_ENABLED=0` build and `GOOS=windows` vet are part of the gates (section 5).

## 4. Incremental & migration strategy

**No migration, no `store_format` change.** The indexes are not part of the schema
history: they exist only for keys somebody filtered on, so an index database that is
never filtered carries none and stays byte-for-byte what it was. Once created, SQLite
maintains them on every write path (`ApplySource`, `RemoveSource`, `ReassignSlots`,
`ReplaceAll`, plain SQL from other processes) inside the same transaction as the
passage write — atomic by construction, no dangling state on rollback. Incremental
sync needs no new step: a changed file's rows are replaced in one transaction and the
indexes follow. An old binary writing the database keeps them exact (2.6); a rebuild
from scratch loses them and the next filtered query recreates them.

## 5. Test & verification matrix

| test (package) | proves |
|---|---|
| `TestMetaIndexable` (store) | key charset/length gate; injection shapes refused |
| `TestOpenCreatesNoFilterIndex` (store) | no indexes without a request; `store_format` stays "2"; `INDEXED BY` refuses without the index |
| `TestMetaIndexMaintainedByEveryWriter` (store) | index == table scan after ApplySource/RemoveSource/slot changes/plain SQL insert-update-delete/ReplaceAll; damaged-index drop + self-heal; no-op Ensure needs no write lock |
| `TestSelectByMetaIndexReadsIndexesOnly` (store) | EXPLAIN QUERY PLAN: every branch reads an index, never the table; limit works |
| `TestMetaIndexDamagedMeta` (store) | malformed meta still writable with indexes present; absent from key indexes; damaged range returns exactly the NUL/surrogate/non-JSON rows |
| `TestMetaIndexOddValues` (store) | `odd` type holds exactly the overflow numbers and duplicate keys; `"9e999"` stays text |
| `TestEnsureMetaIndexesLimitAndKeys` (store) | cap 16 enforced, refused requests leave nothing behind, bad key refused |
| `TestEnsureMetaIndexesDoesNotWaitForAWriter` (store) | fails at once (<3s) while another tx holds the write lock |
| `BenchmarkApplySourceMetaIndexes` (store) | write cost 0/1/4/16 indexes (section 2.4) |
| `TestPreselectIndexed_MatchesEngineAndScan` (gleann) | 720 answerable filters across every operator/field/JSON-type shape: index set == engine set == scan set; unanswerable filters say so |
| `TestPreselectIndexed_ScanIdentityOnCleanMetadata` (gleann) | 420 filters: identical passages AND rowids on both paths |
| `TestPreselectIndexed_DecodeFamily` (gleann) | 60 metas SQLite/Go decode apart x ~50 needles x eq/in/exists/ne/nin/contains/startswith/endswith: index set == engine set == table pass |
| `TestPreselectIndexed_DecodeFamilyRawNUL` (gleann) | metas with a raw NUL byte: index path == engine |
| `TestFilteredSearch_RawNULMeta` (gleann) | a passage whose meta is valid JSON + raw NUL is not returned by a filtered Search, with and without a filter index |
| `TestCompileFilterIndex_OddNeedles` (gleann) | bad-UTF-8 needle makes no empty text branch; U+FFFD needle verifies all text; `odd` range always present and unverified |
| `TestFilteredSearch_LargeIndexCreatesNoFilterIndex` (gleann) | 200K+ passages: a filtered search creates no index and scores nothing exactly; after `EnsureFilterIndexes` it scores exactly |
| `TestCompileFilterIndex_SureRanges` (gleann) | value-by-value sure/maybe pin incl. the 1e+06 `%g` case, `-0`, big ints, surrogates |
| `TestFilteredSearch_CreatesFilterIndexOnDemand` (gleann) | first selective eq search creates exactly its field's index; unfiltered/contains/source searches create none; results identical before/after |
| `TestPreselectFilterIDs_SourceKeyOfSourcelessPassage` (gleann) | source/file resolution chain of a sourceless passage (pre-existing gap fixed) |
| `TestPreselect_LargeIndexUsesExistingFilterIndexOnly` (gleann) | no table pass and no creation without leave; source answered at any size; EnsureFilterIndexes opt-in; not-selective proof |
| `TestPreselect_ConcurrentOnDemandCreation` (gleann, -race) | 12 workers race to create indexes; every answer exact; indexes exist afterwards |
| `TestFilterExactWorthIt` (gleann) | size no longer caps exact scoring (1 of 1000 in 1M: true) |
| existing WP-2.5 / diskann / preselect suites | unchanged behaviour of the loop, tiers and normalizer |

Red proof (base sources, new tests): `filterExactWorthIt(1,1000,1000000)=false want
true`; `0 filter indexes for lang, want 1`; sourceless `source eq b.go` pre-selection
2 vs engine 3 — all fail on 3bcf70f without the source changes, pass with them.

Gates: `gofmt -l` clean; `go vet ./...` clean on linux and `GOOS=windows`;
staticcheck on touched packages clean (one pre-existing SA5011 in
`searcher_extended_test.go`, confirmed on pristine 3bcf70f); `CGO_ENABLED=0 go build
./cmd/gleann`; `go test -race -count=1` on `internal/store`, `pkg/gleann`,
`internal/server`, `internal/mcp` (8G memory cap, cores 12-15) — all green.

Measurements: paired A/B filtbench runs (baseline binary built from pristine 3bcf70f)
at 10K x 384 and 100K x 768, index creation cost/size, write cost, recall gate
(`bench/vectordb/gate`) — section 7.

## 6. Rejected alternatives (old-session probes, 2026-11-21)

1. **Side table `passage_meta_keys` + triggers, ALL keys.** Fast queries (ids-only
   0.27 / 2.6 / 13.2 ms at 1/10/50% on the 100K bench db) but: backfill 1.11 s and
   **+8.1 MB (+28%)**, 400K rows on the bench db; on the REAL code index (31,071
   passages, ~16 keys/passage) backfill 1.97 s and **+18.2 MB (+31% disk)**, 490K rows.
   Every passage write pays for every key, triggers sit on the write path, and it
   needs a migration + `store_format` change. A key-registry variant (only registered
   keys indexed, registration on first use) removes the zero-use cost but keeps
   triggers and the migration. Rejected for on-demand expression indexes: SQLite
   maintains them natively (old binaries too), zero disk when filters are unused.
2. **Generated VIRTUAL column + index.** Same index bytes and speed as the expression
   index (it IS the same index), but `ALTER TABLE` changes the `passages` table
   definition — older binaries then see an extra column. Rejected.
3. **Covering index (vec_slot included).** Makes slot (re)assignment 4.5x slower
   (100K-row `vec_slot` UPDATE: 158+124 ms without vs 673+694 ms with one covering
   index). Rejected; `vec_slot` comes from the table row.

## 7. Measurements (base 3bcf70f, this lane)

Setup: `bench/vectordb/cmd/filtbench`, hnsw, 1000 queries, k=10, alpha=1.0, synthetic
dataset (seed 42), exact ground truth; baseline binary built from a pristine
`git archive 3bcf70f` tree, new binary from the ported worktree; paired A/B runs on
identical index copies (the new binary's copy gains the filter indexes on demand),
every run `systemd-run --user --scope -q -p MemoryMax=8G taskset -c 12-15`.
The 10K x 384 and 100K x 384 indexes are copies of the old session's pristine builds;
the 100K x 768 index was built fresh by the baseline binary (4m29s on P-cores).

**Core pinning note:** on this i9-13900H, cpus 12-15 are E-cores (4.1 GHz) while
0-11 are P-cores (5.2-5.4 GHz). The mandated pin 12-15 therefore makes every absolute
latency ~1.7-2x the old session's (which pinned 0-15); other lanes were active on
cores 16-19 during the runs. The paired base/new comparison on the SAME cores is the
meaningful number. Recall is identical between base and new in every row below.

### 7.1 Paired A/B (p50 ms, median of the pairs; recall@10 base = new)

10K x 384, 3 pairs:

| selectivity | recall | base p50 (pairs)      | new p50 (pairs)      | speedup |
|-------------|--------|-----------------------|----------------------|---------|
| all         | 0.9996 | 9.78 (9.69-9.80)      | 9.75 (9.71-9.90)     | 1.0x    |
| 1%          | 1.0000 | 24.54 (24.50-24.66)   | 14.27 (14.11-14.31)  | 1.7x    |
| 10%         | 0.9998 | 30.21 (30.18-30.31)   | 17.44 (17.33-17.68)  | 1.7x    |
| 50%         | 0.9966 | 11.29 (11.18-11.34)   | 11.53 (11.22-11.55)  | 1.0x    |

100K x 768, 3 pairs:

| selectivity | recall | base p50 (pairs)        | new p50 (pairs)       | speedup |
|-------------|--------|-------------------------|-----------------------|---------|
| all         | 0.9950 | 12.31 (12.23-12.43)     | 12.44 (12.42-12.55)   | 1.0x    |
| 1%          | 1.0000 | 125.11 (124.91-125.53)  | 26.30 (26.17-26.51)   | 4.8x    |
| 10%         | 0.9958 | 191.24 (191.09-192.11)  | 67.27 (66.94-67.95)   | 2.8x    |
| 50%         | 0.9887 | 15.87 (15.86-15.89)     | 16.15 (15.88-16.31)   | 1.0x    |

100K x 384, 2 pairs (comparison shape with the old session):

| selectivity | recall | base p50 (pairs)   | new p50 (pairs)    | speedup |
|-------------|--------|--------------------|--------------------|---------|
| all         | 1.0000 | 5.96 (5.94-5.97)   | 8.09 (6.08-10.10)  | ~1.0x   |
| 1%          | 1.0000 | 118.68 (118.22-119.14) | 20.06 (19.86-20.26) | 5.9x |
| 10%         | 0.9997 | 184.45 (181.41-187.48) | 54.20 (53.87-54.52) | 3.4x |
| 50%         | 1.0000 | 12.45 (10.09-14.81) | 9.50 (9.45-9.55)   | ~1.0x   |

p95 follows p50 (e.g. 100K x 768 1%: base 128.9-130.6, new 29.0-29.5; 10%: base
208.9-209.9, new 84.7-86.3). The `all` and `50%` rows are unchanged within noise:
no filter index is created or read for them (50% is not selective; the loop handles
it, as before). The 100K x 384 `all` pair-2 value (10.10) is cache noise, p50 of
pair 1 is 6.08 vs base 5.97.

### 7.2 Index creation cost and size

- 100K x 768, `EnsureFilterIndexes("t1","t10","t50")` (3 key indexes + damaged
  index, one pass each): **864 ms total** (~216 ms per index), db
  28,758,016 -> 34,344,960 bytes (**+5.33 MB, +19.4%**).
- On-demand creation during the first filtered runs (t1 + t10 + damaged): db
  28,758,016 -> 32,514,048 (**+3.58 MB, +13.1%**) at 100K (both dims; meta is
  identical), 2,916,352 -> 3,289,088 (**+364 KB, +12.8%**) at 10K. The creating
  query pays one index pass (~200-300 ms at 100K, ~20-30 ms at 10K estimated) ONCE; it is 1 of
  1000 queries, so p50/p95 of the run barely move (first-run 1% p50 25.86 vs paired
  26.30 at 100K x 768).
- Real code index (31,071 passages, 58,597,376 bytes; SQL probe, E-cores): damaged
  index 229 ms; per-key creation 260-313 ms; 4 keys (lang, node_type, is_test, name)
  + damaged = **+2,658,304 bytes (+4.5%)**. Damaged-row count: **0** (gleann-written
  metadata is clean, as designed).
- Old-session comparison: identical db sizes (28,758,016 -> 32,514,048;
  2,916,352 -> 3,289,088) and creation order (170 ms/key at 100K x 384 on P-cores).

### 7.3 Write cost (BenchmarkApplySourceMetaIndexes, E-cores)

One op = 200 files x 20 passages (4000 passages, code-index meta shape); indexes=N
also creates the damaged index. See section 2.4: 0 -> 217 ms, 1 -> 291 ms (+34%),
4 -> 381 ms (+75%), 16 -> 499 ms (+130%) per op.

### 7.4 Real-index query cost (SQL probe, 31,071 passages, E-cores, p50 of 9 reps)

| filter (matches)        | full scan ms | indexed ms | speedup |
|-------------------------|--------------|------------|---------|
| lang = python (213)     | 201-233      | 0.56-0.72  | ~300x   |
| node_type = struct (997)| 211-255      | 4.6-5.2    | ~46x    |
| node_type = method (4758)| 237-269     | 15.1-17.6  | ~16x    |
| lang = go (27,827)      | 341-703      | 56.5-169.9 | ~4x     |

(The old session measured 72-103 ms scans / 0.1-5.5 ms indexed on P-cores; same
shapes, E-core pinning explains the absolute difference. At 90% selectivity the
filter is not selective anyway — the loop's result stands.)

### 7.5 Recall gate

`cd bench/vectordb && go test -count=1 -run TestRecallGate ./gate/` (wrapper,
cores 12-15): **ok, 219.6 s** — all five subtests (hnsw, diskann, hnsw-near-copies,
gleann-search, gleann-search-diskann; 10K x 384, 300 queries) pass their thresholds
(0.99 / 0.98 / 0.95, gate/thresholds.go). The filtered gleann-search rows exercise
the new index path end to end. A second run with -v (ok, 205.4 s) logged: hnsw
0.9993, diskann 0.9983, near-copies 0.9763; gleann-search all/1%/10%/50% =
0.9993 / 1.0000 / 0.9997 / 0.9967; gleann-search-diskann = 0.9997 / 1.0000 /
0.9997 / 0.9970 — the values the gates were set at.

### 7.6 Comparison with the old session's numbers

Old (11f4538, cores 0-15 = mostly P-cores): 100K x 384 base 1% 70.2-70.6 ms,
10% 104.0-105.2 ms; new 1% 9.3-9.5 ms, 10% 28.4-28.6 ms (7.5x / 3.7x). This lane
(E-cores 12-15): base 118.7 / 184.4 ms, new 20.1 / 54.2 ms (5.9x / 3.4x). The
absolute values differ by the core class (~1.7-2x, verified: the same baseline
binary measured 74.3 ms at 1% on P-cores 0-11 vs 125.1 ms on E-cores 12-15); the
relative gain is the same design win, slightly smaller on E-cores because the
indexed path's per-row Go work weighs more there. Recall values are identical
(1.0000 / 0.9997 at 1% / 10%). Index sizes are byte-identical. The 100K x 768 shape
and the 3-pair protocol are new in this lane (the old measure.sh was interrupted
before its 10K pairs and ALLDONE).

## 8. Open questions

- The cap (16) and the on-demand threshold (200K) are constants; make them
  configurable per index?
- A CLI surface to list/drop filter indexes (`gleann index ...`) is not implemented;
  dropping via SQL is safe today.
