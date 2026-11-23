package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// graphDocsIndex builds a lexical-only format-2 index with a code graph and
// one markdown document, the way `index build --graph` does, and returns the
// source directory.
func graphDocsIndex(t *testing.T, cfg gleann.Config) string {
	t.Helper()
	docs := t.TempDir()
	for rel, content := range map[string]string{
		"main.go":   "package main\n\nfunc main() {}\n",
		"README.md": "# Project\n\nIntro text about the project.\n\n## Usage\n\nRun the binary.\n",
	} {
		p := filepath.Join(docs, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := indexing.Build(context.Background(), indexing.BuildOptions{
		Options:     indexing.Options{Config: cfg, NoPlugins: true, NoTracker: true, Stdout: io.Discard, Stderr: io.Discard},
		Name:        "docsidx",
		DocsDir:     docs,
		Graph:       true,
		InstantOnly: true,
	}); err != nil {
		t.Fatalf("index build: %v", err)
	}
	return docs
}

// graphDocCount lists the documents of the index's document graph.
func graphDocCount(t *testing.T, indexDir, name string) int {
	t.Helper()
	g, err := gleann.OpenGraphStore(indexDir, name)
	if err != nil {
		t.Fatalf("OpenGraphStore: %v", err)
	}
	if g == nil {
		t.Fatal("index has no graph")
	}
	defer g.Close()
	docs, err := g.ListDocuments()
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	return len(docs)
}

func postGraphIndex(t *testing.T, h http.Handler, docs string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/graph/docsidx/index", bytes.NewBufferString(`{"docs_dir":`+strconv.Quote(docs)+`}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// POST /api/graph/{name}/index runs the same standalone full graph build as
// `gleann graph build`: it must rebuild the document graph from the source
// documents, not wipe it (it used to replace doc_nodes with an empty list).
func TestHandleGraphIndexKeepsDocumentGraph(t *testing.T) {
	_, h, cfg := newBuildServer(t)
	docs := graphDocsIndex(t, cfg)
	t.Setenv("GLEANN_ALLOWED_ROOTS", docs)
	if n := graphDocCount(t, cfg.IndexDir, "docsidx"); n != 1 {
		t.Fatalf("precondition: documents after index build --graph = %d, want 1", n)
	}

	if w := postGraphIndex(t, h, docs); w.Code != http.StatusOK {
		t.Fatalf("POST /api/graph/docsidx/index = %d: %s", w.Code, w.Body.String())
	}
	if n := graphDocCount(t, cfg.IndexDir, "docsidx"); n != 1 {
		t.Errorf("documents after the graph index endpoint = %d, want 1 (the document graph was wiped)", n)
	}
}

// The graph index endpoint takes the index lock: while another process holds
// <name>.lock it answers 500 with the readable lock message and writes
// nothing; once the lock is free the rebuild proceeds.
func TestHandleGraphIndexFailsFastWhenLocked(t *testing.T) {
	_, h, cfg := newBuildServer(t)
	docs := graphDocsIndex(t, cfg)
	t.Setenv("GLEANN_ALLOWED_ROOTS", docs)

	l, err := store.AcquireLock(filepath.Join(cfg.IndexDir, "docsidx.lock"))
	if err != nil {
		t.Fatalf("acquiring the index lock: %v", err)
	}
	w := postGraphIndex(t, h, docs)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("locked POST /api/graph/docsidx/index = %d: %s, want 500", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "held by another process") {
		t.Errorf("the answer must carry the lock message: %s", w.Body.String())
	}
	if n := graphDocCount(t, cfg.IndexDir, "docsidx"); n != 1 {
		t.Errorf("locked-out graph build changed the document graph (%d documents, want 1)", n)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}

	if w := postGraphIndex(t, h, docs); w.Code != http.StatusOK {
		t.Fatalf("POST after the lock was released = %d: %s", w.Code, w.Body.String())
	}
	if n := graphDocCount(t, cfg.IndexDir, "docsidx"); n != 1 {
		t.Errorf("documents after the rebuild = %d, want 1", n)
	}
}
