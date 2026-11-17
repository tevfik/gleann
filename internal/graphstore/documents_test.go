package graphstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// insertDocNodes writes hierarchy rows directly; populating doc_nodes is the
// document indexer's job (the second half of RFC 003 step 7), so the tests
// seed the table through the shared writer handle.
func insertDocNodes(t *testing.T, g *SQLite, nodes []DocNode) {
	t.Helper()
	for _, n := range nodes {
		var parent, name, rpath, hash, summary, text any
		if n.Parent != "" {
			parent = n.Parent
		}
		if n.Name != "" {
			name = n.Name
		}
		if n.RPath != "" {
			rpath = n.RPath
		}
		if n.Hash != "" {
			hash = n.Hash
		}
		if n.Summary != "" {
			summary = n.Summary
		}
		if n.Text != "" {
			text = n.Text
		}
		if _, err := g.writer.ExecContext(context.Background(), `INSERT INTO doc_nodes
(id, type, parent, name, level, rpath, hash, summary, text, start_char, end_char)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.ID, n.Type, parent, name, n.Level, rpath, hash, summary, text, n.StartChar, n.EndChar); err != nil {
			t.Fatalf("inserting doc node %s: %v", n.ID, err)
		}
	}
}

// seedDocs builds folder > document > headings (one nested) > chunks, plus
// one symbol explained by a chunk.
func seedDocs(t *testing.T, g *SQLite, rpath string) {
	t.Helper()
	insertDocNodes(t, g, []DocNode{
		{ID: "fold1", Type: "folder", Name: "docs"},
		{ID: "doc1", Type: "document", Parent: "fold1", Name: "Guide", RPath: rpath, Summary: "how it works"},
		{ID: "h1", Type: "heading", Parent: "doc1", Name: "Intro", Level: 1},
		{ID: "h2", Type: "heading", Parent: "h1", Name: "Background", Level: 2},
		{ID: "h3", Type: "heading", Parent: "doc1", Name: "Usage", Level: 1},
		{ID: "c1", Type: "chunk", Parent: "doc1", Text: "part one", StartChar: 0, EndChar: 8},
		{ID: "c2", Type: "chunk", Parent: "doc1", Text: "part two", StartChar: 9, EndChar: 17},
		{ID: "doc2", Type: "document", Parent: "fold1", Name: "Other", RPath: "docs/other.md"},
	})
	mustApply(t, g, FileDelta{File: "a.go", Symbols: []Symbol{mkSym("a.Explained", "a.go", 1)}})
	if _, err := g.writer.ExecContext(context.Background(),
		`INSERT INTO doc_explains (chunk_id, fqn) VALUES ('c1', 'a.Explained')`); err != nil {
		t.Fatalf("inserting doc_explains: %v", err)
	}
}

func TestDocumentContext(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	ctx, err := g.DocumentContext("docs/guide.md")
	if err != nil {
		t.Fatalf("DocumentContext: %v", err)
	}
	if ctx.Name != "Guide" || ctx.Summary != "how it works" || ctx.FolderName != "docs" {
		t.Fatalf("DocumentContext metadata = %+v", ctx)
	}
	if !reflect.DeepEqual(ctx.Headings, []string{"Intro", "Usage", "Background"}) {
		t.Fatalf("headings = %v, want level order Intro, Usage, Background", ctx.Headings)
	}
	if ctx.Breadcrumb != "docs > Guide > Intro > Usage > Background" {
		t.Fatalf("breadcrumb = %q", ctx.Breadcrumb)
	}
	if _, err := g.DocumentContext("missing.md"); err == nil {
		t.Fatal("DocumentContext(missing) succeeded, want error")
	}
}

func TestDocumentTOC(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	toc, err := g.DocumentTOC("docs/guide.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	if toc.Title != "Guide" || toc.Folder != "docs" || toc.TotalNodes != 3 {
		t.Fatalf("DocumentTOC metadata = %+v", toc)
	}
	want := []Heading{
		{ID: "h1", Title: "Intro", Level: 1, Children: []Heading{
			{ID: "h2", Title: "Background", Level: 2},
		}},
		{ID: "h3", Title: "Usage", Level: 1},
	}
	if !reflect.DeepEqual(toc.Headings, want) {
		t.Fatalf("TOC tree = %+v, want %+v", toc.Headings, want)
	}
	// Lookup by node ID works too (the schema has no vpath).
	if _, err := g.DocumentTOC("doc1"); err != nil {
		t.Fatalf("DocumentTOC(doc1): %v", err)
	}
}

func TestListDocuments(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	docs, err := g.ListDocuments()
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	want := []DocumentInfo{
		{RPath: "docs/guide.md", Title: "Guide", Summary: "how it works", Folder: "docs", HeadingCount: 3},
		{RPath: "docs/other.md", Title: "Other", Folder: "docs", HeadingCount: 0},
	}
	if !reflect.DeepEqual(docs, want) {
		t.Fatalf("ListDocuments = %+v, want %+v", docs, want)
	}
}

func TestFullDocument(t *testing.T) {
	g, _ := openTestGraph(t)
	dir := t.TempDir()
	rpath := filepath.Join(dir, "guide.md")
	seedDocs(t, g, rpath)

	// No physical file yet: reconstruct from chunks in start_char order.
	got, err := g.FullDocument(rpath)
	if err != nil {
		t.Fatalf("FullDocument: %v", err)
	}
	if got != "part one\n\npart two" {
		t.Fatalf("FullDocument = %q, want chunk reconstruction", got)
	}

	// The physical file wins when it exists (kuzu.FullDocument's fast path).
	if err := os.WriteFile(rpath, []byte("on disk"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = g.FullDocument(rpath)
	if err != nil || got != "on disk" {
		t.Fatalf("FullDocument = %q, %v; want the disk content", got, err)
	}

	if _, err := g.FullDocument("missing.md"); err == nil {
		t.Fatal("FullDocument(missing) succeeded, want error")
	}
}

func TestDocumentSymbols(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	syms, err := g.DocumentSymbols("docs/guide.md")
	if err != nil {
		t.Fatalf("DocumentSymbols: %v", err)
	}
	if len(syms) != 1 || syms[0].FQN != "a.Explained" {
		t.Fatalf("DocumentSymbols = %+v, want a.Explained", syms)
	}
	// A symbol explained by two chunks is reported once.
	if _, err := g.writer.ExecContext(context.Background(),
		`INSERT INTO doc_explains (chunk_id, fqn) VALUES ('c2', 'a.Explained')`); err != nil {
		t.Fatal(err)
	}
	syms, err = g.DocumentSymbols("docs/guide.md")
	if err != nil || len(syms) != 1 {
		t.Fatalf("DocumentSymbols after second explains = %+v, %v; want one row", syms, err)
	}
}

// TestDocumentLookupUsesRPathIndex: document lookups must be index-driven,
// not table scans (RFC 003 section 3.4; the indexes arrive through the
// doc_nodes_rpath migration step). The physical-path probe uses
// doc_nodes_rpath; findDocument's combined rpath-or-id lookup is answered
// from an index too (with both indexes present the planner serves the type
// filter from doc_nodes_type).
func TestDocumentLookupUsesRPathIndex(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	plan := func(query string, args ...any) string {
		t.Helper()
		rows, err := g.reader.Query(`EXPLAIN QUERY PLAN `+query, args...)
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatalf("scanning plan: %v", err)
			}
			fmt.Fprintf(&b, "%s\n", detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("plan rows: %v", err)
		}
		return b.String()
	}
	rpathPlan := plan(`SELECT id FROM doc_nodes WHERE rpath = ?`, "docs/guide.md")
	if !strings.Contains(rpathPlan, "doc_nodes_rpath") {
		t.Fatalf("rpath lookup does not use doc_nodes_rpath:\n%s", rpathPlan)
	}
	// The exact query findDocument runs.
	docPlan := plan(`SELECT `+docNodeCols+` FROM doc_nodes
WHERE type = 'document' AND (rpath = ? OR id = ?) LIMIT 1`, "docs/guide.md", "docs/guide.md")
	if strings.Contains(docPlan, "SCAN doc_nodes") || !strings.Contains(docPlan, "USING INDEX") {
		t.Fatalf("document lookup is not index-driven:\n%s", docPlan)
	}
}

// TestDocTreeDepthCap: the recursive heading walk carries an explicit depth
// cap (RFC 003 section 3.7). A chain deeper than the cap is truncated at it
// instead of walking unbounded; the seed sits at depth 0 like callersSQL, so
// a cap of 64 keeps depths 0..64.
func TestDocTreeDepthCap(t *testing.T) {
	g, _ := openTestGraph(t)
	const chain = 100
	nodes := []DocNode{{ID: "doc1", Type: "document", Name: "Deep", RPath: "deep.md"}}
	for i := 0; i < chain; i++ {
		parent := "doc1"
		if i > 0 {
			parent = fmt.Sprintf("h%d", i-1)
		}
		nodes = append(nodes, DocNode{
			ID: fmt.Sprintf("h%d", i), Type: "heading", Parent: parent,
			Name: fmt.Sprintf("H%d", i), Level: i + 1,
		})
	}
	insertDocNodes(t, g, nodes)
	const want = 65 // depth cap 64, seed at depth 0
	toc, err := g.DocumentTOC("deep.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	if toc.TotalNodes != want {
		t.Fatalf("TotalNodes = %d, want %d (the depth cap must bound the walk)", toc.TotalNodes, want)
	}
	ctx, err := g.DocumentContext("deep.md")
	if err != nil {
		t.Fatalf("DocumentContext: %v", err)
	}
	if len(ctx.Headings) != want {
		t.Fatalf("headings = %d, want %d", len(ctx.Headings), want)
	}
}

// TestDocTreeParentCycleTerminates: a parent cycle among headings must not
// hang the walk (UNION dedup plus the depth cap, RFC 003 section 3.7). The
// cycle is a closed component of the parent graph, so it is unreachable from
// the document and none of its nodes may leak into the TOC.
func TestDocTreeParentCycleTerminates(t *testing.T) {
	g, _ := openTestGraph(t)
	insertDocNodes(t, g, []DocNode{
		{ID: "doc1", Type: "document", Name: "Cyclic", RPath: "cyclic.md"},
		{ID: "h1", Type: "heading", Parent: "doc1", Name: "Intro", Level: 1},
		{ID: "h2", Type: "heading", Parent: "h1", Name: "Details", Level: 2},
		{ID: "c1", Type: "heading", Parent: "c2", Name: "C1", Level: 1},
		{ID: "c2", Type: "heading", Parent: "c3", Name: "C2", Level: 1},
		{ID: "c3", Type: "heading", Parent: "c1", Name: "C3", Level: 1},
	})
	toc, err := g.DocumentTOC("cyclic.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	if toc.TotalNodes != 2 {
		t.Fatalf("TotalNodes = %d, want 2 (cycle nodes must not appear)", toc.TotalNodes)
	}
	if _, err := g.DocumentContext("cyclic.md"); err != nil {
		t.Fatalf("DocumentContext: %v", err)
	}
}

// TestDocumentLookupNormalisesBackslashPaths: every document lookup accepts
// a Windows-style path and finds the document stored under its slash form,
// on every OS (the argument is built with explicit backslashes).
func TestDocumentLookupNormalisesBackslashPaths(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	win := `docs\guide.md`
	toc, err := g.DocumentTOC(win)
	if err != nil || toc.Title != "Guide" {
		t.Fatalf("DocumentTOC(%q) = %+v, %v; want Guide", win, toc, err)
	}
	ctx, err := g.DocumentContext(win)
	if err != nil || ctx.Name != "Guide" {
		t.Fatalf("DocumentContext(%q) = %+v, %v; want Guide", win, ctx, err)
	}
	syms, err := g.DocumentSymbols(win)
	if err != nil || len(syms) != 1 || syms[0].FQN != "a.Explained" {
		t.Fatalf("DocumentSymbols(%q) = %+v, %v; want a.Explained", win, syms, err)
	}
	// No physical file on disk: the chunk reconstruction must still work.
	got, err := g.FullDocument(win)
	if err != nil || got != "part one\n\npart two" {
		t.Fatalf("FullDocument(%q) = %q, %v; want chunk reconstruction", win, got, err)
	}
}

// TestListDocumentsCountsNestedHeadings: a document's heading count must equal
// DocumentTOC's TotalNodes (both count every heading, not just the direct
// ones) — the regression for the Kùzu count(h) mismatch. seedDocs nests one
// heading (Background) under another (Intro), so the count is 3.
func TestListDocumentsCountsNestedHeadings(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")
	toc, err := g.DocumentTOC("docs/guide.md")
	if err != nil {
		t.Fatalf("DocumentTOC: %v", err)
	}
	docs, err := g.ListDocuments()
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	var headingCount int
	for _, d := range docs {
		if d.RPath == "docs/guide.md" {
			headingCount = d.HeadingCount
		}
	}
	if headingCount != toc.TotalNodes || headingCount != 3 {
		t.Fatalf("ListDocuments heading count = %d, DocumentTOC TotalNodes = %d; want both 3",
			headingCount, toc.TotalNodes)
	}
}

// TestDocumentLookupBySuffix: a bare filename resolves a document stored under
// a longer physical path, as kuzu's "vpath/rpath ENDS WITH" did.
func TestDocumentLookupBySuffix(t *testing.T) {
	g, _ := openTestGraph(t)
	seedDocs(t, g, "docs/guide.md")

	ctx, err := g.DocumentContext("guide.md")
	if err != nil || ctx.Name != "Guide" {
		t.Fatalf("DocumentContext(guide.md) = %+v, %v; want Guide", ctx, err)
	}
	toc, err := g.DocumentTOC("guide.md")
	if err != nil || toc.Title != "Guide" {
		t.Fatalf("DocumentTOC(guide.md) = %+v, %v; want Guide", toc, err)
	}
	syms, err := g.DocumentSymbols("guide.md")
	if err != nil || len(syms) != 1 || syms[0].FQN != "a.Explained" {
		t.Fatalf("DocumentSymbols(guide.md) = %+v, %v; want a.Explained", syms, err)
	}
	// No physical file on disk: the chunk reconstruction must still work.
	full, err := g.FullDocument("guide.md")
	if err != nil || full != "part one\n\npart two" {
		t.Fatalf("FullDocument(guide.md) = %q, %v; want chunk reconstruction", full, err)
	}
}
