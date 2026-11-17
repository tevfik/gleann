package graphstore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestDocumentTOCSiblingOrder pins the sibling order of the TOC tree: level
// first, then title, then node ID for headings that share both (map
// iteration must not leak into the result).
func TestDocumentTOCSiblingOrder(t *testing.T) {
	g, _ := openTestGraph(t)
	insertDocNodes(t, g, []DocNode{
		{ID: "doc1", Type: "document", Name: "Guide", RPath: "guide.md"},
		{ID: "hz", Type: "heading", Parent: "doc1", Name: "Appendix", Level: 2},
		{ID: "hb", Type: "heading", Parent: "doc1", Name: "Notes", Level: 1},
		{ID: "ha", Type: "heading", Parent: "doc1", Name: "Notes", Level: 1},
		{ID: "hc", Type: "heading", Parent: "doc1", Name: "Intro", Level: 1},
	})
	want := []Heading{
		{ID: "hc", Title: "Intro", Level: 1},
		{ID: "ha", Title: "Notes", Level: 1},
		{ID: "hb", Title: "Notes", Level: 1},
		{ID: "hz", Title: "Appendix", Level: 2},
	}
	// Repeat: a comparator that ignored a key would show up as a flaky order.
	for i := 0; i < 5; i++ {
		toc, err := g.DocumentTOC("guide.md")
		if err != nil {
			t.Fatalf("DocumentTOC: %v", err)
		}
		if toc.Folder != "" {
			t.Fatalf("folder = %q, want empty for a document without a folder", toc.Folder)
		}
		if !reflect.DeepEqual(toc.Headings, want) {
			t.Fatalf("TOC = %+v, want %+v", toc.Headings, want)
		}
	}
}

// TestFullDocumentUnindexedFileOnDisk: a path the graph does not know is
// still served from disk when the file exists; only a path that is neither
// indexed nor on disk is an error.
func TestFullDocumentUnindexedFileOnDisk(t *testing.T) {
	g, _ := openTestGraph(t)
	path := filepath.Join(t.TempDir(), "loose.md")
	if err := os.WriteFile(path, []byte("loose notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := g.FullDocument(path)
	if err != nil || got != "loose notes" {
		t.Fatalf("FullDocument(unindexed file) = %q, %v; want the disk content", got, err)
	}

	// An empty file on disk does not count as content: the lookup error wins.
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.FullDocument(empty); err == nil {
		t.Fatal("FullDocument(empty unindexed file) succeeded, want the not-found error")
	}
}
