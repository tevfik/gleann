package graphstore

import (
	"context"
	"testing"
)

// docDelta builds a small document hierarchy: folder docs/ → document
// docs/guide.md → heading "Intro" → chunk "c1" explaining symbol "p.F".
func docDelta(rpath string) DocDelta {
	return DocDelta{
		RPath: rpath,
		Nodes: []DocNode{
			{ID: "folder:docs", Type: "folder", Name: "docs"},
			{ID: "doc:" + rpath, Type: "document", Parent: "folder:docs", Name: "Guide", RPath: rpath, Hash: "h1", Summary: "the guide"},
			{ID: "doc:" + rpath + ":intro", Type: "heading", Parent: "doc:" + rpath, Name: "Intro", Level: 1, RPath: rpath},
			{ID: "c1:" + rpath, Type: "chunk", Parent: "doc:" + rpath, RPath: rpath, Text: "about p.F", StartChar: 0, EndChar: 9},
		},
		Explains: []DocExplains{{ChunkID: "c1:" + rpath, FQN: "p.F"}},
	}
}

func docNodeCount(t *testing.T, g *SQLite) int {
	t.Helper()
	var n int
	if err := g.reader.QueryRow(`SELECT count(*) FROM doc_nodes`).Scan(&n); err != nil {
		t.Fatalf("counting doc_nodes: %v", err)
	}
	return n
}

func explainsCount(t *testing.T, g *SQLite) int {
	t.Helper()
	var n int
	if err := g.reader.QueryRow(`SELECT count(*) FROM doc_explains`).Scan(&n); err != nil {
		t.Fatalf("counting doc_explains: %v", err)
	}
	return n
}

func TestReplaceDocsAndReadBack(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	mustApply(t, g, FileDelta{File: "p.go", GraphHash: "h", Symbols: []Symbol{mkSym("p.F", "p.go", 3)}})

	if err := g.ReplaceDocs(ctx, []DocDelta{docDelta("docs/guide.md")}); err != nil {
		t.Fatalf("ReplaceDocs: %v", err)
	}
	if got := docNodeCount(t, g); got != 4 {
		t.Fatalf("doc_nodes = %d, want 4", got)
	}
	toc, err := g.DocumentTOC("docs/guide.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	if toc.Title != "Guide" || toc.Folder != "docs" || len(toc.Headings) != 1 || toc.Headings[0].Title != "Intro" {
		t.Fatalf("unexpected TOC: %+v", toc)
	}
	syms, err := g.DocumentSymbols("docs/guide.md")
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	if len(syms) != 1 || syms[0].FQN != "p.F" {
		t.Fatalf("DocumentSymbols = %+v, want [p.F]", syms)
	}

	// A full rebuild replaces, not appends.
	if err := g.ReplaceDocs(ctx, []DocDelta{docDelta("docs/guide.md")}); err != nil {
		t.Fatalf("ReplaceDocs again: %v", err)
	}
	if got := docNodeCount(t, g); got != 4 {
		t.Fatalf("doc_nodes after rebuild = %d, want 4", got)
	}
	if got := explainsCount(t, g); got != 1 {
		t.Fatalf("doc_explains after rebuild = %d, want 1", got)
	}
}

func TestApplyDocDeltaRetractsOldRows(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	if err := g.ApplyDocDelta(ctx, docDelta("docs/guide.md")); err != nil {
		t.Fatalf("ApplyDocDelta: %v", err)
	}

	// Rename the heading and drop the chunk: the old heading and its
	// EXPLAINS relation must be gone, the folder must survive.
	updated := docDelta("docs/guide.md")
	updated.Nodes = updated.Nodes[:3] // folder, document, heading
	updated.Nodes[2].ID = "doc:docs/guide.md:usage"
	updated.Nodes[2].Name = "Usage"
	updated.Explains = nil
	if err := g.ApplyDocDelta(ctx, updated); err != nil {
		t.Fatalf("ApplyDocDelta update: %v", err)
	}
	if got := docNodeCount(t, g); got != 3 {
		t.Fatalf("doc_nodes = %d, want 3 (folder, document, heading)", got)
	}
	if got := explainsCount(t, g); got != 0 {
		t.Fatalf("doc_explains = %d, want 0 (chunk retracted)", got)
	}
	toc, err := g.DocumentTOC("docs/guide.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	if len(toc.Headings) != 1 || toc.Headings[0].Title != "Usage" {
		t.Fatalf("unexpected TOC after update: %+v", toc.Headings)
	}
}

