//go:build treesitter

// Tests for internal/graph/indexer/ts_calls.go through ExtractFile. The
// tree-sitter call extraction is the heart of the CALLS edges for non-Go
// languages; these tests cover edge cases that the per-language
// S-expression queries in modules/chunking/treesitter.go have to handle
// gracefully: no panic, and the declarations still come out.
package indexer_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// extract runs ExtractFile on src as the root-level file relPath of module
// "test" and returns the anchored declarations.
func extract(t *testing.T, relPath, src string) []gtypes.SymbolNode {
	t.Helper()
	return declared(indexer.ExtractFile("test", "/r", "/r/"+relPath, src))
}

// extractCalls runs ExtractFile and returns the anchored declarations together
// with the CALLS edges as "caller -> callee", both with the "test." FQN prefix
// dropped and sorted.
func extractCalls(t *testing.T, relPath, src string) ([]gtypes.SymbolNode, []string) {
	t.Helper()
	fg := indexer.ExtractFile("test", "/r", "/r/"+relPath, src)
	var edges []string
	for _, e := range fg.Calls {
		edges = append(edges, trimTest(e.CallerFQN)+" -> "+trimTest(e.CalleeFQN))
	}
	sort.Strings(edges)
	return declared(fg), edges
}

func trimTest(fqn string) string { return strings.ReplaceAll(fqn, "test.", "") }

func assertEdges(t *testing.T, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// declFQNs returns the sorted, deduplicated FQNs of the anchored declarations,
// leaving out the chunker's gap chunks (kind "block"), which the graph keeps
// for every tree-sitter language but which are not declarations under test.
func declFQNs(syms []gtypes.SymbolNode) []string {
	seen := make(map[string]bool)
	var out []string
	for _, s := range syms {
		if s.Kind == "block" {
			continue
		}
		if !seen[s.FQN] {
			seen[s.FQN] = true
			out = append(out, trimTest(s.FQN))
		}
	}
	sort.Strings(out)
	return out
}

// TestCallQueryElixirDeclarations: def/defp/defmodule are `call` nodes in the
// Elixir grammar. The chunker must extract their real names, and the call
// query must not turn the defining macros (or alias/import/use/require) into
// CALLS edges; the calls inside a definition are charged to it.
func TestCallQueryElixirDeclarations(t *testing.T) {
	src := `defmodule M do
  alias Foo.Bar
  import Baz
  use GenServer
  require Logger
  defstruct [:a]

  def foo do
    helper()
  end

  defp bar(x), do: other(x)
end
`
	syms, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "M::bar -> other", "M::foo -> helper")
	if got := strings.Join(declFQNs(syms), ","); got != "M,M::bar,M::foo" {
		t.Errorf("declarations = %s, want M,M::bar,M::foo", got)
	}
	for _, edge := range edges {
		callee := edge[strings.Index(edge, " -> ")+4:]
		if chunking.IsElixirNonCallKeyword(callee) {
			t.Errorf("definition/directive keyword %q emitted as a CALLS edge in %v", callee, edges)
		}
	}
}

// TestCallQueryElixirGuardedDefinitions: a `when` guard wraps the declared
// signature in a `binary_operator`, so the chunker declared no symbol and the
// header calls (`foo`, `is_integer`, `rem`) leaked as bogus edges charged to
// the file's <script> symbol.
func TestCallQueryElixirGuardedDefinitions(t *testing.T) {
	src := `defmodule M do
  def foo(n) when is_integer(n) do
    helper(n)
  end

  defguard is_even(n) when rem(n, 2) == 0
end
`
	syms, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "M::foo -> helper")
	if got := strings.Join(declFQNs(syms), ","); got != "M,M::foo,M::is_even" {
		t.Errorf("declarations = %s, want M,M::foo,M::is_even", got)
	}
	for _, edge := range edges {
		for _, bogus := range []string{" -> foo", " -> is_integer", " -> rem", " -> def", " -> defguard"} {
			if strings.HasSuffix(edge, bogus) {
				t.Errorf("definition header call %q emitted as a CALLS edge: %v", bogus, edges)
			}
		}
		if strings.HasPrefix(edge, "<script:") {
			t.Errorf("call charged to the script symbol despite a real declaration: %v", edges)
		}
	}
}

