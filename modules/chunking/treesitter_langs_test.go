//go:build cgo && treesitter

package chunking

import (
	"sort"
	"strings"
	"testing"
)

// chunkOfFile runs the public chunker (tree-sitter first, regex fallback) and
// returns its chunks. The metadata records which parser produced a chunk, so a
// test can tell a real AST extraction from the regex fallback.
func chunkOfFile(t *testing.T, filename, src string) []CodeChunk {
	t.Helper()
	return NewASTChunker(DefaultASTChunkerConfig()).ChunkCode(src, filename)
}

// chunkNames returns "nodeType:name" for every named declaration chunk,
// sorted. The preamble and gap chunks of the chunker are not declarations.
func chunkNames(chunks []CodeChunk) []string {
	var out []string
	for _, ch := range chunks {
		if ch.Name == "" || ch.NodeType == "preamble" || ch.NodeType == "block" {
			continue
		}
		out = append(out, ch.NodeType+":"+ch.Name)
	}
	sort.Strings(out)
	return out
}

// findChunk returns the chunk with the given name, or nil.
func findChunk(chunks []CodeChunk, name string) *CodeChunk {
	for i := range chunks {
		if chunks[i].Name == name {
			return &chunks[i]
		}
	}
	return nil
}

func assertNames(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("chunks =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// TestKotlinASTDeclarations: the bundled Kotlin grammar exposes no field names
// on its declarations, so the chunker has to find the name node itself. Before
// the fix every Kotlin chunk had an empty name and the indexer dropped them.
func TestKotlinASTDeclarations(t *testing.T) {
	src := "fun greet() { helper() }\n\nclass Foo {\n  fun bar() { baz() }\n}\n\nobject Single {\n  fun go() { run() }\n}\n\nval top = compute()\n"
	chunks := chunkOfFile(t, "a.kt", src)
	for _, name := range []string{"greet", "Foo", "Foo::bar", "Single", "Single::go"} {
		ch := findChunk(chunks, name)
		if ch == nil {
			t.Errorf("no chunk named %q in %v", name, chunkNames(chunks))
			continue
		}
		if ch.Metadata["parser"] != "tree-sitter" {
			t.Errorf("chunk %q came from %v, want tree-sitter", name, ch.Metadata["parser"])
		}
	}
	// The property is a declaration too: its name lives in the nested
	// variable_declaration node.
	if ch := findChunk(chunks, "top"); ch == nil {
		t.Errorf("no chunk for the property in %v", chunkNames(chunks))
	}
	if ch := findChunk(chunks, "Foo::bar"); ch != nil && ch.NodeType != "function" {
		t.Errorf("Foo::bar type = %q, want function", ch.NodeType)
	}
}

// TestKotlinASTCalls: Kotlin's call_expression wraps the callee in a
// navigation_expression for `a.b()`, exactly as a bare call wraps a
// simple_identifier.
func TestKotlinASTCalls(t *testing.T) {
	src := "fun f() {\n  println(1)\n  a.b()\n  m.n(2)\n}\n"
	ch := findChunk(chunkOfFile(t, "a.kt", src), "f")
	if ch == nil {
		t.Fatal("no chunk for f")
	}
	if got := strings.Join(ch.OutboundCalls, ","); got != "println,b,n" {
		t.Errorf("outbound calls = %q, want println,b,n", got)
	}
}

// TestLuaASTDeclarations: the bundled Lua grammar names its declaration node
// function_statement (not function_declaration), which the rule table missed,
// so Lua silently kept the regex chunker.
func TestLuaASTDeclarations(t *testing.T) {
	src := "local function helper() return 1 end\nlocal M = {}\nfunction M.run() helper() end\nfunction M:send(x) self:helper(x) end\nreturn M\n"
	chunks := chunkOfFile(t, "a.lua", src)
	assertNames(t, chunkNames(chunks), "function:helper", "method:M:send", "method:M.run")
	for _, name := range []string{"helper", "M.run", "M:send"} {
		if ch := findChunk(chunks, name); ch != nil && ch.Metadata["parser"] != "tree-sitter" {
			t.Errorf("chunk %q came from %v, want tree-sitter", name, ch.Metadata["parser"])
		}
	}
}

// TestLuaASTCalls: bare, dotted, colon and string/table-argument calls all
// have to yield the callee, never the receiver.
func TestLuaASTCalls(t *testing.T) {
	src := "function f()\n  helper()\n  m.helper(1)\n  obj:send(\"x\")\n  io.write(tostring(x))\n  require \"json\"\nend\n"
	ch := findChunk(chunkOfFile(t, "a.lua", src), "f")
	if ch == nil {
		t.Fatal("no chunk for f")
	}
	if got := strings.Join(ch.OutboundCalls, ","); got != "helper,send,write,tostring,require" {
		t.Errorf("outbound calls = %q, want helper,send,write,tostring,require", got)
	}
}

// TestElixirASTDeclarations: def/defp/defmodule are call nodes in the Elixir
// grammar; the chunker used to emit one nameless chunk for the whole file.
func TestElixirASTDeclarations(t *testing.T) {
	src := `defmodule M do
  alias Foo.Bar
  import Baz
  use GenServer
  defstruct [:a]

  def foo do
    helper()
  end

  defp bar(x), do: other(x)
end
`
	chunks := chunkOfFile(t, "a.ex", src)
	assertNames(t, chunkNames(chunks), "function:M::bar", "function:M::foo", "module:M")
	if ch := findChunk(chunks, "M::foo"); ch != nil {
		if ch.Metadata["parser"] != "tree-sitter" {
			t.Errorf("M::foo came from %v, want tree-sitter", ch.Metadata["parser"])
		}
		if got := strings.Join(ch.OutboundCalls, ","); got != "helper" {
			t.Errorf("M::foo outbound calls = %q, want helper", got)
		}
	}
	if ch := findChunk(chunks, "M::bar"); ch != nil {
		if got := sortedCSV(ch.OutboundCalls); got != "other" {
			t.Errorf("M::bar outbound calls = %q, want other (a keyword-form body is a real call)", got)
		}
	}
}

// TestElixirASTDefprotocolAndMacro: defprotocol and defmacro declare symbols
// too, and the quote/unquote block of a macro is not walked as definitions.
func TestElixirASTDefprotocolAndMacro(t *testing.T) {
	src := `defprotocol P do
  def run(x)
end

defmodule M do
  defmacro mac(x) do
    quote do
      unquote(x)
    end
  end
end
`
	assertNames(t, chunkNames(chunkOfFile(t, "a.ex", src)),
		"function:M::mac", "function:P::run", "module:M", "module:P")
}

// TestElixirASTGuardedDeclarations: a guard clause makes the grammar wrap the
// declared signature in a `binary_operator` (`when`) node, so the chunker used
// to miss `def foo(n) when ...` and `defguard` entirely and every name in the
// header (the signature and the guard checks) leaked as a call.
func TestElixirASTGuardedDeclarations(t *testing.T) {
	src := `defmodule M do
  def foo(n) when is_integer(n) do
    helper(n)
  end

  defguard is_even(n) when rem(n, 2) == 0
end
`
	chunks := chunkOfFile(t, "a.ex", src)
	assertNames(t, chunkNames(chunks), "function:M::foo", "function:M::is_even", "module:M")

	if ch := findChunk(chunks, "M::foo"); ch != nil {
		if got := strings.Join(ch.OutboundCalls, ","); got != "helper" {
			t.Errorf("M::foo outbound calls = %q, want helper", got)
		}
	}
	if ch := findChunk(chunks, "M::is_even"); ch != nil {
		if got := strings.Join(ch.OutboundCalls, ","); got != "" {
			t.Errorf("M::is_even outbound calls = %q, want none (a guard is header, not a call)", got)
		}
	}
}

// TestElixirASTRemoteCalls: a remote call (`Mod.func(1)`, `A.B.helper(2)`) puts
// a `dot` node in the call's `target` field; the query used to bind no name at
// all, so the call was dropped.
func TestElixirASTRemoteCalls(t *testing.T) {
	src := `defmodule M do
  def run do
    Mod.func(1)
    A.B.helper(2)
  end
end
`
	chunks := chunkOfFile(t, "a.ex", src)
	ch := findChunk(chunks, "M::run")
	if ch == nil {
		t.Fatalf("no chunk named M::run in %v", chunkNames(chunks))
	}
	if got := strings.Join(ch.OutboundCalls, ","); got != "func,helper" {
		t.Errorf("M::run outbound calls = %q, want func,helper (the receiver is not part of the callee)", got)
	}
}

// TestScalaASTObjectMembers: a Scala object is a container like a class; its
// methods were not descended into, so they never became callers.
func TestScalaASTObjectMembers(t *testing.T) {
	src := "package x\n\nobject Main {\n  def greet() = { helper(); obj.run(1) }\n  def helper() = 1\n}\n"
	chunks := chunkOfFile(t, "a.scala", src)
	assertNames(t, chunkNames(chunks), "function:Main::greet", "function:Main::helper", "object:Main")
	if ch := findChunk(chunks, "Main::greet"); ch != nil {
		if got := strings.Join(ch.OutboundCalls, ","); got != "helper,run" {
			t.Errorf("Main::greet outbound calls = %q, want helper,run", got)
		}
	}
}

// sortedCSV joins calls in sorted order so an assertion does not depend on the
// order the query cursor happens to emit matches.
func sortedCSV(calls []string) string {
	cp := append([]string(nil), calls...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}

// TestElixirASTGuardedWrapperExpressions: the Elixir grammar wraps a
// parenthesized guard in a `block` node and a `not` guard in a
// `unary_operator` node. Both are still part of the definition header, so the
// guard checks must not leak into OutboundCalls (they are not call sites).
func TestElixirASTGuardedWrapperExpressions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		fn   string
		want string
	}{
		{
			name: "parenthesized_guard",
			src:  "defmodule M do\n  def foo(x) when (is_integer(x)) do\n    work(x)\n  end\nend\n",
			fn:   "M::foo", want: "work",
		},
		{
			name: "not_guard",
			src:  "defmodule M do\n  def bar(x) when not is_map(x) do\n    work(x)\n  end\nend\n",
			fn:   "M::bar", want: "work",
		},
		{
			name: "nested_not_parenthesized",
			src:  "defmodule M do\n  def baz(x) when not (is_nil(x)) do\n    work(x)\n  end\nend\n",
			fn:   "M::baz", want: "work",
		},
		{
			name: "parenthesized_compound_guard",
			src:  "defmodule M do\n  def foo(x) when (is_integer(x) and x > 0) do\n    work(x)\n  end\nend\n",
			fn:   "M::foo", want: "work",
		},
		{
			name: "defguard_parenthesized_guard",
			src:  "defmodule M do\n  defguard is_even(n) when (rem(n, 2) == 0)\nend\n",
			fn:   "M::is_even", want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := findChunk(chunkOfFile(t, "a.ex", tc.src), tc.fn)
			if ch == nil {
				t.Fatalf("no chunk named %q", tc.fn)
			}
			if got := sortedCSV(ch.OutboundCalls); got != tc.want {
				t.Errorf("%s outbound calls = %q, want %q (a guard is header, not a call)", tc.fn, got, tc.want)
			}
		})
	}
}

