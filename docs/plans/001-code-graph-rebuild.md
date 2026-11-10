# RFC 001: Code Graph Rebuild — Phantom & Split Symbol Prevention

* **Status:** Draft / Open for Agent & Human Review
* **Target Feature Branch:** `feature/graph-rebuild`
* **Related Issue:** [Issue #12](https://github.com/tevfik/gleann/issues/12)
* **Contributors:** @citkane, @tevfik

---

## 1. Problem Statement

As highlighted in [Issue #12](https://github.com/tevfik/gleann/issues/12), the current code graph extraction pipeline (`internal/graph` and tree-sitter extractors) suffers from three core architectural issues:

1. **Phantom Symbols:** Call sites and unresolved imports sometimes spawn phantom `Symbol` nodes without concrete declaration anchors. These clutter blast radius calculation (`explain`) and cause ghost dependencies.
2. **Split Symbols:** Receiver methods, multi-file Go package declarations, and cross-file type references fail to resolve to a canonical symbol ID, producing split duplicate nodes for what is logically a single entity.
3. **Fragile Incremental Updates:** During `gleann index sync`, retracting a modified file's symbols can leave dangling edges or trigger Kùzu database primary key collisions (`duplicated primary key`) when external references point to them.

---

## 2. Invariants & Requirements

Per [AGENTS.md](../../AGENTS.md), this rebuild must satisfy:

* **Strict Source Anchors:** Every first-party symbol node must have an exact source declaration location (`file`, `line_start`, `line_end`, `col_start`, `byte_offset`).
* **External Symbol Differentiation:** External dependencies (standard library, third-party packages) must be marked `is_external: true` and isolated from codebase declarations.
* **Canonical Fully-Qualified Names (FQN):**
  - Go: `<package_import_path>.<Receiver>.<Method>` or `<package_import_path>.<Func>`
  - Python: `<module_path>.<Class>.<method>`
  - Rust: `<crate>::<module>::<Type>::<fn>`
  - TypeScript/JS: `<module_path>#<Class>.<method>`
* **Deterministic Kùzu Schema:** Schema must enforce foreign key integrity and prevent duplicate primary keys during sync.
* **Build Tag Discipline:** Pure Go stub implementation when `-tags treesitter` is not provided.

---

## 3. Proposed Architecture

```
 Source Files (.go, .rs, .py, .ts, etc.)
                   │
                   ▼
       [ Tree-sitter Parser ]
                   │
                   ▼
     [ AST Extractor & Visitor ]
       - Phase 1: Symbol Declaration Extraction (Concrete Anchors + FQN)
       - Phase 2: Call/Reference Edge Resolution (Scoped Scope Table)
                   │
                   ▼
     [ Graph Builder / Syncer ]
       - Atomic retraction of stale nodes
       - Upsert canonical Symbol nodes
       - Re-bind CALLS, IMPLEMENTS, REFERENCES edges
                   │
                   ▼
          [ Kùzu Embedded DB ]
```

---

## 4. Next Steps for Collaborators

1. **@citkane:** Add your draft rebuild notes and technical design suggestions directly to this document or open a PR against `feature/graph-rebuild`.
2. **Collaborating Agents:** Review data structures, edge-case handling in incremental mode, and multi-language query patterns.
3. **Maintainer Sign-Off:** Review converged plan before implementing changes on `internal/graph`.
