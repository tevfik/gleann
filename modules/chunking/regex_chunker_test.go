package chunking

import (
	"fmt"
	"strings"
	"testing"
)

// boundaryNames returns "<nodeType>:<name>" for every non-preamble chunk.
func boundaryNames(chunks []CodeChunk) []string {
	var out []string
	for _, c := range chunks {
		if c.NodeType == "preamble" {
			continue
		}
		out = append(out, c.NodeType+":"+c.Name)
	}
	return out
}

func containsAll(got []string, want ...string) bool {
	set := make(map[string]bool, len(got))
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// TestChunkByRegex_PerLanguage exercises the regex fallback for every
// language family it dispatches on (it is the only strategy in a build
// without -tags treesitter).
func TestChunkByRegex_PerLanguage(t *testing.T) {
	tests := []struct {
		lang   Language
		source string
		want   []string
	}{
		{LangPython, "import os\n\nclass Foo:\n    def bar(self):\n        pass\n\ndef baz():\n    pass\n\nasync def qux():\n    pass\n",
			[]string{"class:Foo", "method:bar", "function:baz", "function:qux"}},
		{LangJavaScript, "import x from 'x'\n\nexport class A {}\nexport async function run() {}\nconst f = async (a) => a\nexport interface I {}\nexport type T = string\n",
			[]string{"class:A", "function:run", "function:f", "interface:I", "type:T"}},
		{LangVue, "export default {}\nfunction setup() {}\n", []string{"function:setup"}},
		{LangJava, "package p;\n\npublic class Main {\n}\npublic interface Svc {\n}\n",
			[]string{"class:Main", "interface:Svc"}},
		{LangKotlin, "public class K {\n}\n", []string{"class:K"}},
		{LangC, "#define MAX 10\nstruct point {\n};\nstatic int add(int a, int b) {\n  return a + b;\n}\nnamespace ns {\n}\n",
			[]string{"macro:MAX", "class:point", "function:add", "namespace:ns"}},
		{LangRust, "use std::io;\n\npub fn main() {}\nfn helper() {}\npub struct S {}\nenum E {}\npub trait Tr {}\nimpl S {}\npub mod m {}\n",
			[]string{"function:main", "function:helper", "struct:S", "enum:E", "trait:Tr", "impl:S", "module:m"}},
		{LangZig, "pub fn main() void {}\n", []string{"function:main"}},
		{LangRuby, "module Util\nend\nclass Foo\n  def bar\n  end\nend\n", []string{"module:Util", "class:Foo", "function:bar"}},
		{LangElixir, "defmodule App do\n  def run do\n  end\nend\n", []string{"module:App", "function:run"}},
		{LangPHP, "<?php\nclass A {\n  public function go() {}\n}\ninterface I {}\ntrait T {}\n", []string{"class:A", "function:go", "interface:I", "trait:T"}},
		{LangSwift, "import Foundation\npublic func run() {}\nclass C {}\nstruct S {}\nprotocol P {}\nenum E {}\n",
			[]string{"function:run", "class:C", "struct:S", "interface:P", "enum:E"}},
		{LangLua, "local function a()\nend\nfunction M.b()\nend\nc = function()\nend\n", []string{"function:a", "function:M.b", "function:c"}},
		{LangPowerShell, "Function Get-Thing {\n}\nclass Foo {\n}\nfilter Bar {\n}\n", []string{"function:Get-Thing", "class:Foo", "function:Bar"}},
		{LangJulia, "module M\nfunction f()\nend\nmacro m()\nend\nmutable struct S\nend\nabstract type A end\nend\n",
			[]string{"module:M", "function:f", "function:m", "struct:S", "type:A"}},
	}
	for _, tc := range tests {
		t.Run(string(tc.lang), func(t *testing.T) {
			c := NewASTChunker(DefaultASTChunkerConfig())
			chunks := c.chunkByRegex(tc.source, "file", tc.lang)
			got := boundaryNames(chunks)
			if !containsAll(got, tc.want...) {
				t.Errorf("boundaries %v do not include %v", got, tc.want)
			}
			for _, ch := range chunks {
				if ch.StartLine < 1 || ch.EndLine < ch.StartLine {
					t.Errorf("bad line range on %+v", ch)
				}
				if ch.Metadata["language"] != string(tc.lang) {
					t.Errorf("language metadata = %v, want %s", ch.Metadata["language"], tc.lang)
				}
			}
		})
	}
}

func TestChunkByRegex_UnknownLanguageUsesSlidingWindow(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())
	chunks := c.chunkByRegex("line one\nline two\n", "x.txt", LangUnknown)
	if len(chunks) != 1 || chunks[0].NodeType != "block" {
		t.Errorf("chunks = %+v, want a single sliding-window block", chunks)
	}
}

