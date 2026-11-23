package indexing

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// isolateGraphBuildEnv points HOME and the gleann directories at temp dirs:
// a standalone graph build reads the source documents, which loads the
// plugin registry from the home directory (gleann.LoadPlugins).
func isolateGraphBuildEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("GLEANN_CONFIG_DIR", filepath.Join(home, ".gleann"))
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(home, ".gleann", "memory"))
}

// docNodeCount counts the document-graph nodes of one type in the index
// database of env's "g" index.
func docNodeCount(t *testing.T, env *testEnv, typ string) int {
	t.Helper()
	return countRows(t, openGraphDB(t, env), `SELECT count(*) FROM doc_nodes WHERE type = ?`, typ)
}

// A standalone full graph build (CLI `gleann graph build`, REST
// POST /api/graph/{name}/index) passes no PluginDocs. It used to replace the
// document graph with that empty list, wiping every document node; it must
// read the source documents the way migrate does and rebuild them instead.
func TestBuildGraphIndexRebuildsDocumentGraph(t *testing.T) {
	isolateGraphBuildEnv(t)
	env := newEnv(t)
	docs := fixture(t) // main.go, README.md, notes.txt (both extracted as documents), ...
	buildFormat2(t, env, docs)
	if n := docNodeCount(t, env, "document"); n != 2 {
		t.Fatalf("precondition: document nodes after index build --graph = %d, want 2 (README.md, notes.txt)", n)
	}
	headingsBefore := docNodeCount(t, env, "heading")
	if headingsBefore == 0 {
		t.Fatal("precondition: no heading nodes after index build --graph")
	}

	// The standalone build the CLI and REST run: no PluginDocs, no ChangedFiles.
	if err := BuildGraphIndexErr(GraphOptions{Name: "g", DocsDir: docs, IndexDir: env.cfg.IndexDir}); err != nil {
		t.Fatalf("BuildGraphIndexErr: %v", err)
	}
	if n := docNodeCount(t, env, "document"); n != 2 {
		t.Errorf("document nodes after graph build = %d, want 2 (the document graph was wiped)", n)
	}
	if n := docNodeCount(t, env, "heading"); n != headingsBefore {
		t.Errorf("heading nodes after graph build = %d, want %d", n, headingsBefore)
	}
	// The code graph was rebuilt too.
	if n := countRows(t, openGraphDB(t, env), `SELECT count(*) FROM symbols WHERE name = 'Add'`); n != 1 {
		t.Errorf("Add declared %d times after graph build, want 1", n)
	}
}

// When the source tree yields no documents (a code-only tree, or the
// documents were moved away), a standalone full graph build leaves the
// document graph untouched instead of deleting it.
func TestBuildGraphIndexNoDocumentsKeepsDocNodes(t *testing.T) {
	isolateGraphBuildEnv(t)
	env := newEnv(t)
	docs := fixture(t)
	buildFormat2(t, env, docs)
	before := docNodeCount(t, env, "document")
	if before == 0 {
		t.Fatal("precondition: no document nodes after index build --graph")
	}
	// The documents leave the source tree; code, the minified bundle and
	// the binary blob remain, and none produces a document-graph node.
	for _, name := range []string{"README.md", "notes.txt"} {
		if err := os.Remove(filepath.Join(docs, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := BuildGraphIndexErr(GraphOptions{Name: "g", DocsDir: docs, IndexDir: env.cfg.IndexDir}); err != nil {
		t.Fatalf("BuildGraphIndexErr: %v", err)
	}
	if n := docNodeCount(t, env, "document"); n != before {
		t.Errorf("document nodes after a graph build that read no documents = %d, want %d (untouched)", n, before)
	}
}

// The standalone graph build takes the same <name>.lock as Build and Sync
// (AGENTS.md section 2.4): while another process holds it, the build fails
// fast with the readable lock message and writes nothing.
func TestBuildGraphIndexFailsFastWhenLocked(t *testing.T) {
	isolateGraphBuildEnv(t)
	env := newEnv(t)
	docs := fixture(t)
	buildFormat2(t, env, docs)
	idxDir := filepath.Join(env.cfg.IndexDir, "g")
	before := snapshotDir(t, idxDir)

	h := startLockHolder(t, filepath.Join(env.cfg.IndexDir, "g.lock"))

	start := time.Now()
	err := BuildGraphIndexErr(GraphOptions{Name: "g", DocsDir: docs, IndexDir: env.cfg.IndexDir})
	if err == nil {
		t.Fatal("graph build succeeded while another process holds the index lock")
	}
	if !errors.Is(err, ErrIndexLocked) || !strings.Contains(err.Error(), "held by another process") || !strings.Contains(err.Error(), "g") {
		t.Fatalf("graph build lock error = %v; want ErrIndexLocked naming the index", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("graph build waited %s; it must fail fast", elapsed)
	}
	if after := snapshotDir(t, idxDir); !reflect.DeepEqual(before, after) {
		t.Errorf("locked-out graph build modified the index:\nbefore=%v\nafter=%v", before, after)
	}

	// Killing the holder releases the OS lock; the graph build now proceeds.
	h.stop()
	if err := BuildGraphIndexErr(GraphOptions{Name: "g", DocsDir: docs, IndexDir: env.cfg.IndexDir}); err != nil {
		t.Fatalf("graph build after holder died: %v", err)
	}
}

// Guard (passes before and after the fix): an incremental standalone graph
// update applies only the changed files' deltas and does not touch the
// document graph when no documents were re-extracted.
func TestBuildGraphIndexIncrementalKeepsDocumentGraph(t *testing.T) {
	isolateGraphBuildEnv(t)
	env := newEnv(t)
	docs := fixture(t)
	buildFormat2(t, env, docs)
	before := docNodeCount(t, env, "document")
	if before == 0 {
		t.Fatal("precondition: no document nodes after index build --graph")
	}
	mainGo := filepath.Join(docs, "main.go")
	writeFile(t, mainGo, "package main\n\n// Add adds two numbers.\nfunc Add(a, b int) int { return a + b + 1 }\n\nfunc main() { println(Add(1, 2)) }\n")
	if err := BuildGraphIndexErr(GraphOptions{Name: "g", DocsDir: docs, IndexDir: env.cfg.IndexDir, ChangedFiles: []string{mainGo}}); err != nil {
		t.Fatalf("BuildGraphIndexErr: %v", err)
	}
	if n := docNodeCount(t, env, "document"); n != before {
		t.Errorf("document nodes after an incremental graph update = %d, want %d (untouched)", n, before)
	}
	if n := countRows(t, openGraphDB(t, env), `SELECT count(*) FROM symbols WHERE name = 'Add'`); n != 1 {
		t.Errorf("Add declared %d times after the incremental update, want 1", n)
	}
}
