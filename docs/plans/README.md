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
