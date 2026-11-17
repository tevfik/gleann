package indexing

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

// goFixture is a two-file Go module with a call edge across files:
// main.main CALLS example.com/fx.Helper.
func goFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/fx\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "a.go"), "package fx\n\n// Helper does the work.\nfunc Helper(x int) int { return x + 1 }\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package fx\n\nfunc main() { println(Helper(1)) }\n")
	return dir
}

// buildFormat2 builds a format-2 index of docs with the graph enabled.
func buildFormat2(t *testing.T, env *testEnv, docs string) {
	t.Helper()
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs, Graph: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(env.cfg.IndexDir, "g", "g")
	if got := gleann.StoreFormatOf(base); got != 2 {
		t.Fatalf("StoreFormatOf = %d, want 2", got)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.IndexDir, "g_graph")); !os.IsNotExist(err) {
		t.Fatalf("format-2 build must not create a Kùzu directory (stat err = %v)", err)
	}
}

// openGraphDB opens the format-2 index database for direct row assertions.
func openGraphDB(t *testing.T, env *testEnv) *sql.DB {
	t.Helper()
	st, err := store.Open(filepath.Join(env.cfg.IndexDir, "g", "g.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st.ReaderDB()
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

// assertNoOrphans checks the referential-integrity invariants of AGENTS.md
// section 2.3: every internal edge target and every edge source has a
// declaration row.
func assertNoOrphans(t *testing.T, db *sql.DB) {
	t.Helper()
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE dst_external = 0 AND dst NOT IN (SELECT fqn FROM symbols)`); n != 0 {
		t.Errorf("%d orphan internal edge target(s)", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE src NOT IN (SELECT fqn FROM symbols)`); n != 0 {
		t.Errorf("%d edge(s) with undeclared source", n)
	}
}

func TestFormat2GraphBuildSymbolsEdgesAnchorsAndCallers(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols`); n < 2 {
		t.Fatalf("symbols rows = %d, want >= 2 (Helper, main)", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src = 'example.com/fx.main' AND dst = 'example.com/fx.Helper' AND dst_external = 0`); n != 1 {
		t.Fatalf("CALLS edge main -> Helper = %d, want 1", n)
	}
	// Concrete declaration anchors (AGENTS.md section 2.2).
	var startLine, endLine, startCol, byteOffset int64
	err := db.QueryRow(`SELECT start_line, end_line, start_col, byte_offset FROM symbols WHERE fqn = 'example.com/fx.Helper'`).
		Scan(&startLine, &endLine, &startCol, &byteOffset)
	if err != nil {
		t.Fatalf("reading Helper anchor: %v", err)
	}
	if startLine != 4 || endLine < startLine || startCol <= 0 || byteOffset <= 0 {
		t.Fatalf("Helper anchor = (%d,%d,%d,%d), want line 4 with valid end/col/offset", startLine, endLine, startCol, byteOffset)
	}
	assertNoOrphans(t, db)

	// The searcher's GraphDB answers callers from the SQLite tables in this
	// (possibly lite) build.
	s := gleann.NewSearcher(env.cfg, nil)
	if err := s.Load(context.Background(), "g"); err != nil {
		t.Fatalf("searcher Load: %v", err)
	}
	defer s.Close()
	gdb := s.GraphDB()
	if gdb == nil {
		t.Fatal("format-2 index with graph data must expose GraphDB in every build")
	}
	callers, err := gdb.Callers("example.com/fx.Helper")
	if err != nil {
		t.Fatalf("GraphDB.Callers: %v", err)
	}
	found := false
	for _, c := range callers {
		if c.FQN == "example.com/fx.main" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Callers(Helper) = %+v, want example.com/fx.main", callers)
	}
}

func TestFormat2GraphRenameLeavesNoOrphanEdge(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	// Rename the declaration in one file only; the call site in the other
	// file now references a name nobody declares.
	writeFile(t, filepath.Join(docs, "a.go"), "package fx\n\nfunc Renamed(x int) int { return x + 1 }\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/fx.Helper'`); n != 0 {
		t.Fatalf("old symbol row survived rename (%d)", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/fx.Renamed'`); n != 1 {
		t.Fatalf("renamed symbol row = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE dst = 'example.com/fx.Helper' AND dst_external = 0`); n != 0 {
		t.Fatalf("%d orphan internal edge(s) to the renamed symbol", n)
	}
	assertNoOrphans(t, db)
}

func TestFormat2GraphDeleteFileRemovesItsRows(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	if err := os.Remove(filepath.Join(docs, "main.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE file = 'main.go'`); n != 0 {
		t.Fatalf("%d symbol row(s) of the deleted file survived", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE file = 'main.go'`); n != 0 {
		t.Fatalf("%d edge(s) of the deleted file survived", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/fx.Helper'`); n != 1 {
		t.Fatalf("unchanged file's symbol lost (%d)", n)
	}
	assertNoOrphans(t, db)
}

func TestFormat2GraphEdgeIntoUnchangedFileSurvives(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	// Change a.go without touching Helper or main.go: the inbound edge
	// main -> Helper must be restored after a.go's delta.
	writeFile(t, filepath.Join(docs, "a.go"), "package fx\n\n// Helper does the work.\nfunc Helper(x int) int { return x + 1 }\n\nfunc Extra() {}\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src = 'example.com/fx.main' AND dst = 'example.com/fx.Helper' AND dst_external = 0`); n != 1 {
		t.Fatalf("edge into unchanged file lost (%d), want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = 'example.com/fx.Extra'`); n != 1 {
		t.Fatalf("new symbol missing (%d)", n)
	}
	assertNoOrphans(t, db)
}

// A failed graph write must not advance the file's graph hash (AGENTS.md
// section 2.3): while another process holds the SQLite write lock the delta
// cannot commit, the build reports an error, graph_hash stays at its old
// value, and the next sync converges the file.
func TestFormat2GraphWriteFailureDoesNotAdvanceHash(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	dbPath := filepath.Join(env.cfg.IndexDir, "g", "g.db")
	hashOf := func(file string) string {
		st, err := store.Open(dbPath, store.Options{})
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		defer st.Close()
		var h *string
		if err := st.ReaderDB().QueryRow(`SELECT graph_hash FROM files WHERE path = ?`, file).Scan(&h); err != nil {
			t.Fatalf("reading graph_hash of %s: %v", file, err)
		}
		if h == nil {
			return ""
		}
		return *h
	}
	before := hashOf("a.go")
	if before == "" {
		t.Fatal("precondition: a.go must have a graph_hash after the build")
	}

	writeFile(t, filepath.Join(docs, "a.go"), "package fx\n\nfunc Helper(x int) int { return x + 2 }\n")

	// Hold the single writer's lock like a concurrent `gleann serve` would:
	// the graph delta's BEGIN IMMEDIATE cannot acquire it and must fail
	// after the busy timeout instead of advancing the hash.
	blocker, err := store.Open(dbPath, store.Options{})
	if err != nil {
		t.Fatalf("store.Open blocker: %v", err)
	}
	tx, err := blocker.BeginWrite(context.Background())
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}

	gopts := GraphOptions{
		Name:         "g",
		DocsDir:      docs,
		IndexDir:     env.cfg.IndexDir,
		ChangedFiles: []string{filepath.Join(docs, "a.go")},
	}
	err = buildGraphIndex(gopts, newPrinter(nil, nil))
	if err == nil {
		t.Fatal("graph build succeeded while the write lock was held")
	}
	if after := hashOf("a.go"); after != before {
		t.Fatalf("graph_hash advanced despite the failed graph write: %q -> %q", before, after)
	}

	// Release the lock; the next sync must pick the change up, not skip it.
	tx.Rollback()
	blocker.Close()

	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("converging Sync: %v", err)
	}
	if after := hashOf("a.go"); after == before || after == "" {
		t.Fatalf("graph_hash did not advance after convergence: %q -> %q", before, after)
	}
	db := openGraphDB(t, env)
	assertNoOrphans(t, db)
}

// Two files added in one sync, the first calling into the second: the call
// is internal (not stored as an external edge because its target had not
// been applied yet) and the dirty pass lands it in the same sync.
func TestFormat2GraphSyncBatchCallIntoNewFileIsInternal(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)
	writeFile(t, filepath.Join(docs, "b.go"), "package fx\n\nfunc Caller() int { return Zed() }\n")
	writeFile(t, filepath.Join(docs, "z.go"), "package fx\n\nfunc Zed() int { return 7 }\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src = 'example.com/fx.Caller' AND dst = 'example.com/fx.Zed' AND dst_external = 0`); n != 1 {
		t.Fatalf("internal CALLS Caller -> Zed = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM files WHERE graph_dirty = 1`); n != 0 {
		t.Errorf("%d file(s) left graph_dirty after the sync", n)
	}
	assertNoOrphans(t, db)
}

// A changed path given through a symlink to the (resolved) docs root still
// maps to the stored relative path, so a deletion retracts the file's rows.
func TestRelToRootResolvesSymlinkedPaths(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(real, "pkg", "a.go"), "package pkg\n")
	if got := relToRoot(root, filepath.Join(link, "pkg", "a.go")); got != "pkg/a.go" {
		t.Errorf("existing file via symlink: %q, want pkg/a.go", got)
	}
	if got := relToRoot(root, filepath.Join(link, "pkg", "gone.go")); got != "pkg/gone.go" {
		t.Errorf("deleted file via symlink: %q, want pkg/gone.go", got)
	}
}