// TestCallQueryElixirRemoteCalls: the Elixir call query bound only an
// `identifier` target, so a remote call (`Mod.func(1)`, `A.B.helper(2)`) —
// whose target is a `dot` node — produced no edge at all.
func TestCallQueryElixirRemoteCalls(t *testing.T) {
	src := `defmodule M do
  def run do
    Mod.func(1)
    A.B.helper(2)
  end
end
`
	syms, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "M::run -> func", "M::run -> helper")
	if got := strings.Join(declFQNs(syms), ","); got != "M,M::run" {
		t.Errorf("declarations = %s, want M,M::run", got)
	}
}

// TestCallQueryRuby: the Ruby grammar exposes every call as a `call` node with
// the callee in its `method` field, so calls written without parentheses
// (`helper x`, `puts "hi"`) are found as well as receiver calls. A plain
// identifier (`y = plain`) and a `def` name are not calls.
func TestCallQueryRuby(t *testing.T) {
	src := `class Greeter
  def greet
    helper(x)
    helper x
    puts "hi #{@name}"
    self.salute
    Mod.call(x)
    y = plain_identifier
  end
end

def defined_but_never_called
end
`
	syms, edges := extractCalls(t, "greeter.rb", src)
	assertEdges(t, edges,
		"Greeter::greet -> call",
		"Greeter::greet -> helper",
		"Greeter::greet -> puts",
		"Greeter::greet -> salute",
	)
	declared := strings.Join(declFQNs(syms), ",")
	for _, want := range []string{"Greeter", "Greeter::greet", "defined_but_never_called"} {
		if !strings.Contains(declared, want) {
			t.Errorf("declaration %q missing from %s", want, declared)
		}
	}
	for _, bogus := range []string{"plain_identifier", "defined_but_never_called", "greet"} {
		for _, e := range edges {
			if strings.HasSuffix(e, " -> "+bogus) {
				t.Errorf("plain identifier %q emitted as a call: %v", bogus, edges)
			}
		}
	}
}

// TestCallQueryScala: Scala call_expression keeps the callee in its `function`
// field, a field_expression for `obj.run()`. Calls must be charged to the
// enclosing method, including a method of an `object`.
func TestCallQueryScala(t *testing.T) {
	src := `package x

class Box[T](value: T) {
  def printIt(): Unit = println("got")
}

object Main {
  def greet() = { helper(); obj.run(1) }
  def helper() = 1
}
`
	syms, edges := extractCalls(t, "Box.scala", src)
	assertEdges(t, edges,
		"Box::printIt -> println",
		"Main::greet -> helper",
		"Main::greet -> run",
	)
	if got := strings.Join(declFQNs(syms), ","); !strings.Contains(got, "Main::greet") {
		t.Errorf("Main::greet is not a declaration: %s", got)
	}
}

// TestCallQueryKotlin: the Kotlin chunker used to yield nameless declarations,
// so every call fell through to the file's <script> symbol. The real enclosing
// function/method must be the caller, and a navigation call (`a.b()`) must
// bind b, not a.
func TestCallQueryKotlin(t *testing.T) {
	src := `class Greet {
    fun hello() {
        helper()
        name.length()
    }
}

fun greet() {
    helper()
}
`
	syms, edges := extractCalls(t, "Greet.kt", src)
	assertEdges(t, edges,
		"Greet::hello -> helper",
		"Greet::hello -> length",
		"greet -> helper",
	)
	got := strings.Join(declFQNs(syms), ",")
	if got != "Greet,Greet::hello,greet" {
		t.Errorf("declarations = %s, want Greet,Greet::hello,greet", got)
	}
	for _, e := range edges {
		if strings.HasPrefix(e, "<script:") {
			t.Errorf("call charged to the script symbol despite real declarations: %v", edges)
		}
	}
}

func TestCallQuerySwift(t *testing.T) {
	src := `import Foundation

class Greeter {
    var name: String
    init(name: String) { self.name = name }
    func greet() { print("hi \(name)") }
}
`
	if syms := extract(t, "Greeter.swift", src); len(syms) < 1 {
		t.Errorf("expected ≥1 symbol in Swift, got %d", len(syms))
	}
}

func TestCallQueryPHP(t *testing.T) {
	src := `<?php
class Greeter {
    private $name;
    public function __construct($name) { $this->name = $name; }
    public function greet() { echo "hi " . $this->name; }
}
?>
`
	if syms := extract(t, "Greeter.php", src); len(syms) < 1 {
		t.Errorf("expected ≥1 symbol in PHP, got %d", len(syms))
	}
}

func TestCallQueryCPP(t *testing.T) {
	src := `#include <iostream>

class Greeter {
public:
    void greet() { std::cout << "hi" << std::endl; }
};
`
	if syms := extract(t, "greeter.cpp", src); len(syms) < 1 {
		t.Errorf("expected ≥1 symbol in C++, got %d", len(syms))
	}
}

