//go:build !treesitter

package chunking

import "testing"

// Without -tags treesitter every tree-sitter entry point must degrade to a
// clean "unavailable" answer so callers fall back to regex chunking.
func TestTreeSitterStub_Degrades(t *testing.T) {
	if TreeSitterAvailable() {
		t.Error("TreeSitterAvailable() = true in a stub build")
	}
	for _, lang := range []Language{LangPython, LangGo, LangRust} {
		if IsTreeSitterLanguage(lang) {
			t.Errorf("IsTreeSitterLanguage(%s) = true in a stub build", lang)
		}
	}
	if p, tree, src, ok := ParseTree(LangPython, "x = 1"); ok || p != nil || tree != nil || src != nil {
		t.Errorf("ParseTree = (%v,%v,%v,%v), want all zero", p, tree, src, ok)
	}
	if q := GetOrCompileQuery(LangPython, "(identifier) @id"); q != nil {
		t.Errorf("GetOrCompileQuery = %v, want nil", q)
	}
	if got := ChunkCodeWithTree(LangPython, "a.py", "x = 1", nil, nil); got != nil {
		t.Errorf("ChunkCodeWithTree = %v, want nil", got)
	}
	c := NewASTChunker(DefaultASTChunkerConfig())
	if got := c.ChunkCodeWithTree(LangPython, "a.py", "x = 1", nil, nil); got != nil {
		t.Errorf("method ChunkCodeWithTree = %v, want nil", got)
	}
	if got := treeSitterChunk("x = 1", "a.py", LangPython, DefaultASTChunkerConfig()); got != nil {
		t.Errorf("treeSitterChunk = %v, want nil", got)
	}
}

// A non-Go source file must be chunked by the regex fallback in a stub build.
func TestChunkCode_PythonUsesRegexFallbackInStubBuild(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())
	chunks := c.ChunkCode("def one():\n    pass\n\ndef two():\n    pass\n", "m.py")
	if !containsAll(boundaryNames(chunks), "function:one", "function:two") {
		t.Errorf("chunks = %+v", chunks)
	}
}
