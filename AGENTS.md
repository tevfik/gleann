# Gleann — AI Agent Guidelines & Repository Rules

> **Target Audience:** Coding agents (OpenCode, Claude Code, Cursor, Antigravity, Copilot, Windsurf) and human engineers collaborating on this codebase.

---

## 1. Core Architectural Invariants

All agents modifying this codebase must adhere strictly to the following fundamental constraints:

1. **Zero Python in Core:**
   - The core `gleann` engine and CLI must remain 100% native (Go, C/C++, Rust via static linking or CGo).
   - Never introduce Python runtime scripts, virtualenvs, or Python daemon dependencies into the core engine.
   - Any Python-dependent processors (such as deep OCR/Docling) live exclusively in isolated external plugins (`gleann-plugin-*`) communicating via standardized IPC/HTTP, never in core.

2. **Zero Mandatory Docker:**
   - Gleann must compile and run as a standalone binary directly on bare metal (Linux, macOS, Windows).
   - Docker is permitted only as an optional deployment convenience for users, never as a build or runtime prerequisite.

3. **Build Tags Discipline & Pure-Go Graceful Degradation:**
   - **Pure Go (Default):** Must build with `CGO_ENABLED=0` without any tags. Uses pure-Go vector indexing (DiskANN), BM25, and in-memory caches.
   - **`-tags "eif"`:** Activates embedded C-runtime (`ext/bitllm.c`) for CPU-native BERT and BitNet quantized model inference.
   - **`-tags "treesitter"`:** Activates Tree-sitter AST parsers and embedded Kùzu graph database.
   - **`-tags "native"`:** Activates embedded Rust core acceleration (`ext/gleann-core-rs`).
   - **`-tags "faiss"`:** Activates FAISS vector indexing (requires CGo and libfaiss).
   - **Rule on Stubs:** Any code requiring CGo/Tree-sitter/Kùzu/FAISS **must** have a corresponding pure-Go stub file (`*_stub.go` or `*_nocgo.go`) built when the tag is omitted. Stubs must return clean, descriptive errors (e.g. `503 Service Unavailable: feature requires build with -tags treesitter`) and must never cause compiler errors or unhandled panics.

4. **Multi-Platform Support:**
   - Supported architectures: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` (Apple Silicon), `windows/amd64`, `windows/arm64`.
   - Never use platform-specific system calls without build tags or runtime platform guards.

---

## 2. Code Graph & AST Extraction Invariants

*(Ref: Issue #12 — Symbol Resolution & Graph Rebuild)*

When working on `internal/graph`, `internal/indexing`, or Tree-sitter AST extractors:

1. **Fully-Qualified Identifiers (No Split Symbols):**
   - Symbols must be deterministically identified by fully-qualified canonical names:
     - Go: `<package_path>.<ReceiverType>.<MethodName>` or `<package_path>.<FunctionName>`
     - Python: `<module_path>.<ClassName>.<method_name>`
     - Rust: `<crate>::<module>::<Type>::<function>`
     - TypeScript/JS: `<module_path>#<ClassName>.<methodName>`
   - Do not emit duplicate or split nodes for the same symbol declared across files in the same package.

2. **Source Anchors (No Phantom Symbols):**
   - Every first-party symbol node **must** have a concrete source declaration anchor: `(file_path, start_line, end_line, start_col, byte_offset)`.
   - References to external / third-party / standard library symbols must be tagged explicitly as external stubs (`is_external: true`) and separated from project declarations.
   - Never synthesize phantom declaration nodes from call sites or import statements.

3. **Referential Integrity on Incremental Sync:**
   - When a file is updated or deleted during `gleann index sync`:
     - Its declared symbols and associated edges (`CALLS`, `IMPLEMENTS`, `REFERENCES`, `DECLARES`) must be cleanly retracted.
     - Inbound references from unchanged files must be preserved or correctly rebound, without leaving dangling edges or causing primary key collision errors in Kùzu (`duplicated primary key`).
   - Graph mutation operations must be atomic and report errors cleanly. If graph update fails, the manifest must not advance.