func TestRemoveDocKeepsSharedFolders(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	if err := g.ReplaceDocs(ctx, []DocDelta{docDelta("docs/guide.md"), docDelta("docs/other.md")}); err != nil {
		t.Fatalf("ReplaceDocs: %v", err)
	}
	// Both documents share folder:docs (INSERT OR IGNORE keeps one row).
	if got := docNodeCount(t, g); got != 7 {
		t.Fatalf("doc_nodes = %d, want 7 (1 folder + 2x3)", got)
	}
	if err := g.RemoveDoc(ctx, "docs/guide.md"); err != nil {
		t.Fatalf("RemoveDoc: %v", err)
	}
	if got := docNodeCount(t, g); got != 4 {
		t.Fatalf("doc_nodes after RemoveDoc = %d, want 4 (folder + other doc)", got)
	}
	if got := explainsCount(t, g); got != 1 {
		t.Fatalf("doc_explains after RemoveDoc = %d, want 1 (other doc's)", got)
	}
	if _, err := g.DocumentTOC("docs/guide.md"); err == nil {
		t.Fatal("DocumentTOC of removed document should fail")
	}
	if _, err := g.DocumentTOC("docs/other.md"); err != nil {
		t.Fatalf("DocumentTOC of surviving document: %v", err)
	}
}

func TestDeclaredFQNs(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{File: "a.go", GraphHash: "h", Symbols: []Symbol{mkSym("a.One", "a.go", 1)}})
	mustApply(t, g, FileDelta{File: "b.go", GraphHash: "h", Symbols: []Symbol{mkSym("b.Two", "b.go", 1)}})

	got, err := g.DeclaredFQNs([]string{"a.One", "b.Two", "missing.Three"})
	if err != nil {
		t.Fatalf("DeclaredFQNs: %v", err)
	}
	if len(got) != 2 || got["a.One"] != "a.go" || got["b.Two"] != "b.go" {
		t.Fatalf("DeclaredFQNs = %v, want a.One->a.go, b.Two->b.go", got)
	}
	if _, ok := got["missing.Three"]; ok {
		t.Fatal("undeclared FQN must be absent")
	}
	empty, err := g.DeclaredFQNs(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("DeclaredFQNs(nil) = %v, %v", empty, err)
	}
}

func TestMarkFileDirty(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	mustApply(t, g, FileDelta{File: "a.go", GraphHash: "h", Symbols: []Symbol{mkSym("a.One", "a.go", 1)}})

	if err := g.MarkFileDirty(ctx, "a.go"); err != nil {
		t.Fatalf("MarkFileDirty: %v", err)
	}
	dirty, err := g.DirtyFiles()
	if err != nil {
		t.Fatalf("DirtyFiles: %v", err)
	}
	if len(dirty) != 1 || dirty[0] != "a.go" {
		t.Fatalf("DirtyFiles = %v, want [a.go]", dirty)
	}
	// A file the graph knows only through edges gets a row too.
	if err := g.MarkFileDirty(ctx, "ghost.go"); err != nil {
		t.Fatalf("MarkFileDirty(ghost): %v", err)
	}
	dirty, err = g.DirtyFiles()
	if err != nil {
		t.Fatalf("DirtyFiles: %v", err)
	}
	if len(dirty) != 2 {
		t.Fatalf("DirtyFiles = %v, want a.go and ghost.go", dirty)
	}
}
