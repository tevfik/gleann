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

In a `-tags treesitter` build, `index build --graph` and `index sync` also extract CALLS edges of Python, JavaScript/TypeScript, Rust, C/C++, Java, C#, Ruby, PHP, Swift, Scala, Kotlin, Lua, Elixir and Svelte files into the code graph (Go uses `go/ast` / `go/types` in every build). A call is charged to the innermost function or method that contains it; calls at the top level of a file go to a per-file `<script:file>` symbol anchored at line 1. A callee named by its bare name binds to a member of the same package (`inferred`, or `ambiguous` with several candidates); more than 8 candidates stay external. Full builds extract files in parallel with a deterministic merge.

The declarations a file contributes are the chunker's tree-sitter chunks: classes, objects, functions, methods and the like, named and anchored in their source. For Svelte, the declarations and calls of an inline `<script>` block come from the JavaScript/TypeScript grammar and are anchored at the block's real lines in the component.

A file that yields no declaration chunk — for example an `.exs` script made only of top-level calls — falls back to regex chunking and contributes **no** CALLS edges: `chunkWithCalls` (`internal/graph/indexer/extract_ts.go`) aborts when the chunker returns nothing, so the per-file `<script:file>` caller only exists for a file that declared at least one symbol. This is a pipeline limit shared by every tree-sitter language, not specific to Elixir.

### Call graph limits

- **Kotlin:** the bundled grammar stores declaration names in child nodes instead of fields (`fun greet` has no `name` field). The chunker reads them explicitly, so a Kotlin file yields its real classes, objects, functions and properties, and calls are charged to the enclosing function or method.
- **Lua:** declarations are `function_statement` nodes (`local function f`, `function M.run`, `function M:send`), chunked as `function` or `method` and anchored on `function`..`end` (the grammar attaches the surrounding whitespace to the node). A bare call (`helper()`), a dotted call (`m.helper()`), a colon call (`obj:send()`) and a call without a paren list (`require "json"`, `f{...}`) bind the method/function name, never the receiver.
- **Ruby:** a call is found with or without parentheses (`helper(x)`, `helper x`, `puts "hi"`) and on a receiver (`obj.save`, `Mod.call(x)`). A receiverless call with no arguments and no parentheses (`save`) parses as a plain identifier, so the grammar exposes no call node for it.
- **Elixir:** `def`, `defp`, `defmodule`, `defprotocol`, `defmacro`, `defdelegate` and the guards declare symbols (`M` for a module, `M::foo` for its function). A guard clause stays part of the definition header (`def foo(n) when is_integer(n)` declares `M::foo`), so neither the signature nor the guard checks become CALLS edges — whether the guard is bare, parenthesized (`when (is_integer(n))`), negated (`when not is_map(x)`), compounded (`when (is_integer(x) and x > 0)`) or nested inside another guard call's argument list (`when binary_part(x, 0, byte_size(x)) == "a"` keeps neither `binary_part` nor `byte_size`). A module attribute is not a call: its name (`@moduledoc`, `@doc`, `@spec`, `@attr`, …) is never an edge, and every type expression of a typespec attribute (`@spec`, `@callback`, `@macrocallback`, `@type`, `@typep`, `@opaque`) is a declaration rather than a call. The value expression of a plain attribute is still a real compile-time call, so `@attr helper()` yields `helper` and `@config build_config() |> finalize()` yields `build_config` and `finalize`. A default argument is not a guard: `def foo(x \\ helper())` keeps `helper` as a real edge, also when the signature has a guard. Definition and directive keywords are never CALLS edges, including `defstruct`, `defimpl`, `alias`, `import`, `require`, `use`, `quote` and the control-flow macros (`if`, `unless`, `case`, `cond`, `with`, `for`, `try`, `receive`); the filter applies to an unqualified target only, so `Mod.if(x)` is a real remote call to a function named `if`. A remote call (`Mod.func(x)`, `A.B.helper(x)`) binds the function name, never the receiver, and the right-hand side of a pipe is a call even without parentheses (`xs |> helper` yields `helper`, and so do `xs |> helper()` and `xs |> Mod.func`).
- **Scala:** an `object` is a container like a `class`, so its methods are declarations and a call inside one is charged to that method (not to the file's `<script:file>` symbol).
- **Svelte:** each inline `<script>` block is parsed with the JavaScript grammar (TypeScript for `lang="ts"`): its declarations and calls become symbols and edges at their real component lines. Template markup is not scanned for calls (`on:click={handler}` contributes no edge), and a component without a `<script>` block has no calls.
- **Other languages:** a call written in a shape the grammar does not model as a call (a dynamic send, a call built from a string) is not extracted; a callee declared nowhere keeps a stub FQN and is stored as external.
