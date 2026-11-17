//go:build cgo && treesitter

package chunking

import (
	"strings"
	"testing"
)

func TestParserPool_RoundTrip(t *testing.T) {
	p := GetParser(LangPython)
	if p == nil {
		t.Fatal("GetParser returned nil")
	}
	ReturnParser(LangPython, p)

	// The pooled parser must still parse after being reset and reused.
	parser, tree, src, ok := ParseTree(LangPython, "x = 1\n")
	if !ok {
		t.Fatal("ParseTree failed after the parser was returned to the pool")
	}
	defer ReturnParser(LangPython, parser)
	defer tree.Close()
	if got := tree.RootNode().Type(); got != "module" || string(src) != "x = 1\n" {
		t.Errorf("root = %q src = %q", got, src)
	}
}

func TestTreeSitterLanguageSupport(t *testing.T) {
	tests := []struct {
		lang Language
		want bool
	}{
		{LangPython, true},
		{LangRust, true},
		{LangObjectiveC, true},
		{LangGo, false}, // go/ast handles Go
		{LangZig, false},
		{LangUnknown, false},
	}
	for _, tc := range tests {
		if got := IsTreeSitterLanguage(tc.lang); got != tc.want {
			t.Errorf("IsTreeSitterLanguage(%s) = %v, want %v", tc.lang, got, tc.want)
		}
	}
	if _, _, _, ok := ParseTree(LangGo, "package p"); ok {
		t.Error("ParseTree(LangGo) must report ok=false")
	}
	if got := treeSitterChunk("package p", "p.go", LangGo, DefaultASTChunkerConfig()); got != nil {
		t.Errorf("treeSitterChunk for an unbound language = %v, want nil", got)
	}
}

func TestGetOrCompileQuery_NegativeResultsAreCached(t *testing.T) {
	// Bad S-expression for a bound language.
	for i := 0; i < 2; i++ {
		if q := GetOrCompileQuery(LangPython, "(this_is_not_a_node_type) @x"); q != nil {
			t.Fatalf("attempt %d: bad query compiled to %v", i, q)
		}
	}
	// Language with no grammar binding.
	for i := 0; i < 2; i++ {
		if q := GetOrCompileQuery(LangGo, "(identifier) @x"); q != nil {
			t.Fatalf("attempt %d: unbound language compiled to %v", i, q)
		}
	}
	// A good query is cached and returns the same compiled object.
	q1 := GetOrCompileQuery(LangPython, "(identifier) @id")
	q2 := GetOrCompileQuery(LangPython, "(identifier) @id")
	if q1 == nil || q1 != q2 {
		t.Errorf("good query: q1=%v q2=%v, want the same non-nil query", q1, q2)
	}
}

func TestSymbolQuery_UnboundLanguageAndCaching(t *testing.T) {
	if _, ok := buildSymbolQuery(LangGo); ok {
		t.Error("buildSymbolQuery(LangGo) must have no rules")
	}
	if q := GetOrCompileSymbolQuery(LangGo); q != nil {
		t.Errorf("GetOrCompileSymbolQuery(LangGo) = %v, want nil", q)
	}
	q1 := GetOrCompileSymbolQuery(LangPython)
	q2 := GetOrCompileSymbolQuery(LangPython)
	if q1 == nil || q1 != q2 {
		t.Errorf("symbol query not cached: %v vs %v", q1, q2)
	}
}

func TestTreeSitterChunk_NoSemanticNodesReturnsNil(t *testing.T) {
	cfg := DefaultASTChunkerConfig()
	if got := treeSitterChunk("# only a comment\nx = 1\n", "a.py", LangPython, cfg); got != nil {
		t.Errorf("chunks = %+v, want nil so the caller falls back", got)
	}
	// ChunkCode still produces output through the regex/sliding-window fallback.
	c := NewASTChunker(cfg)
	if got := c.ChunkCode("# only a comment\nx = 1\n", "a.py"); len(got) == 0 {
		t.Error("ChunkCode returned nothing for a file without declarations")
	}
}

func TestTreeSitterChunk_GapBetweenDeclarationsBecomesBlock(t *testing.T) {
	src := "def a():\n    return 1\n\nCONSTANT = 42\nOTHER = 'x'\n\ndef b():\n    return 2\n"
	c := NewASTChunker(DefaultASTChunkerConfig())
	chunks := c.ChunkCode(src, "gap.py")
	var gap *CodeChunk
	for i := range chunks {
		if chunks[i].NodeType == "block" && strings.Contains(chunks[i].Text, "CONSTANT = 42") {
			gap = &chunks[i]
		}
	}
	if gap == nil {
		t.Fatalf("no block chunk with the top-level assignments in %+v", chunks)
	}
	if gap.StartLine != 3 || gap.EndLine != 6 || !strings.HasPrefix(gap.Name, "gap_") || gap.Metadata["parser"] != "tree-sitter" {
		t.Errorf("gap chunk = %+v", gap)
	}
}

