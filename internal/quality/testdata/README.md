# Retrieval-quality gate: test data

`go test ./internal/quality` unpacks `corpus.tar.gz` to a temporary directory and indexes it with the real indexing pipeline
(`internal/indexing.Build`, HNSW backend, default chunking), runs `tasks.json`
against BM25, dense and hybrid search, and fails when file-level MRR or R@10
falls more than `tolerance` below `baseline.json`.

## Corpus

- One archive instead of loose files, so gleann's own walker, code search and
  agents that read nested `AGENTS.md` files do not see a second copy of the code.
  The archive is deterministic (sorted entries, zero mtime, fixed owner and
  mode), so regenerating at the same commit gives the same bytes (with the same
  Go compress/flate version).
- A snapshot of gleann's own Go and Markdown files at the commit in `COMMIT`
  (Apache-2.0, like the rest of the repository), 320 files, 2.6 MB unpacked, 670 KB archived.
- It holds every file named as a gold file by the tasks (139 files) plus a
  deterministic sample of other `.go` and `.md` files as distractors (every
  third path in sorted order, files over 40 KB skipped).
- Produced by `internal/quality/gen_corpus.go`, which reads the files from git
  objects and needs no network:

  ```
  go run internal/quality/gen_corpus.go -commit 7521e58
  ```

- `.gitattributes` turns line-ending conversion off so Windows checkouts chunk
  the files the same way.

## Tasks

`tasks.json` is the union of `bench/tasks/gleann.json` (20 symbol lookups) and
`bench/tasks/gleann_nl.json` (121 natural-language questions) as they were at
that commit, 141 tasks. They are copied so later edits of `bench/tasks` do not
move the baseline.

## Embedder

The test uses a hashed bag-of-words embedder defined in `embedder_test.go`
(256 dimensions, camelCase and snake_case split). It is not a good embedding
model, so the dense and hybrid numbers are far below those of a real model
(hybrid MRR is lower than BM25 here, the reverse of the real-model runs in
`docs/retrieval-experiments.md`). Its job is to be deterministic and to feed
the whole pipeline: chunking, vector index, BM25, rank fusion, symbol boosts
and test/vendor demotion. The numbers are for comparing code changes only,
never with other benchmarks.

## Baseline and tolerance

Three runs with the pinned corpus gave identical numbers, because the embedder
and the HNSW build are deterministic. The tolerance is 0.01 (absolute, on MRR
and R@10 as fractions) to absorb floating-point differences between CPU
architectures, which can swap a few near-tied ranks. A run that is better than
the baseline by more than the tolerance passes and logs a hint to update it.

To rewrite the baseline after an intended change to ranking:

```
go test ./internal/quality -run TestRetrievalQuality -update
# or: GLEANN_QUALITY_UPDATE=1 go test ./internal/quality
```

Review the diff of `baseline.json` and say in the commit message why the
numbers moved. `go test -v ./internal/quality` prints the table for BM25,
dense and hybrid.

Under `-race` the test is skipped (about 170 s against 12 s); set
`GLEANN_QUALITY_RACE=1` to run it anyway.