// TestElixirASTKeywordFormGuardedDefinition: the `do:` value of a keyword-form
// definition is a real call body (`pair` under `keywords` stops the header
// walk), not part of the guard.
func TestElixirASTKeywordFormGuardedDefinition(t *testing.T) {
	src := "defmodule M do\n  def foo(x) when is_integer(x), do: helper(x)\nend\n"
	ch := findChunk(chunkOfFile(t, "a.ex", src), "M::foo")
	if ch == nil {
		t.Fatalf("no chunk named M::foo")
	}
	if got := sortedCSV(ch.OutboundCalls); got != "helper" {
		t.Errorf("M::foo outbound calls = %q, want helper (a keyword-form body is a real call)", got)
	}
}

// TestElixirASTDefaultArgumentWithGuard: a default argument lives inside the
// signature call's own argument list, NOT in the `when` guard, so the helper
// call it makes is a real call site even when the signature has a guard.
func TestElixirASTDefaultArgumentWithGuard(t *testing.T) {
	src := "defmodule M do\n  def foo(x \\\\ helper()) when (is_map(x)) do\n    work(x)\n  end\nend\n"
	ch := findChunk(chunkOfFile(t, "a.ex", src), "M::foo")
	if ch == nil {
		t.Fatalf("no chunk named M::foo")
	}
	if got := sortedCSV(ch.OutboundCalls); got != "helper,work" {
		t.Errorf("M::foo outbound calls = %q, want helper,work (default argument is a real call, the guard is not)", got)
	}
}

