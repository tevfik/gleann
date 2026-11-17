package graphstore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestDocumentReadEdgeCases: a missing document is ErrDocumentNotFound for
// every reader; a document with no name or no folder row still gets a
// breadcrumb and a title; a document with neither a file on disk nor chunks
// has no content.
func TestDocumentReadEdgeCases(t *testing.T) {
	g, _ := openTestGraph(t)
	insertDocNodes(t, g, []DocNode{
		// No name, and its parent folder row does not exist.
		{ID: "orphan", Type: "document", Parent: "gone-folder", RPath: "notes/orphan.md"},
		{ID: "oh1", Type: "heading", Parent: "orphan", Name: "Only", Level: 1},
	})

	for name, read := range map[string]func() error{
		"DocumentContext": func() error { _, err := g.DocumentContext("nope.md"); return err },
		"DocumentTOC":     func() error { _, err := g.DocumentTOC("nope.md"); return err },
		"DocumentSymbols": func() error { _, err := g.DocumentSymbols("nope.md"); return err },
		"FullDocument":    func() error { _, err := g.FullDocument("nope.md"); return err },
	} {
		if err := read(); !errors.Is(err, ErrDocumentNotFound) {
			t.Errorf("%s(missing) error = %v, want ErrDocumentNotFound", name, err)
		}
	}

	ctxt, err := g.DocumentContext("notes/orphan.md")
	if err != nil {
		t.Fatalf("DocumentContext: %v", err)
	}
	if ctxt.FolderName != "" || ctxt.Breadcrumb != "notes/orphan.md > Only" {
		t.Fatalf("DocumentContext = %+v, want no folder and the rpath as the name", ctxt)
	}
	docs, err := g.ListDocuments()
	if err != nil || len(docs) != 1 || docs[0].Title != "notes/orphan.md" || docs[0].HeadingCount != 1 {
		t.Fatalf("ListDocuments = %+v, %v; want the rpath as the title and 1 heading", docs, err)
	}
	if _, err := g.FullDocument("notes/orphan.md"); err == nil || !strings.Contains(err.Error(), "no content found") {
		t.Fatalf("FullDocument without chunks or file: error = %v", err)
	}
}

// TestCorruptDocumentRows: a heading level of the wrong type makes the
// document readers fail instead of returning a partial tree.
func TestCorruptDocumentRows(t *testing.T) {
	ctx := context.Background()
	g, s := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	if _, err := s.WriterDB().ExecContext(ctx, `UPDATE doc_nodes SET level = 'deep' WHERE id = 'h2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.DocumentContext("docs/guide.md"); err == nil {
		t.Error("DocumentContext over a corrupt heading: nil error")
	}
	if _, err := g.DocumentTOC("docs/guide.md"); err == nil {
		t.Error("DocumentTOC over a corrupt heading: nil error")
	}
	// A document node whose level is corrupt fails the lookup itself, by
	// exact path and by suffix, with a lookup error rather than "not found".
	if _, err := s.WriterDB().ExecContext(ctx, `UPDATE doc_nodes SET level = 'x' WHERE id = 'doc1'`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"docs/guide.md", "guide.md"} {
		if _, err := g.DocumentTOC(path); err == nil || errors.Is(err, ErrDocumentNotFound) {
			t.Errorf("DocumentTOC(%s) over a corrupt document row: error = %v, want a lookup error", path, err)
		}
	}
}
