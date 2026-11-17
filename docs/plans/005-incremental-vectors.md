# RFC 005: Incremental vector updates for format 2 (WP-2.2)

Status: implemented (2026-11-17; approved by the maintainer the same day)
Builds on: RFC 003 section 3.4 (publication order, `vec_slot`), RFC 004 B0 ("Incremental append", "Readers see other processes' writes")
Release: v1.20.0 (part of RFC 004 B0, before B1)

## 1. Problem

Format 2 has no in-place vector update. `updateIndexFormat2` (`pkg/gleann/builder_format2.go`) treats every add, change or delete alike: it reads all passages (`storeDB.ForEach`), embeds all of them again (`b.embedder.Compute` over every text), builds a new graph (`b.backend.Build`), renumbers every unchanged slot (`ReassignSlotsTx`) and publishes the new file (`publishVectorFormat2`).

Measured in gleann-rag-test `results/scale/phase2-ecc1036/README.md` (fake 64-d embedder, so almost all of it is graph and I/O):

| size | full build | one-file sync | 5 % budget |
|---|---:|---:|---:|
| 100K | 89.6 s | 73.0 s | 4.5 s |
| 1M | 1132 s | 937 s (5.5 GB RSS) | 57 s |

Callers that pay it on every event: `gleann index watch`, `POST /api/indexes/{name}/update` (`handleUpdateIndex` → `UpdateIndexWithStats`), uploads, and the memory vector syncer, which publishes once per entity (`LeannSyncer.AddContent` → `AddToIndex`).

Format 1 had `appendInPlace` (`pkg/gleann/builder.go`), which only appended and still rewrote the whole HNSW file through `Builder.AddVectors` (`modules/hnsw/backend.go`: `ReadCSR` → `ToGraphWithDistance` → `Insert` → `ConvertToCSR` → `WriteTo`).

## 2. Goals and pass conditions

