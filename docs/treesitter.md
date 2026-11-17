# Tree-sitter Backend (Optional)

gleann includes an optional tree-sitter backend for precise AST-aware code chunking across all supported languages and for the call graph of non-Go languages. Without this tag, non-Go languages use regex-based boundary detection and contribute only their declarations to the code graph. The code graph itself (SQLite, in the index database) is part of every build; see [graph.md](graph.md).

## Default (Regex) vs Tree-sitter

| | Default (regex) | Tree-sitter (`-tags treesitter`) |
|---|---|---|
| **Go** | `go/ast` native (full AST) | `go/ast` native (unchanged) |
| **Python** | Regex: top-level + 1 indent | Full AST: nested classes, decorators |
| **JS/TS** | Regex: function/class/const | Full AST: arrow functions, exports |
| **Java/C#** | Regex: class/method | Full AST: inner classes, annotations |
| **C/C++** | Regex: function/struct | Full AST: templates, namespaces |
| **Rust** | Regex: fn/struct/impl | Full AST: generics, macros |
| **Dependencies** | None | CGo + tree-sitter grammars |
| **Chunk Expansion** | — | Parent scope context headers |

## Building with Tree-sitter

```bash
# Build with tree-sitter support
go build -tags treesitter -o gleann ./cmd/gleann/

# Run tree-sitter chunker tests
go test -tags treesitter ./modules/chunking/ -v

# Both FAISS and tree-sitter
go build -tags "faiss treesitter" -o gleann ./cmd/gleann/
```

With `-tags treesitter`, the build pipeline uses `modules/chunking.ASTChunker` as the default code chunker, extracting rich symbol metadata (`fqn`, `kind`, `signature`, `parent_symbol`, `doc`, `is_test`, `is_vendor`) and injecting symbol boundary headers into passage text. Without `-tags treesitter`, non-Go languages use regex fallback patterns while Go files use native `go/ast`.

## What Tree-sitter Improves

**Nested structures** — Regex `^    def` only matches 1 indent level. Tree-sitter correctly parses:

```python
class Outer:
    class Inner:          # ← regex misses this
        def deep_method(self):  # ← regex misses this
            pass
```

**Chunk expansion** — Each chunk gets a parent scope header for better embedding quality:

```python
# File: calc.py | Scope: Calculator
def add(self, a, b):
    return a + b
```

**Accurate boundaries** — Tree-sitter knows exactly where a function/class ends (matching braces, indentation), while regex guesses from the next boundary pattern.

## Call graph

In a `-tags treesitter` build, `index build --graph` and `index sync` also extract CALLS edges of Python, JavaScript/TypeScript, Rust, C/C++, Java, C#, Ruby, PHP, Swift, Scala, Kotlin and Elixir files into the code graph (Go uses `go/ast` / `go/types` in every build). A call is charged to the innermost function or method that contains it; calls at the top level of a file go to a per-file `<script:file>` symbol anchored at line 1. A callee named by its bare name binds to a member of the same package (`inferred`, or `ambiguous` with several candidates); more than 8 candidates stay external. Full builds extract files in parallel with a deterministic merge.

### Call graph limits

- **Lua:** declarations only. The bundled Lua grammar yields no chunks, so Lua keeps the regex chunker and no calls are extracted.
- **Ruby:** only calls written with parentheses (`helper(x)`) are found; `helper x` is not.
- **Elixir:** `def` and `defmodule` are macros, so they are recorded as calls as well as the real callees.
- **Kotlin:** the chunker finds no Kotlin declarations, so every call is charged to the file's `<script:file>` symbol and the callee stays unresolved unless another file declares it.
- **Scala:** the call query is registered, but no test checks the extracted edges yet.
- **Svelte:** no call query (the grammar's node names do not match the JS/TS query).