// TestElixirASTQualifiedKeywordCalls: `Mod.if(x)` calls a function named `if`
// on Mod; only an unqualified bare `if` is the Kernel macro. The keyword filter
// must not drop a dot-qualified callee.
func TestElixirASTQualifiedKeywordCalls(t *testing.T) {
	src := "defmodule M do\n  def run(x) do\n    Mod.if(x)\n    MyDSL.unless(x)\n  end\nend\n"
	ch := findChunk(chunkOfFile(t, "a.ex", src), "M::run")
	if ch == nil {
		t.Fatalf("no chunk named M::run")
	}
	if got := sortedCSV(ch.OutboundCalls); got != "if,unless" {
		t.Errorf("M::run outbound calls = %q, want if,unless (a qualified keyword is a real remote call)", got)
	}
}

// TestElixirASTParenlessPipe: the pipe operator calls its right-hand side, so
// `xs |> helper` is `helper(xs)` even though the grammar models the RHS as a
// bare identifier rather than a call node.
func TestElixirASTParenlessPipe(t *testing.T) {
	src := "defmodule M do\n  def run(xs) do\n    xs |> helper\n    xs |> Mod.func\n    xs |> Enum.map(&f/1)\n  end\nend\n"
	ch := findChunk(chunkOfFile(t, "a.ex", src), "M::run")
	if ch == nil {
		t.Fatalf("no chunk named M::run")
	}
	if got := sortedCSV(ch.OutboundCalls); got != "func,helper,map" {
		t.Errorf("M::run outbound calls = %q, want func,helper,map", got)
	}
}

