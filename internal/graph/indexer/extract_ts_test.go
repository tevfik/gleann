//go:build treesitter

package indexer

import (
	"sort"
	"strings"
	"testing"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
)

// tsExtract extracts one file as ExtractFile does for a root-level source of
// module "mod" and returns the CALLS edges as "caller -> callee" with the
// "mod." prefix dropped, sorted. It also checks the invariants every format-2
// graph relies on (AGENTS.md section 2): each edge's caller is a declaration
// with a concrete anchor, no declaration is a phantom, no FQN repeats.
func tsExtract(t *testing.T, file, src string) (FileGraph, []string) {
	t.Helper()
	fg := ExtractFile("mod", "/r", "/r/"+file, src)
	decl := map[string]gtypes.SymbolNode{}
	for _, s := range fg.Symbols {
		if s.File == "" {
			continue
		}
		if _, dup := decl[s.FQN]; dup {
			t.Errorf("%s: declaration %s emitted twice", file, s.FQN)
		}
		decl[s.FQN] = s
		if s.Line < 1 || s.EndLine < s.Line {
			t.Errorf("%s: declaration %s has no line anchor (%d-%d)", file, s.FQN, s.Line, s.EndLine)
		}
	}
	var edges []string
	for _, e := range fg.Calls {
		if _, ok := decl[e.CallerFQN]; !ok {
			t.Errorf("%s: edge %s -> %s has an undeclared caller", file, e.CallerFQN, e.CalleeFQN)
		}
		edges = append(edges, strings.ReplaceAll(e.CallerFQN, "mod.", "")+" -> "+strings.ReplaceAll(e.CalleeFQN, "mod.", ""))
	}
	sort.Strings(edges)
	return fg, edges
}

