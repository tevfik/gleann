package indexing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Every passage stores ext = chunking.FileExt of its source, whichever
// chunker produced it: "1.10" is a plain text file with the extension ".10".
func TestReadDocumentsStoresExtForEveryPath(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"1.10":      "version ten plain text\n",
		"notes.TXT": "some prose notes\n",
		"doc.md":    "# Title\n\nbody text\n",
		"main.go":   "package main\n\nfunc main() {}\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]string{"1.10": ".10", "notes.TXT": ".txt", "doc.md": ".md", "main.go": ".go"}
	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, NoPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, it := range items {
		src, _ := it.Metadata["source"].(string)
		if w, ok := want[src]; ok {
			seen[src] = true
			if it.Metadata["ext"] != w {
				t.Errorf("%s: ext = %v, want %q", src, it.Metadata["ext"], w)
			}
		}
	}
	for n := range want {
		if !seen[n] {
			t.Errorf("no passage for %s", n)
		}
	}
}
