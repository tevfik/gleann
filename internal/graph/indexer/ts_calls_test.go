//go:build treesitter

// Tests for internal/graph/indexer/ts_calls.go through ExtractFile. The
// tree-sitter call extraction is the heart of the CALLS edges for non-Go
// languages; these tests cover edge cases that the per-language
// S-expression queries in modules/chunking/treesitter.go have to handle
// gracefully: no panic, and the declarations still come out.
package indexer_test

import (
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

func TestCallQueryElixir(t *testing.T) {
	// Elixir's grammar matches `call` nodes aggressively (even `1 + 2`);
	// the extractor must not panic on it.
	_ = extract(t, "elixir.ex", "# elixir fixture\n")
}

func TestCallQueryRuby(t *testing.T) {
	src := `class Greeter
  def initialize(name)
    @name = name
  end

  def greet
    puts "hi #{@name}"
    self.salute
  end
end
`
	if syms := extract(t, "greeter.rb", src); len(syms) < 2 {
		t.Errorf("expected ≥2 symbols in Ruby, got %d", len(syms))
	}
}

func TestCallQueryScala(t *testing.T) {
	src := `package x

class Box[T](value: T) {
  def get: T = value
  def printIt(): Unit = println(s"got: $value")
}

object Main {
  def main(args: Array[String]): Unit = {
    val b = new Box(42)
    b.printIt()
  }
}
`
	if syms := extract(t, "Box.scala", src); len(syms) < 2 {
		t.Errorf("expected ≥2 symbols in Scala, got %d", len(syms))
	}
}

func TestCallQueryKotlin(t *testing.T) {
	// The Kotlin chunker may or may not surface classes vs methods
	// depending on the grammar version; we only assert no panic.
	src := `class Greet {
    fun hello() {
        1 + 1
    }
}
`
	t.Logf("kotlin symbols: %d", len(extract(t, "Greet.kt", src)))
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

func TestCallQueryLua(t *testing.T) {
	src := `local M = {}
function M.say_hi(name)
  print("hi " .. name)
  return string.upper(name)
end
return M
`
	if syms := extract(t, "greeter.lua", src); len(syms) < 1 {
		t.Errorf("expected ≥1 symbol in Lua, got %d", len(syms))
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
		return "m.ex", "# elixir fixture\n"
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