// TestElixirASTModuleAttributes: every module attribute is parsed as
// `unary_operator(@, call(<name>, arguments(...)))`, so the attribute name
// (`@moduledoc`, `@doc`, `@spec`, `@attr`, ...) used to leak as a CALLS edge.
// Inside a typespec attribute (`@spec`, `@type`, `@callback`, ...) every type
// expression is a declaration, never a call. Only the value expression of a
// plain attribute (`@attr helper()`) is a real compile-time call.
func TestElixirASTModuleAttributes(t *testing.T) {
	src := `defmodule M do
  @moduledoc "module docs"
  @typedoc "type docs"
  @doc "foo docs"
  @spec foo(integer()) :: integer()
  @type t :: list(integer())
  @typep tp :: atom()
  @opaque o :: term()
  @callback cb(atom()) :: {:ok, term()}
  @macrocallback mcb(term()) :: Macro.t()
  @attr helper()
  def foo(x), do: work(x)
end
`
	chunks := chunkOfFile(t, "a.ex", src)
	if ch := findChunk(chunks, "M::foo"); ch == nil {
		t.Fatalf("no chunk named M::foo in %v", chunkNames(chunks))
	} else if got := sortedCSV(ch.OutboundCalls); got != "work" {
		t.Errorf("M::foo outbound calls = %q, want work", got)
	}
	ch := findChunk(chunks, "M")
	if ch == nil {
		t.Fatalf("no chunk named M in %v", chunkNames(chunks))
	}
	if got := sortedCSV(ch.OutboundCalls); got != "helper,work" {
		t.Errorf("M outbound calls = %q, want helper,work (attribute names and typespecs are not calls)", got)
	}
	for _, bogus := range []string{"moduledoc", "typedoc", "doc", "spec", "type", "typep", "opaque", "callback", "macrocallback", "attr", "foo", "integer", "list", "atom", "term", "Macro", "t"} {
		for _, call := range ch.OutboundCalls {
			if call == bogus {
				t.Errorf("phantom call %q leaked from a module attribute: %v", bogus, ch.OutboundCalls)
			}
		}
	}
}

