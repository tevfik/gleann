# Collaborative Planning Workspace

Welcome to the Gleann architectural planning and RFC workspace.

This directory serves as the shared collaboration ground for both human engineers and AI coding agents (OpenCode, Antigravity, Claude, Cursor, etc.).

## Workflow: `Plan -> Audit -> Agree -> Build`

```
  ┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐       ┌─────────────────┐
  │ 1. Plan / RFC   │ ───>  │ 2. Agent Audit  │ ───>  │ 3. Human Signoff│ ───>  │ 4. Build & Test │
  │ (docs/plans/...)│       │ (Mutual review) │       │ (Maintainer OK) │       │ (Feature branch)│
  └─────────────────┘       └─────────────────┘       └─────────────────┘       └─────────────────┘
```

1. **Draft an RFC:**
   - Create a document named `<number>-<short-slug>.md` (e.g. `001-code-graph-rebuild.md`).
   - Use the standard RFC template (Motivation, Design, Schema, Backward Compatibility, Test Plan).

2. **Agent-to-Agent Audit:**
   - Both collaborators' agents review the plan against [AGENTS.md](../../AGENTS.md).
   - Verify: Zero-Python rule, build tags, edge-case safety, incremental indexing invariants.

3. **Human Agreement:**
   - Maintainers review the converged specification and give the green light.

4. **Implementation:**
   - Executed on a dedicated branch (e.g. `feature/graph-rebuild`) with small, test-backed commits.

---

## Active RFCs

- [001-code-graph-rebuild.md](./001-code-graph-rebuild.md) — Code graph: phantom symbol prevention, qualified identifiers, and unified multi-language AST extraction.
- [002-retrieval-quality.md](./002-retrieval-quality.md) — Retrieval quality: rerank stage and identifier gate, MiniLM-L6 pooler patch, HyDE query expansion, explanations, post-hoc citations.
- [003-storage.md](./003-storage.md) — Storage engine: SQLite record store, persisted FTS5 lexical index, code graph in SQL tables (implemented in v1.20.0).
- [004-v1.20-sqlite-only.md](./004-v1.20-sqlite-only.md) — v1.20.0: one SQLite store per index, Kùzu removed, format 2 only, memory entities carried over (implemented).
- [005-incremental-vectors.md](./005-incremental-vectors.md) — Incremental vector updates: delta file, stable slots, merge and compaction (implemented).
- [006-vector-db-benchmark.md](./006-vector-db-benchmark.md) — Vector database benchmark (bench/vectordb): gleann's HNSW/DiskANN against Qdrant, Chroma, LanceDB and sqlite-vec on recall-matched latency, updates and churn (approved).
