package chunking

import "testing"

func TestFileExt(t *testing.T) {
	for in, want := range map[string]string{
		"a/b.go":        ".go",
		"README.MD":     ".md",
		"1.10":          ".10",
		"Makefile":      "",
		"v1.2/Makefile": "",
		".gitignore":    ".gitignore",
		"x.tar.gz":      ".gz",
		"":              "",
	} {
		if got := FileExt(in); got != want {
			t.Errorf("FileExt(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every chunker stores the same ext for a source.
func TestChunkersStoreFileExt(t *testing.T) {
	md := NewMarkdownChunker(500, 0).ChunkMarkdown("plain words\n", "Docs/Notes.MD")
	if len(md) == 0 || md[0].Metadata["ext"] != ".md" {
		t.Errorf("markdown ext = %v", md)
	}
	ast := NewASTChunker(DefaultASTChunkerConfig()).ChunkWithMetadata("hello\n", map[string]any{"file": "v1.2/Makefile"})
	if len(ast) == 0 || ast[0].Metadata["ext"] != "" {
		t.Errorf("extensionless code ext = %v", ast)
	}
}