// TestElixirASTAttributeValueCalls: the value expression of a non-typespec
// module attribute is evaluated at compile time, so its calls are real call
// sites. The parenless pipe form keeps the right-hand side as well.
func TestElixirASTAttributeValueCalls(t *testing.T) {
	src := `defmodule M do
  @config build_config() |> finalize()
  @attr xs |> helper
  @attr helper()
end
`
	ch := findChunk(chunkOfFile(t, "a.ex", src), "M")
	if ch == nil {
		t.Fatalf("no chunk named M")
	}
	if got := sortedCSV(ch.OutboundCalls); got != "build_config,finalize,helper" {
		t.Errorf("M outbound calls = %q, want build_config,finalize,helper (an attribute value is a real call)", got)
	}
}

// TestElixirASTNestedGuardBuiltins: a guard-safe builtin nested inside another
// guard call's argument list (`byte_size` in `binary_part(x, 0, byte_size(x))`)
// is still part of the definition header and must not become a call site at any
// depth.
func TestElixirASTNestedGuardBuiltins(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "binary_part_byte_size",
			src:  "defmodule M do\n  def foo(x) when binary_part(x, 0, byte_size(x)) == \"a\" do\n    work(x)\n  end\nend\n",
		},
		{
			name: "elem_map_size",
			src:  "defmodule M do\n  def foo(x) when is_integer(elem(x, map_size(y))) do\n    work(x)\n  end\nend\n",
		},
		{
			name: "rem_byte_size",
			src:  "defmodule M do\n  def foo(x) when rem(x, byte_size(y)) == 0 do\n    work(x)\n  end\nend\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := findChunk(chunkOfFile(t, "a.ex", tc.src), "M::foo")
			if ch == nil {
				t.Fatalf("no chunk named M::foo")
			}
			if got := sortedCSV(ch.OutboundCalls); got != "work" {
				t.Errorf("M::foo outbound calls = %q, want work (a nested guard builtin is header, not a call)", got)
			}
		})
	}
}

// TestElixirASTAttributeAndGuardPreservedCalls: filtering attribute and guard
// expressions must not drop a default argument (inside the signature call's own
// arguments), an `unquote` in a macro head, or a keyword-form `do:` body.
func TestElixirASTAttributeAndGuardPreservedCalls(t *testing.T) {
	src := `defmodule M do
  def foo(x \\ bar(baz())) do
    work(x)
  end

  defmacro mac(unquote(helper())) do
    ok()
  end

  def kw(x) when is_map(x), do: helper(x)
end
`
	chunks := chunkOfFile(t, "a.ex", src)
	for _, tc := range []struct{ fn, want string }{
		{fn: "M::foo", want: "bar,baz,work"},
		{fn: "M::mac", want: "helper,ok"},
		{fn: "M::kw", want: "helper"},
	} {
		ch := findChunk(chunks, tc.fn)
		if ch == nil {
			t.Fatalf("no chunk named %s", tc.fn)
		}
		if got := sortedCSV(ch.OutboundCalls); got != tc.want {
			t.Errorf("%s outbound calls = %q, want %q", tc.fn, got, tc.want)
		}
	}
}