| # | Goal | Pass condition |
|---|---|---|
| G1 | Fast one-file sync | gleann-rag-test `scale` `sync-1` at 100K ≤ 4.5 s and at 1M ≤ 57 s (≤ 5 % of that run's full build); embed calls = the changed file's chunks only |
| G2 | Add, change and delete | each passes G1 on its own |
| G3 | Crash safety as today | every crash point (`crashBeforeCommit`, `crashAfterCommit`, `crashAfterRename`, `crashAfterFinish`) and the new ones recover to the old or the new state; a vector hit never returns the wrong passage |
| G4 | Readers in other processes see the old or the new generation | `serve` running + CLI sync: the next search returns the right text; no hit maps to a wrong passage, even mid-publication |
| G5 | Quality unchanged | the `internal/quality` gate passes at the B1 thresholds (BM25 0.521, Dense 0.297, Hybrid 0.376) on an index reached through incremental steps |
| G6 | No recall drift | recall@10 against exact search after 50 rounds (1 % of sources changed per round, 10 % deleted overall) within 0.01 of a fresh build of the same final corpus, at 100K |

Not goals: changing the embedding model (still a rebuild); compacting the lexical index (FTS5 already updates per source).

## 3. Design

**O1, insert into the HNSW graph and rewrite the file each time** (format-1 `AddVectors`): O(N) per sync (deserialise 1M nodes, rewrite, remap), refuses pruned indexes, and DiskANN rebuilds anyway. Rejected as the per-sync path; kept as the HNSW merge step.

**O2, append-only segments merged periodically (LSM):** a publication covers N files and readers search N segments. Rejected for v1.20; O3 is its one-segment case.

**O3 (chosen): one delta file, tombstones, merge at a size limit.** New vectors go to `<name>.delta`, a flat, exactly searched list of `(slot, vector)` rewritten whole at each publication (small by construction). Deleted or replaced passages lose their row; their slot is dead and never reused. Search queries the main backend and the delta, merges by distance and drops dead slots. A merge folds the delta into the main file past a limit; a compaction drops dead slots past a limit. Per sync: embed the changed chunks, one small transaction, rewrite the delta (≤ 4096 × dim × 4 B ≈ 12 MiB at 768-d). No O(N) step.

### 3.1 Slots: never renumber, never reuse
- Meta key `vector_slots` is the high-water mark. New passages take slots from it in the same transaction (`applySourceGroupsTx(..., startSlot=vector_slots)`); unchanged passages keep theirs; `ReassignSlotsTx` is not used on the incremental path.
- Dead slots = `vector_slots` − passages with a slot (the unique partial index `passages_slot` tells live from dead; no new table).
- A reader with an older vector file can lose a hit but can never resolve a hit to the wrong text, because a slot is never reused. This is the invariant behind G4.
- Existing indexes without the key derive it as `SlotSpan().maxSlot+1` in the first incremental transaction.

### 3.2 Tombstones and filtering
- Search already skips a hit whose slot has no passage row (`buildBoostedResults`, `TopK` `continue` on a failed `passages.Get`). To keep k results the vector call over-fetches `pool + ceil(pool × dead/live)`, capped at `filterPoolCap`; the score normaliser is still fixed at the first round.
- Pruned indexes (recompute mode): `adHocRecompute` returns a nil vector for a dead slot instead of embedding `""` (`searchZeroCopy` already skips nil vectors). Pruned indexes get a lower compaction limit.
- Store mode (default): dead nodes keep their stored vector, so graph routing is unaffected.

### 3.3 The delta file
`<name>.delta`: header (magic `GLDL`, version, dims, dtype `f32`/`f16` equal to the main file, metric, count), `count × int64` slots, `count × dims` vectors. Readers load it into RAM. Search is exact top-k with the backend's distance function; in an f16 index the delta stores f16 so distances match the main file. Each rewrite drops entries whose slot has died.

### 3.4 Merge and compaction
- Merge when `delta_count > max(4096, 2 % of live)`; compaction when the dead share > 25 % (store mode) or > 10 % (pruned), or on `gleann index compact` / `index repair`. Both configurable.
- The sync that crosses a limit pays the merge; it is reported as `UpdateStats.Mode = UpdateMerge` and is not part of G1. Watch and the server may defer it to idle.

| backend | merge | compaction | renumbers? |
|---|---|---|---|
| hnsw, store mode | add delta vectors with their own slots (new optional `AddVectorsWithIDs`; `Insert` already takes an external ID) | rebuild from the live stored vectors plus the delta, no embedding (`BuildWithIDs`) | never |
| hnsw, pruned | full rebuild with recompute (today's path) | same | no (`BuildWithIDs`) |
| diskann | full rebuild from `DiskIndex.GetVector` (no embedding) | same | yes, dense; relies on the B0 freshness check |
| faiss (tag) | full rebuild (positional IDs) | same | yes |

## 4. Fit with RFC 003 publication and recovery
- Per-sync publication reuses `publishVectorFormat2` with the staged file `<name>.delta.tmp`; removals, new rows at `vector_slots` and the key itself go into the one transaction with the pending keys.
- Merge, compaction and full builds stage `<name>.index.tmp` and an empty `<name>.delta.tmp`. `pending_vector_file` becomes a list (one name = one-element list); `recoverVectorPublication` renames each staged file still present to its name without `.tmp` (today it hardcodes `.index`); stale-temp cleanup covers both. The full-build publication also stages the empty delta, so no stale delta survives a rebuild.
- New crash points `after_rename_main` and `after_merge_commit`. For HNSW a mixed main/delta pair is still correct (stable slots; a slot in both files is deduplicated, smaller distance kept). For DiskANN/faiss a mixed pair exists only until recovery, which every open runs.
- Every publication bumps `vector_generation`; new key `vector_main_generation` changes only with the main file. `syncPublishedMeta` and `meta.json` gain `VectorSlots` and `VectorMainGeneration`.

## 5. Readers
- The main file is untouched by per-sync publications, so the mmap searcher stays valid; it re-maps only when `vector_main_generation` changes. The delta is a separate small read.
- f16: the delta uses the same dtype; a merge keeps f16 vectors losslessly; tested with `VectorDType=f16`.
- Freshness (RFC 004 B0 item 1): the searcher records `(vector_generation, vector_main_generation)` when it loads and re-reads them after opening its files (load-then-verify, closing the race with a rename). On acquire, a change of only `vector_generation` reloads the delta and the dead count; a change of `vector_main_generation` reloads fully. Watch and the TUI use the same check.

## 6. Index health and doctor
The format-2 rule "dense slots" (`count == maxSlot+1`) becomes `slotless == 0 && maxSlot < vector_slots`, and every live slot lies in the main file or the delta (sampled). Doctor reports the dead share and delta size as information.

## 7. Migration
New indexes: none. Existing format-2 indexes: no `.delta` = empty; `vector_slots` derived; they are dense and stay valid. Older binaries ignore the delta (new passages invisible to the vector leg, lexical correct; doctor suggests repair) — acceptable in the breaking v1.20 release and stated in the release notes.

## 8. Tests and benchmarks

| Layer | Test |
|---|---|
| unit, `pkg/gleann` | delta encode/decode f32/f16; exact top-k vs brute force; dedup of a slot in both files; over-fetch keeps k at 30 % dead; `vector_slots` derivation; a dead slot is never reallocated (add/delete/add) |
| unit, `modules/hnsw` | `AddVectorsWithIDs` with non-contiguous IDs; `BuildWithIDs`; mmap search on sparse IDs; recompute returns nil for dead slots |
| crash | the four existing points for a delta publication; `after_rename_main`/`after_merge_commit` for merge and full build; after each: reopen, doctor clean, every vector hit's text equals its source |
| cross-process (G4) | `serve` + CLI add/change/delete; next search correct; a reader loaded before the sync never returns wrong text |
| scale (G1) | gleann-rag-test `scale` 100K and 1M: `sync-1` add/change/delete separately; a no-op sync first as the floor; merge and compaction timed separately; embed calls counted |
| quality (G5) | quality gate on an index built at 50 % and grown incrementally to 100 % |
| drift (G6) | `pkg/gleann` test (tag `drift`, 100K synthetic): 50 rounds, recall@10 vs exact vs a fresh build |

## 9. Steps (one commit each)
1. Generalise the publication (list-valued `pending_vector_file`, suffix-derived rename in recovery, cleanup of `.delta.tmp`); crash tests for a two-file publication. No behaviour change.
2. `vector_slots` key and `IndexMeta.VectorSlots`/`VectorMainGeneration`; `syncPublishedMeta` writes them.
3. Delta file format and exact search (`pkg/gleann/vector_delta.go`).
4. Searcher: load the delta, merge with dedup, over-fetch by dead share, nil vectors for dead slots in recompute.
5. Builder: incremental branch in `updateIndexFormat2` (no `ForEach`, no `ReassignSlotsTx`, embed changed chunks only, publish the delta); full path for `force`, a missing main file or a crossed limit.
6. HNSW `AddVectorsWithIDs`/`BuildWithIDs` and adapters; merge and store-mode compaction from stored vectors.
7. DiskANN and faiss merge = rebuild from stored vectors; the full build stages the empty delta.
8. Health and doctor sparse-slot rule.
9. Reader freshness: load-then-verify; delta-only vs full reload in `searcher_cache.go`, watch and TUI (extends RFC 004 B0 item 1).
10. Benchmarks (scale, quality gate, drift); results in gleann-rag-test `results/scale/`; RFC 004 B0 marked done.

## 10. Risks
- Distance mismatch between the legs (DiskANN PQ vs exact delta): merge on the backend's full-precision distance (DiskANN reranks with full vectors); a unit test compares the merged order with brute force.
- The fixed per-sync cost (directory walk, manifest diff, opening the db) may already exceed 5 % at 1M: the no-op floor is measured first; if it alone breaks the budget, that is a separate item.
- Recall loss from dead nodes in pruned indexes: 10 % compaction limit and the drift test in recompute mode.
- Frequent publications (watch, memory syncer) cause frequent reader reloads: delta-only reloads; the memory syncer batches entities per tick.
- DiskANN/faiss renumber on merge: correct only with the freshness check; step 9 lands before B1 and the cross-process test runs per backend.
- The merge sync is O(N): reported separately, deferrable to idle or `index compact`, bounded by the limits.

## 11. Audit log
- 2026-11-17: drafted by a read-only planning subagent against main 13cae74; every claim cites a function at that commit.
- 2026-11-17: implemented in three commits (68dcf73, 4eba8c5, 9068959; the last landed on main as 36b01ca). Deviations from sections 3.1 and 3.4, all measured:
  - A merge renumbers slots densely for every backend (no `BuildWithIDs`), and builds the HNSW graph in a shuffled insertion order seeded by the generation. Building in slot order, with the newest topics last, lost about 3 points of recall@10 against a fresh build at 30K passages. A merge is a main-file publication, so readers reload on `vector_main_generation`; this is the same exposure as a full rebuild before this RFC.
  - Compaction defaults: dead share 5 % (also for pruned indexes) and at least 256 dead slots (`GLEANN_DEAD_MIN`); an explicit `GLEANN_DEAD_SHARE` sets the minimum to 0. With 25 % the G6 drift test failed (0.855 against 0.917); with these defaults it passes (0.941 against 0.917).
  - Faiss and pruned (recompute) indexes take the full re-embedding path, because they cannot return stored vectors.
  - Merge and compaction cost about 87 % of a full build with a 0 ms embedder (109.6 s and 110.3 s against 126.2 s at 100K); they save only the embedding calls. Inserting the delta into the existing graph (`AddVectorsWithIDs`) is left as a later optimisation.
  - Measured at 100K (gleann-rag-test `results/scale/rfc005-README.md`): one-file add 0.349 s, change 0.334 s, delete 0.320 s, no-op 0.087 s (G1 target 4.5 s; before: 73 s). The 1M run belongs to the v1.20 test campaign.