func TestChunkCodeWithTree_Variants(t *testing.T) {
	src := "def a():\n    return 1\n\ndef b():\n    return 2\n"
	parser, tree, srcBytes, ok := ParseTree(LangPython, src)
	if !ok {
		t.Fatal("ParseTree failed")
	}
	defer ReturnParser(LangPython, parser)
	defer tree.Close()

	if got := ChunkCodeWithTree(LangPython, "a.py", src, nil, srcBytes); got != nil {
		t.Errorf("nil tree: %v, want nil", got)
	}
	// A language with no node rules cannot be chunked from a python tree.
	if got := ChunkCodeWithTree(LangGo, "a.go", src, tree, srcBytes); got != nil {
		t.Errorf("language without rules: %v, want nil", got)
	}
	free := ChunkCodeWithTree(LangPython, "a.py", src, tree, srcBytes)
	if len(free) < 2 {
		t.Fatalf("function form returned %d chunks, want at least 2", len(free))
	}

	c := NewASTChunker(DefaultASTChunkerConfig())
	if got := c.ChunkCodeWithTree(LangPython, "a.py", src, nil, srcBytes); got != nil {
		t.Errorf("method with nil tree: %v, want nil", got)
	}
	method := c.ChunkCodeWithTree(LangPython, "a.py", src, tree, srcBytes)
	if len(method) != len(free) {
		t.Errorf("method returned %d chunks, function %d", len(method), len(free))
	}
	// Same result as parsing from scratch.
	if direct := c.ChunkCode(src, "a.py"); len(direct) != len(method) {
		t.Errorf("ChunkCode returned %d chunks, ChunkCodeWithTree %d", len(direct), len(method))
	}

	// A tree without declarations yields nil from the method too.
	p2, t2, b2, ok := ParseTree(LangPython, "x = 1\n")
	if !ok {
		t.Fatal("ParseTree failed")
	}
	defer ReturnParser(LangPython, p2)
	defer t2.Close()
	if got := c.ChunkCodeWithTree(LangPython, "x.py", "x = 1\n", t2, b2); got != nil {
		t.Errorf("declaration-free tree: %v, want nil", got)
	}

	// Line numbers are applied per config.
	cfg := DefaultASTChunkerConfig()
	cfg.AddLineNumbers = true
	numbered := NewASTChunker(cfg).ChunkCodeWithTree(LangPython, "a.py", src, tree, srcBytes)
	if len(numbered) == 0 || !strings.Contains(numbered[0].Text, "|") {
		t.Errorf("numbered chunks = %+v, want line-number prefixes", numbered)
	}
}

func TestTreeSitterChunk_NameExtraction(t *testing.T) {
	tests := []struct {
		name     string
		file     string
		source   string
		wantName string
	}{
		{"c++ template function", "t.cpp", "template <typename T>\nT add(T a, T b) {\n    return a + b;\n}\n", "add"},
		{"rust impl block", "i.rs", "struct Widget {}\n\nimpl Widget {\n    fn new() -> Widget { Widget {} }\n}\n", "Widget"},
		{"c macro", "m.c", "#define LIMIT 10\n\nint main(void) {\n    return LIMIT;\n}\n", "LIMIT"},
		{"c function-like macro", "m2.c", "#define SQUARE(x) ((x) * (x))\n\nint f(void) {\n    return SQUARE(2);\n}\n", "SQUARE"},
		{"c pointer-returning function", "p.c", "char *dup(const char *s) {\n    return 0;\n}\n", "dup"},
	}
	c := NewASTChunker(DefaultASTChunkerConfig())
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := c.ChunkCode(tc.source, tc.file)
			var names []string
			for _, ch := range chunks {
				names = append(names, ch.Name)
			}
			if !containsAll(names, tc.wantName) {
				t.Errorf("chunk names %v do not include %q", names, tc.wantName)
			}
		})
	}
}

func TestExtractCallsDFS_FindsCallsInTree(t *testing.T) {
	parser, tree, src, ok := ParseTree(LangPython, "def f():\n    g()\n    h(g())\n")
	if !ok {
		t.Fatal("ParseTree failed")
	}
	defer ReturnParser(LangPython, parser)
	defer tree.Close()
	got := extractCallsDFS(tree.RootNode(), src)
	if !containsAll(got, "g", "h") {
		t.Errorf("calls = %v, want g and h", got)
	}
}
