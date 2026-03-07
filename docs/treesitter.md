# Tree-sitter Backend (Optional)

gleann includes an optional tree-sitter backend for precise AST-aware code chunking across all supported languages. Without this tag, non-Go languages use regex-based boundary detection.

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

# Run tree-sitter tests
go test -tags treesitter ./internal/chunking/ -v -run TestTreeSitter

# Both FAISS and tree-sitter
go build -tags "faiss treesitter" -o gleann ./cmd/gleann/
```

Without `-tags treesitter`, all non-Go languages use regex patterns and the binary remains pure Go.

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