4. **Single-Writer Concurrency:**
   - Kùzu database operates in a single-writer / multi-reader model.
   - Server processes must release graph write locks or coordinate internal sync safely. CLI commands attempting to write to a locked database must fail fast with a human-readable lock message.

---

## 3. Agent Collaboration Protocol (`Plan -> Audit -> Agree -> Build`)

To ensure consistent, bug-free, and aligned development between collaborating agents and human engineers:

```
[ RFC / Proposal ] ──> [ Collaborative Audit ] ──> [ Human Approval ] ──> [ Phased Build & Test ]
   (docs/plans/)           (Edge cases & tags)         (Maintainer)             (Feature branch)
```

1. **Phase 1: RFC / Plan Draft (`docs/plans/`):**
   - Any non-trivial refactor or feature begins as a Markdown specification in `docs/plans/<number>-<title>.md`.
   - The plan must state:
     - **Problem & Motivation** (concrete failure cases or shortcomings).
     - **Architecture & Data Structures** (schemas, node types, edge types, interfaces).
     - **Build Tag & Platform Impact** (pure Go vs CGo vs EIF).
     - **Incremental & Migration Strategy** (backward compatibility, index versioning).
     - **Test & Verification Matrix**.

2. **Phase 2: Collaborative Audit:**
   - The collaborating agent (e.g. OpenCode + Qwen) audits the plan and submits critiques, edge-case observations, or performance improvements.
   - The author agent refines the plan based on the audit.

3. **Phase 3: Human Maintainer Agreement:**
   - Human engineers review the aligned RFC and give the green light.

4. **Phase 4: Implementation in Feature Branches:**
   - Work is done on dedicated branches (e.g. `feature/graph-rebuild`).
   - Code is written incrementally in small, test-backed commits.

---

## 4. Coding Standards & Conventions

1. **Go Idioms & Formatting:**
   - Always run `gofmt -w` and `go vet ./...` before committing.
   - Preserve all existing comments and documentation unless deliberately updating them.
   - Return errors up the stack using standard wrapping: `fmt.Errorf("doing <action>: %w", err)`.
   - Avoid package-level mutable global variables. Pass configurations and dependencies explicitly.

2. **Git & Commit Hygiene:**
   - Use conventional commit messages: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `ci:`.
   - Never push directly to `main` without maintainer authorization.
   - Never commit binaries, downloaded models, `.eifm`, `.onnx`, or generated indexes (`*.db`, `*.index`) to git.

3. **Verification Checklist Before Submitting Code:**
   ```bash
   # 1. Pure Go build & tests (default)
   go vet ./...
   go test -v -race -timeout 5m ./...

   # 2. Build tag checks
   go build ./cmd/gleann
   go build -tags "eif" ./cmd/gleann
   go build -tags "treesitter" ./cmd/gleann

   # 3. Integration & graph tests (if working on graph)
   go test -v -tags "treesitter" ./internal/graph/...
   ```

---

## 5. gleann: Code Intelligence, Search & Long-term Memory

This project is indexed in [gleann](https://github.com/tevfik/gleann) under index name **`gleann`**.

### Exploration Commands
- `gleann search gleann <query>` — semantic code & doc search
- `gleann search gleann <query> --rerank` — semantic search with cross-encoder reranking
- `gleann ask gleann <question>` — RAG-powered Q&A across the codebase
- `gleann index sync gleann` — incrementally sync index after editing code
- `gleann graph explain <symbol> --index gleann` — inspect callers, callees, and blast radius
- `gleann graph query <pattern> --index gleann` — find symbols by name/regex pattern
- `gleann graph path <from> <to> --index gleann` — find shortest dependency path

### Long-term Memory (Always ON)
Persist decisions, conventions, and architectural notes across agent sessions:

```bash
gleann memory remember "Hexagonal architecture: adapters live in internal/adapters"
gleann memory remember "Kuzu graph DB is single-writer; avoid concurrent CLI write" --tag "convention"
gleann memory search "graph"
gleann memory list --tier long
gleann memory stats
```