func TestChunkByPattern_PreambleAndNoBoundaries(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())

	src := "# header comment\nimport os\n\ndef first():\n    pass\n"
	chunks := c.chunkByRegex(src, "m.py", LangPython)
	if len(chunks) != 2 || chunks[0].NodeType != "preamble" || chunks[0].EndLine != 3 || chunks[1].Name != "first" {
		t.Errorf("chunks = %+v, want preamble then function first", chunks)
	}

	// No boundary at all: falls back to the sliding window.
	plain := c.chunkByRegex("x = 1\ny = 2\n", "m.py", LangPython)
	if len(plain) != 1 || plain[0].NodeType == "function" {
		t.Errorf("chunks = %+v, want one sliding-window chunk", plain)
	}
}

func TestChunkByPattern_SignaturesOnly(t *testing.T) {
	cfg := DefaultASTChunkerConfig()
	cfg.SignaturesOnly = true
	c := NewASTChunker(cfg)
	src := "def long_one(a,\n             b):\n    x = 1\n    y = 2\n\nclass K:\n    pass\n"
	chunks := c.chunkByRegex(src, "m.py", LangPython)
	var fn *CodeChunk
	for i := range chunks {
		if chunks[i].Name == "long_one" {
			fn = &chunks[i]
		}
	}
	if fn == nil {
		t.Fatalf("function chunk missing in %+v", chunks)
	}
	if strings.Contains(fn.Text, "x = 1") || !strings.HasSuffix(strings.TrimSpace(fn.Text), "b):") || fn.EndLine != 2 {
		t.Errorf("signature chunk = %q (end %d), want only the two-line signature", fn.Text, fn.EndLine)
	}
}

func TestChunkByPattern_SignaturesOnlyUnterminatedHeader(t *testing.T) {
	cfg := DefaultASTChunkerConfig()
	cfg.SignaturesOnly = true
	c := NewASTChunker(cfg)
	// No '{' or trailing ':' within five lines: the signature is capped at five lines.
	src := "def f(a,\n  b,\n  c,\n  d,\n  e,\n  g,\n  h):\n    pass\n"
	chunks := c.chunkByRegex(src, "m.py", LangPython)
	if len(chunks) == 0 || chunks[0].EndLine != 5 {
		t.Errorf("chunks = %+v, want the signature capped at 5 lines", chunks)
	}
}

func TestChunkGo_FallbackToPatternsOnSyntaxError(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())
	src := "package p\n\nfunc (r *T) Method( {\n\nfunc Free(\n\ntype Shape struct {\n\ntype Doer interface {\n"
	chunks := c.ChunkCode(src, "broken.go")
	got := boundaryNames(chunks)
	if !containsAll(got, "method:r", "function:Free", "struct:Shape", "interface:Doer") {
		t.Errorf("boundaries = %v, want regex fallback symbols", got)
	}
}

func TestChunkGo_DeclarationKinds(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())
	src := `package p

import (
	"fmt"
)

const Pi = 3.14

var Global = 1

type Alias = int

type Shape struct{ X int }

type Doer interface{ Do() }

type Plain int

func (s *Shape) Area() int { return fmt.Sprint(s.X) == "" }

func Free() {}
`
	chunks := c.ChunkCode(src, "decls.go")
	got := boundaryNames(chunks)
	for _, want := range []string{"struct:Shape", "interface:Doer", "type:Plain", "function:Free"} {
		if !containsAll(got, want) {
			t.Errorf("boundaries %v missing %s", got, want)
		}
	}
	if chunks[0].NodeType != "preamble" {
		t.Errorf("first chunk = %q, want preamble covering the import block", chunks[0].NodeType)
	}
}

func TestChunkGo_OnlyPackageClauseUsesSlidingWindow(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())
	chunks := c.ChunkCode("package p\n", "empty.go")
	if len(chunks) != 1 {
		t.Errorf("chunks = %+v, want a single sliding-window chunk", chunks)
	}
}

func TestChunkSlidingWindow_OverlapsAndCoversFile(t *testing.T) {
	cfg := DefaultASTChunkerConfig()
	cfg.MaxChunkSize = 400 // 10 lines per window
	cfg.ChunkOverlap = 80  // 2 lines of overlap
	c := NewASTChunker(cfg)
	var sb strings.Builder
	for i := 1; i <= 35; i++ {
		fmt.Fprintf(&sb, "line %02d\n", i)
	}
	chunks := c.chunkSlidingWindow(sb.String(), "notes.txt", LangUnknown)
	if len(chunks) < 4 {
		t.Fatalf("got %d chunks, want at least 4 windows", len(chunks))
	}
	if chunks[1].StartLine != chunks[0].EndLine-1 {
		t.Errorf("window 2 starts at %d, want overlap of 2 lines with window 1 ending at %d", chunks[1].StartLine, chunks[0].EndLine)
	}
	if !strings.Contains(chunks[len(chunks)-1].Text, "line 35") {
		t.Errorf("last window %q does not reach the end of the file", chunks[len(chunks)-1].Text)
	}
}

