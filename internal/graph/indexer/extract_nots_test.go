//go:build !treesitter

package indexer

import "testing"

// Without tree-sitter a non-Go file keeps the chunker's regex declarations and
// has no CALLS edges and no synthetic script symbol; nothing panics or fails.
func TestExtractFileWithoutTreeSitterIsDeclarationsOnly(t *testing.T) {
	fg := ExtractFile("mod", "/r", "/r/a.py", "def helper():\n    pass\n\n\ndef run():\n    helper()\n\nrun()\n")
	if len(fg.Calls) != 0 {
		t.Errorf("calls = %d, want 0 without tree-sitter", len(fg.Calls))
	}
	decl := 0
	for _, s := range fg.Symbols {
		if s.Kind == "script" {
			t.Errorf("unexpected script symbol %s", s.FQN)
		}
		if s.File != "" {
			decl++
		}
	}
	if decl < 2 {
		t.Errorf("declarations = %d, want helper and run", decl)
	}
}