// TestCallQueryLua: the bundled Lua grammar declares functions as
// function_statement nodes and calls as function_call nodes. Chunks must come
// from the AST (not the regex fallback), dotted and colon calls must bind the
// method, and bare/string/table-argument calls must bind the function.
func TestCallQueryLua(t *testing.T) {
	src := `local function helper() return 1 end
local M = {}
function M.run() helper() end
function M:send(x) self:helper(x) end
obj:method()
m.helper()
helper()
require "json"
return M
`
	syms, edges := extractCalls(t, "greeter.lua", src)
	assertEdges(t, edges,
		"<script:greeter_lua> -> helper",
		"<script:greeter_lua> -> method",
		"<script:greeter_lua> -> require",
		"M.run -> helper",
		"M:send -> helper",
	)
	got := strings.Join(declFQNs(syms), ",")
	if got != "<script:greeter_lua>,M.run,M:send,helper" {
		t.Errorf("declarations = %s, want <script:greeter_lua>,M.run,M:send,helper", got)
	}
}

func TestCallQueryUnknownLanguageSkipsCleanly(t *testing.T) {
	// An unrecognised extension must not crash the extractor; the chunker
	// may still produce a sliding-window chunk, so the count is not checked.
	_ = extract(t, "weird.zzz", "Some content that isn't code at all, just text.")
}

func TestCallQueryEmptySource(t *testing.T) {
	_ = extract(t, "empty.go", "")
}

func TestCallQueryWhitespaceOnly(t *testing.T) {
	_ = extract(t, "ws.go", "   \n\n\t\n")
}

func TestCallQueryHandlesNoCallExpressions(t *testing.T) {
	src := `package x
type Foo struct {
    A int
    B string
}
func (f Foo) String() string { return f.B }
`
	// Foo + String method → at least 2 symbols.
	if syms := extract(t, "data.go", src); len(syms) < 2 {
		t.Errorf("expected ≥2 symbols (type + method), got %d", len(syms))
	}
}

// TestCallQuerySvelteScriptCalls: the Svelte grammar keeps a <script> body as
// an unparsed raw_text node, so the extractor parses each inline block with the
// JavaScript/TypeScript grammar and shifts its lines back into the component.
// Declarations and CALLS inside <script> are real; the template markup is not
// scanned for calls.
func TestCallQuerySvelteScriptCalls(t *testing.T) {
	src := `<script>
  function helper() { return 1; }
  function f() {
    helper();
    console.log(1);
  }
</script>

<button on:click={f}>go</button>
`
	syms, edges := extractCalls(t, "App.svelte", src)
	assertEdges(t, edges, "f -> helper", "f -> log")

	byName := map[string]gtypes.SymbolNode{}
	for _, s := range syms {
		byName[s.Name] = s
	}
	f, ok := byName["f"]
	if !ok {
		t.Fatalf("no declaration for f in %v", declFQNs(syms))
	}
	if f.Line != 3 || f.EndLine != 6 {
		t.Errorf("f anchor = %d-%d, want 3-6 (shifted into the component)", f.Line, f.EndLine)
	}
	if h, ok := byName["helper"]; !ok || h.Line != 2 {
		t.Errorf("helper anchor = %+v, want line 2", h)
	}
}

// TestCallQuerySvelteTypeScriptScript uses the TypeScript grammar for a
// <script lang="ts"> block.
func TestCallQuerySvelteTypeScriptScript(t *testing.T) {
	src := `<script lang="ts">
  function f(): void {
    helper();
  }
</script>
`
	_, edges := extractCalls(t, "App.svelte", src)
	assertEdges(t, edges, "f -> helper")
}

// TestCallQuerySvelteWithoutScript: a template-only component is indexed
// cleanly (no panic, no declarations, no calls).
func TestCallQuerySvelteWithoutScript(t *testing.T) {
	fg := indexer.ExtractFile("test", "/r", "/r/App.svelte", "<div class=\"a\">{label}</div>\n")
	if len(fg.Calls) != 0 {
		t.Errorf("calls = %v, want none", fg.Calls)
	}
	for _, s := range fg.Symbols {
		if s.File != "" {
			t.Errorf("unexpected declaration %s", s.FQN)
		}
	}
}