func TestChunkSlidingWindow_OverlapLargerThanWindow(t *testing.T) {
	cfg := DefaultASTChunkerConfig()
	cfg.MaxChunkSize = 400
	cfg.ChunkOverlap = 4000 // more overlap than window: step is clamped to 1
	c := NewASTChunker(cfg)
	src := strings.Repeat("x\n", 14)
	chunks := c.chunkSlidingWindow(src, "n.txt", LangUnknown)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want several overlapping windows", len(chunks))
	}
	if chunks[1].StartLine != 2 {
		t.Errorf("second window starts at line %d, want 2 (step clamped to 1)", chunks[1].StartLine)
	}
}

func TestSplitOversizedChunk_OverlapClampedToWindow(t *testing.T) {
	cfg := DefaultASTChunkerConfig()
	cfg.MaxChunkSize = 400
	cfg.ChunkOverlap = 4000
	c := NewASTChunker(cfg)
	text := strings.Repeat("some code line\n", 40)
	parts := c.splitOversizedChunk(CodeChunk{Text: text, StartLine: 1, EndLine: 41, NodeType: "function", Name: "f", Metadata: map[string]any{"k": "v"}})
	if len(parts) < 3 {
		t.Fatalf("got %d parts, want the chunk split into several", len(parts))
	}
	for _, p := range parts {
		if !strings.HasPrefix(p.Name, "f_part") || p.NodeType != "function" {
			t.Errorf("part lost identity: %+v", p)
		}
	}
}

func TestIsTestSymbol(t *testing.T) {
	tests := []struct {
		path, name string
		want       bool
	}{
		{"a/b_test.go", "X", true},
		{"test_foo.py", "x", true},
		{"foo_test.py", "x", true},
		{"a.spec.ts", "x", true},
		{"a.spec.js", "x", true},
		{"a.test.ts", "x", true},
		{"a.test.js", "x", true},
		{"FooTest.java", "x", true},
		{"main.go", "TestThing", true},
		{"main.go", "BenchmarkThing", true},
		{"main.go", "FuzzThing", true},
		{"main.go", "test_thing", true},
		{"main.go", "Handler", false},
	}
	for _, tc := range tests {
		if got := IsTestSymbol(tc.path, tc.name); got != tc.want {
			t.Errorf("IsTestSymbol(%q,%q) = %v, want %v", tc.path, tc.name, got, tc.want)
		}
	}
}

func TestChunkWithMetadata_FilenameSourcesAndHeaders(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())
	py := "import os\n\ndef greet():\n    return 1\n"

	for _, key := range []string{"file", "file_path", "source"} {
		items := c.ChunkWithMetadata(py, map[string]any{key: "pkg/mod.py"})
		if len(items) == 0 {
			t.Fatalf("%s: no chunks", key)
		}
		if got := items[0].Metadata["lang"]; got != "python" {
			t.Errorf("%s: lang = %v, want python", key, got)
		}
		if !strings.HasPrefix(items[0].Text, "# file: pkg/mod.py") {
			t.Errorf("%s: header = %q, want python comment prefix", key, items[0].Text)
		}
	}

	items := c.ChunkWithMetadata("package p\n\nfunc Run() {}\n", map[string]any{"file": "vendor/lib/run.go"})
	var fn *Chunk
	for i := range items {
		if items[i].Metadata["name"] == "Run" {
			fn = &items[i]
		}
	}
	if fn == nil {
		t.Fatalf("function chunk missing: %+v", items)
	}
	if fn.Metadata["is_vendor"] != true || fn.Metadata["is_test"] != false || fn.Metadata["fqn"] != "vendor/lib/run.go:Run" {
		t.Errorf("metadata = %+v", fn.Metadata)
	}
	if !strings.HasPrefix(fn.Text, "// file: vendor/lib/run.go") {
		t.Errorf("header = %q", fn.Text)
	}
}

func TestChunkWithMetadata_NoSymbolsFallsBackToFileChunk(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())
	if items := c.ChunkWithMetadata("   \n", nil); len(items) != 0 {
		t.Errorf("whitespace-only text produced %d chunks, want 0", len(items))
	}
	// Chunk (no metadata) returns plain strings.
	if got := c.Chunk("package p\n\nfunc A() {}\n"); len(got) == 0 {
		t.Error("Chunk returned nothing for a source file")
	}
}

func TestAddLineNumbers_WidthTracksLastLine(t *testing.T) {
	got := addLineNumbers("a\nb\nc", 8)
	want := " 8|a\n 9|b\n10|c"
	if got != want {
		t.Errorf("addLineNumbers = %q, want %q", got, want)
	}
}

func TestCopyMetadata_NilAndIndependence(t *testing.T) {
	if m := copyMetadata(nil); m == nil || len(m) != 0 {
		t.Errorf("copyMetadata(nil) = %v, want empty map", m)
	}
	src := map[string]any{"a": 1}
	cp := copyMetadata(src)
	cp["a"] = 2
	if src["a"] != 1 {
		t.Error("copy shares storage with the source")
	}
}