func TestExtractFileTreeSitterCalls(t *testing.T) {
	cases := []struct {
		file, src string
		want      []string
	}{
		{"a.py", "class A:\n    def m(self):\n        helper()\n        self.n()\n    def n(self):\n        pass\n\ndef helper():\n    pass\n\nhelper()\n",
			[]string{"<script:a_py> -> helper", "A::m -> helper", "A::m -> n"}},
		{"a.js", "class A {\n  m() { helper(); this.n(); }\n  n() {}\n}\nfunction helper() {}\nhelper();\n",
			[]string{"<script:a_js> -> helper", "A::m -> helper", "A::m -> n"}},
		{"a.ts", "class A {\n  m(): void { helper(); this.n(); }\n  n(): void {}\n}\nfunction helper() {}\nhelper();\n",
			[]string{"<script:a_ts> -> helper", "A::m -> helper", "A::m -> n"}},
		{"a.rs", "struct A;\nimpl A {\n    fn m(&self) { helper(); self.n(); }\n    fn n(&self) {}\n}\nfn helper() {}\nfn main() { helper(); }\n",
			[]string{"A::m -> helper", "A::m -> n", "main -> helper"}},
		{"a.c", "void helper(void) {}\nvoid m(void) { helper(); }\n",
			[]string{"m -> helper"}},
		// One-line class: the call goes to the method, which wins the tie.
		{"a.cpp", "void helper() {}\nclass A { public: void m() { helper(); n(); } void n() {} };\n",
			[]string{"A::n -> helper", "A::n -> n"}},
		{"A.java", "class A {\n  void m() { helper(); n(); }\n  void n() {}\n  static void helper() {}\n}\n",
			[]string{"A::m -> helper", "A::m -> n"}},
		{"a.cs", "class A {\n  void M() { Helper(); N(); }\n  void N() {}\n  static void Helper() {}\n}\n",
			[]string{"A::M -> Helper", "A::M -> N"}},
		{"a.rb", "class A\n  def m\n    helper()\n    n()\n  end\n  def n\n  end\nend\ndef helper\nend\n",
			[]string{"A::m -> helper", "A::m -> n"}},
		{"a.php", "<?php\nclass A {\n  function m() { helper(); $this->n(); }\n  function n() {}\n}\nfunction helper() {}\nhelper();\n",
			[]string{"<script:a_php> -> helper", "A::m -> helper", "A::m -> n"}},
		{"a.swift", "class A {\n  func m() { helper(); n() }\n  func n() {}\n}\nfunc helper() {}\n",
			[]string{"A::m -> helper", "A::m -> n"}},
		// The chunker finds no Kotlin declarations, so every call is a
		// module-level call of the anchored script symbol.
		{"a.kt", "class A {\n  fun m() { helper(); n() }\n  fun n() {}\n}\nfun helper() {}\n",
			[]string{"<script:a_kt> -> helper", "<script:a_kt> -> n"}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			_, got := tsExtract(t, c.file, c.src)
			want := append([]string{}, c.want...)
			sort.Strings(want)
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("calls =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

// The bundled Lua grammar yields no chunks, so Lua keeps the regex fallback:
// declarations, no calls (and nothing unanchored).
func TestExtractFileLuaDeclarationsOnly(t *testing.T) {
	fg, got := tsExtract(t, "a.lua", "function helper() end\nfunction m() helper() end\nhelper()\n")
	if len(got) != 0 {
		t.Errorf("calls = %v, want none", got)
	}
	if len(fg.Symbols) < 2 {
		t.Errorf("symbols = %d, want the regex-chunker declarations", len(fg.Symbols))
	}
}

// Elixir has no function-call syntax apart from def/defmodule macros, so the
// extractor sees them as calls; the edges are anchored like any other and the
// real callee is present.
func TestExtractFileElixirCalls(t *testing.T) {
	fg, got := tsExtract(t, "a.ex", "defmodule A do\n  def m do\n    helper()\n  end\n  def helper do\n  end\nend\n")
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "-> helper") {
		t.Errorf("no call to helper in %q", joined)
	}
	if len(fg.Calls) == 0 {
		t.Fatal("no calls extracted")
	}
}

func TestExtractFileScriptSymbolAnchoredAndNamedPerFile(t *testing.T) {
	src := "def helper():\n    pass\n\n\nhelper()\n"
	a := ExtractFile("mod", "/r", "/r/pkg/a.py", src)
	b := ExtractFile("mod", "/r", "/r/pkg/b.py", src)
	script := func(fg FileGraph) gtypes.SymbolNode {
		for _, s := range fg.Symbols {
			if s.Kind == "script" {
				return s
			}
		}
		t.Fatal("no script symbol")
		return gtypes.SymbolNode{}
	}
	sa, sb := script(a), script(b)
	if sa.FQN == sb.FQN {
		t.Errorf("files of one package share the script FQN %q: the later file's top-level calls would be dropped", sa.FQN)
	}
	if sa.File != "pkg/a.py" || sa.Name != "<script>" || sa.Line != 1 || sa.StartCol != 1 || sa.ByteOffset != 0 || sa.EndLine != 6 {
		t.Errorf("script anchor = %+v, want pkg/a.py <script> 1:1 offset 0 end line 6", sa)
	}
	declared := false
	for _, d := range a.Declares {
		if d.SymbolFQN == sa.FQN && d.FilePath == "pkg/a.py" {
			declared = true
		}
	}
	if !declared {
		t.Error("script symbol has no DECLARES edge")
	}
}

// A file without module-level calls gets no script symbol (no phantom nodes).
func TestExtractFileNoScriptWithoutTopLevelCalls(t *testing.T) {
	fg := ExtractFile("mod", "/r", "/r/a.py", "def helper():\n    pass\n\n\ndef run():\n    helper()\n")
	for _, s := range fg.Symbols {
		if s.Kind == "script" {
			t.Errorf("unexpected script symbol %s", s.FQN)
		}
	}
	if len(fg.Calls) != 1 {
		t.Errorf("calls = %d, want 1", len(fg.Calls))
	}
}

// Callees are named in the caller's directory, so a call to a function of
// another file in that directory has the FQN of the declaration there, and a
// call to something declared nowhere keeps a stub FQN (stored external).
func TestExtractFileCalleeResolvesInSameDirectory(t *testing.T) {
	fg := ExtractFile("mod", "/r", "/r/pkg/a.py", "def run():\n    other()\n    print(1)\n")
	want := map[string]bool{"mod/pkg.other": true, "mod/pkg.print": true}
	for _, e := range fg.Calls {
		delete(want, e.CalleeFQN)
		if e.CallerFQN != "mod/pkg.run" {
			t.Errorf("caller = %s", e.CallerFQN)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing callees %v", want)
	}
}

// Python methods called from a module function resolve to the class, the
// innermost declaration still being the method for calls made inside it.
func TestExtractFileMethodBodyCallsAreChargedToTheMethod(t *testing.T) {
	_, got := tsExtract(t, "a.py", "class R:\n    def add(self, x):\n        clean(x)\n\n    def other(self):\n        pass\n")
	if strings.Join(got, ",") != "R::add -> clean" {
		t.Errorf("calls = %v, want [R::add -> clean]", got)
	}
}