// TestCallQueryLanguagesRegisteredSanity extracts a minimal snippet per
// binding-backed language: a missing call query still yields declarations,
// but a missing binding or a broken query must not panic.
func TestCallQueryLanguagesRegisteredSanity(t *testing.T) {
	for _, lang := range bindingLangs {
		t.Run(string(lang), func(t *testing.T) {
			rel, src := minimalSnippet(lang)
			_ = extract(t, rel, src)
		})
	}
}

var bindingLangs = []chunking.Language{
	chunking.LangPython, chunking.LangJavaScript, chunking.LangTypeScript,
	chunking.LangJava, chunking.LangC, chunking.LangCPP, chunking.LangRust,
	chunking.LangCSharp, chunking.LangRuby, chunking.LangPHP, chunking.LangKotlin,
	chunking.LangScala, chunking.LangSwift, chunking.LangLua, chunking.LangElixir,
	chunking.LangSvelte,
}

// minimalSnippet returns a tiny but realistic source for each language that
// exercises the chunker and (where the call query is registered) the call
// extractor.
func minimalSnippet(lang chunking.Language) (relPath, src string) {
	switch lang {
	case chunking.LangPython:
		return "m.py", "def f():\n    return 1\n"
	case chunking.LangJavaScript:
		return "m.js", "function f() { return 1; }\n"
	case chunking.LangTypeScript:
		return "m.ts", "function f(): number { return 1; }\n"
	case chunking.LangJava:
		return "M.java", "class M { int f() { return 1; } }\n"
	case chunking.LangC:
		return "m.c", "int f(void) { return 1; }\n"
	case chunking.LangCPP:
		return "m.cpp", "int f() { return 1; }\n"
	case chunking.LangRust:
		return "m.rs", "fn f() -> i32 { 1 }\n"
	case chunking.LangCSharp:
		return "M.cs", "class M { int F() { return 1; } }\n"
	case chunking.LangRuby:
		return "m.rb", "def f; 1; end\n"
	case chunking.LangPHP:
		return "m.php", "<?php function f() { return 1; }\n"
	case chunking.LangKotlin:
		return "M.kt", "fun f(): Int { return 1 }\n"
	case chunking.LangScala:
		return "M.scala", "object M { def f = 1 }\n"
	case chunking.LangSwift:
		return "m.swift", "func f() -> Int { return 1 }\n"
	case chunking.LangLua:
		return "m.lua", "function f() return 1 end\n"
	case chunking.LangElixir:
		return "m.ex", "defmodule M do\n  def f do\n    1\n  end\nend\n"
	case chunking.LangSvelte:
		return "App.svelte", "<script>\n  function f() { return 1; }\n</script>\n<p>{f()}</p>\n"
	}
	return "m.txt", ""
}

// TestCallQueryLanguagesHaveBindings: every language of minimalSnippet must
// have a tree-sitter binding.
func TestCallQueryLanguagesHaveBindings(t *testing.T) {
	for _, lang := range bindingLangs {
		if !chunking.IsTreeSitterLanguage(lang) {
			t.Errorf("language %s listed in minimalSnippet but has no binding", lang)
		}
	}
}

// TestCallQuerySnippetStringsAreValid is a sanity check on minimalSnippet.
func TestCallQuerySnippetStringsAreValid(t *testing.T) {
	for _, lang := range bindingLangs {
		rel, src := minimalSnippet(lang)
		if strings.TrimSpace(src) == "" {
			t.Errorf("language %s: snippet is empty", lang)
		}
		if rel == "" {
			t.Errorf("language %s: relPath is empty", lang)
		}
	}
}

// TestCallQueryElixirGuardedWrapperExpressions: the grammar wraps a
// parenthesized guard in a `block` node and a `not` guard in a
// `unary_operator` node. Both remain part of the definition header, so neither
// the guard check nor the signature may become a CALLS edge.
func TestCallQueryElixirGuardedWrapperExpressions(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		edges []string
	}{
		{
			name:  "parenthesized_guard",
			src:   "defmodule M do\n  def foo(x) when (is_integer(x)) do\n    work(x)\n  end\nend\n",
			edges: []string{"M::foo -> work"},
		},
		{
			name:  "not_guard",
			src:   "defmodule M do\n  def bar(x) when not is_map(x) do\n    work(x)\n  end\nend\n",
			edges: []string{"M::bar -> work"},
		},
		{
			name:  "nested_not_parenthesized",
			src:   "defmodule M do\n  def baz(x) when not (is_nil(x)) do\n    work(x)\n  end\nend\n",
			edges: []string{"M::baz -> work"},
		},
		{
			name:  "parenthesized_compound_guard",
			src:   "defmodule M do\n  def foo(x) when (is_integer(x) and x > 0) do\n    work(x)\n  end\nend\n",
			edges: []string{"M::foo -> work"},
		},
		{
			name:  "defguard_parenthesized_guard",
			src:   "defmodule M do\n  defguard is_even(n) when (rem(n, 2) == 0)\nend\n",
			edges: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, edges := extractCalls(t, "a.ex", tc.src)
			assertEdges(t, edges, tc.edges...)
		})
	}
}

// TestCallQueryElixirKeywordFormGuardedDefinition: the `do:` value of a
// keyword-form definition is a real call body, not part of the `when` guard.
func TestCallQueryElixirKeywordFormGuardedDefinition(t *testing.T) {
	src := "defmodule M do\n  def foo(x) when is_integer(x), do: helper(x)\nend\n"
	_, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "M::foo -> helper")
}

// TestCallQueryElixirDefaultArgumentWithGuard: a default argument sits inside
// the signature call's own argument list, not in the `when` guard, so the
// helper call it makes remains a real CALLS edge.
func TestCallQueryElixirDefaultArgumentWithGuard(t *testing.T) {
	src := "defmodule M do\n  def foo(x \\\\ helper()) when (is_map(x)) do\n    work(x)\n  end\nend\n"
	_, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "M::foo -> helper", "M::foo -> work")
}

// TestCallQueryElixirQualifiedKeywordCalls: a dot-qualified callee named after
// a control-flow macro (`Mod.if(x)`) is a real remote call; only an unqualified
// bare macro invocation is filtered.
func TestCallQueryElixirQualifiedKeywordCalls(t *testing.T) {
	src := "defmodule M do\n  def run(x) do\n    Mod.if(x)\n    MyDSL.unless(x)\n  end\nend\n"
	_, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "M::run -> if", "M::run -> unless")
}

// TestCallQueryElixirParenlessPipe: `xs |> helper` calls helper(xs), but the
// grammar models the pipe's RHS as a bare identifier rather than a call node.
func TestCallQueryElixirParenlessPipe(t *testing.T) {
	src := "defmodule M do\n  def run(xs) do\n    xs |> helper\n    xs |> Mod.func\n    xs |> Enum.map(&f/1)\n  end\nend\n"
	_, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "M::run -> func", "M::run -> helper", "M::run -> map")
}

// TestCallQueryElixirModuleAttributes: a module attribute is a
// `unary_operator(@, call(<name>, arguments(...)))`; its name is never a
// CALLS edge and a typespec attribute (`@spec`, `@type`, `@callback`, ...)
// declares types instead of calling functions. Only the value of a plain
// attribute (`@attr helper()`) is a real module-level call.
func TestCallQueryElixirModuleAttributes(t *testing.T) {
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
	syms, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges, "<script:a_ex> -> helper", "M::foo -> work")
	if got := strings.Join(declFQNs(syms), ","); got != "<script:a_ex>,M,M::foo" {
		t.Errorf("declarations = %s, want <script:a_ex>,M,M::foo", got)
	}
}

// TestCallQueryElixirAttributeValueCalls: the value expression of a non-typespec
// attribute is evaluated at compile time, so its calls are real CALLS edges,
// including the right-hand side of a parenless pipe.
func TestCallQueryElixirAttributeValueCalls(t *testing.T) {
	src := `defmodule M do
  @config build_config() |> finalize()
  @attr xs |> helper
  @attr helper()
end
`
	_, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges,
		"<script:a_ex> -> build_config",
		"<script:a_ex> -> finalize",
		"<script:a_ex> -> helper",
	)
}

// TestCallQueryElixirNestedGuardBuiltins: a guard-safe builtin nested inside
// another guard call's argument list is part of the defining macro's header at
// any depth, so it is not a CALLS edge.
func TestCallQueryElixirNestedGuardBuiltins(t *testing.T) {
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
			_, edges := extractCalls(t, "a.ex", tc.src)
			assertEdges(t, edges, "M::foo -> work")
		})
	}
}

// TestCallQueryElixirAttributeAndGuardPreservedCalls: the attribute and
// guard filters must not drop a default argument, an `unquote` in a macro head
// or a keyword-form `do:` body.
func TestCallQueryElixirAttributeAndGuardPreservedCalls(t *testing.T) {
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
	_, edges := extractCalls(t, "a.ex", src)
	assertEdges(t, edges,
		"M::foo -> bar",
		"M::foo -> baz",
		"M::foo -> work",
		"M::kw -> helper",
		"M::mac -> helper",
		"M::mac -> ok",
	)
}
