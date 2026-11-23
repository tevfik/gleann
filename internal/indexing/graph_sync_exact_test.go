package indexing

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

const syncExactPkg = "example.com/sx"

// Files of syncExactFixture that the tests rewrite.
const (
	sxApp = `package sx

import "example.com/sx/store"

// Getter is implemented by store.Store, a type of a package this one imports.
type Getter interface {
	Get() int
}

// Runner calls the store through a field.
type Runner struct{ st *store.Store }

// Old calls through a receiver.
func (r *Runner) Old() int { return r.st.Get() }

// Use dispatches through the interface.
func Use(g Getter) int { return g.Get() }

// Drain calls a method of an interface this file never names.
func Drain() int { return store.NewSource().Next() }
`
	sxAppEdited = sxApp + `
// New is added by the edit: two calls through a receiver.
func (r *Runner) New() { r.st.Put(r.Old()) }

// Local implements Getter by method set.
type Local struct{}

// Get implements Getter.
func (Local) Get() int { return 3 }
`
	sxOther = `package sx

// Other is in an unchanged sibling file and calls through a receiver.
func Other(r *Runner) int { return r.Old() }
`
	sxStore = `package store

// Base provides the promoted Get.
type Base struct{}

// Get is promoted into Store when Store has no Get of its own.
func (Base) Get() int { return 1 }

// Store is the type the other packages call.
type Store struct{ Base }

// Get shadows Base.Get.
func (*Store) Get() int { return 2 }

// Put stores a value.
func (*Store) Put(int) {}

// Source is returned by NewSource; its callers need not name it.
type Source interface{ Next() int }

type source struct{}

func (source) Next() int { return 0 }

// NewSource returns the interface.
func NewSource() Source { return source{} }

// Spare implements the root package's Getter too and is never called.
type Spare struct{}

// Get implements Getter.
func (Spare) Get() int { return 4 }
`
	sxCLI = `package cli

import "example.com/sx/store"

// Run is in a package that imports store and is never edited.
func Run(s *store.Store) int { return s.Get() }
`
)

// syncExactFixture is a Go module of three packages whose calls go through
// receivers, a field and an interface: nothing the syntactic resolver follows.
func syncExactFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+syncExactPkg+"\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "app.go"), sxApp)
	writeFile(t, filepath.Join(dir, "other.go"), sxOther)
	writeFile(t, filepath.Join(dir, "store", "store.go"), sxStore)
	writeFile(t, filepath.Join(dir, "cli", "cli.go"), sxCLI)
	return dir
}

// edgeConfidence returns the confidence of an internal edge, or "" when the
// graph does not hold it.
func edgeConfidence(t *testing.T, db *sql.DB, kind, src, dst string) string {
	t.Helper()
	var conf string
	err := db.QueryRow(`SELECT confidence FROM edges WHERE kind = ? AND src = ? AND dst = ? AND dst_external = 0`, kind, src, dst).Scan(&conf)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("%s edge %s -> %s: %v", kind, src, dst, err)
	}
	return conf
}

// wantExact fails for every listed edge that is not in the graph as "exact".
func wantExact(t *testing.T, db *sql.DB, kind string, edges [][2]string) {
	t.Helper()
	for _, e := range edges {
		if conf := edgeConfidence(t, db, kind, syncExactPkg+e[0], syncExactPkg+e[1]); conf != "exact" {
			t.Errorf("%s %s -> %s = %q, want exact", kind, e[0], e[1], conf)
		}
	}
}

func syncGraph(t *testing.T, env *testEnv) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v\n%s", err, out.String())
	}
	return out.String()
}

func dirtyCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	return countRows(t, db, `SELECT count(*) FROM files WHERE graph_dirty = 1`)
}

// The exact edges the fixture has after a full build: the baseline of the
// sync tests below.
var sxBuiltCalls = [][2]string{
	{".Runner.Old", "/store.Store.Get"},
	{".Use", ".Getter"},
	{".Drain", "/store.NewSource"},
	{".Drain", "/store.Source"}, // interface method, bound to the interface
	{".Other", ".Runner.Old"},
	{"/cli.Run", "/store.Store.Get"},
}

// A sync of a changed Go file resolves it as exactly as the full build did:
// the calls through a receiver it already had stay, the ones the edit adds
// appear, both labelled "exact", and so do the method-set IMPLEMENTS edges of
// its types. The unchanged sibling the sync re-extracts (a declaration
// appeared in its package) keeps its receiver call too.
func TestSyncResolvesChangedGoFilesExactly(t *testing.T) {
	env := newEnv(t)
	docs := syncExactFixture(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	wantExact(t, db, "CALLS", sxBuiltCalls)
	wantExact(t, db, "IMPLEMENTS", [][2]string{{"/store.Store", ".Getter"}})
	if t.Failed() {
		t.Fatal("the full build is not exact: nothing to compare the sync with")
	}

	writeFile(t, filepath.Join(docs, "app.go"), sxAppEdited)
	out := syncGraph(t, env)
	if strings.Contains(out, "warning") {
		t.Errorf("unexpected warning: %s", out)
	}
	db = openGraphDB(t, env)
	wantExact(t, db, "CALLS", append([][2]string{
		{".Runner.New", "/store.Store.Put"}, // added by the edit, another package
		{".Runner.New", ".Runner.Old"},      // added by the edit, same file
	}, sxBuiltCalls...))
	wantExact(t, db, "IMPLEMENTS", [][2]string{
		{".Local", ".Getter"},       // type added by the edit
		{"/store.Store", ".Getter"}, // unchanged file, untouched
	})
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty", n)
	}
	assertNoOrphans(t, db)

	// The edges are re-derived, not kept: Local stops implementing Getter.
	writeFile(t, filepath.Join(docs, "app.go"), strings.Replace(sxAppEdited, "func (Local) Get() int", "func (Local) Fetch() int", 1))
	syncGraph(t, env)
	db = openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "IMPLEMENTS", syncExactPkg+".Local", syncExactPkg+".Getter"); conf != "" {
		t.Errorf("IMPLEMENTS Local -> Getter = %q after Local lost Get, want none", conf)
	}
	wantExact(t, db, "CALLS", [][2]string{{".Runner.New", "/store.Store.Put"}, {".Runner.Old", "/store.Store.Get"}})
	assertNoOrphans(t, db)
}

// The sync graph equals the graph a full build of the edited tree produces:
// edges AND symbols with their source anchors (file, lines, column, offset),
// across the scenarios the reviews found divergent (Q7, G6): added
// declarations, a rename within a package, a move to another package, a
// deleted methods-only file, a removed method whose interface lives in an
// importing package, type-error convergence, and a go.work workspace.
func TestSyncGraphEqualsFullBuild(t *testing.T) {
	// equal builds the fixture, applies the edit, syncs, then compares with a
	// full build of the same edited tree.
	equal := func(t *testing.T, docs string, edit func()) {
		t.Helper()
		synced := newEnv(t)
		buildFormat2(t, synced, docs)
		edit()
		syncGraph(t, synced)
		full := newEnv(t)
		buildFormat2(t, full, docs)
		assertGraphEquals(t, t.Name(), openGraphDB(t, synced), openGraphDB(t, full))
		assertNoOrphans(t, openGraphDB(t, synced))
	}

	t.Run("added declarations", func(t *testing.T) {
		docs := syncExactFixture(t)
		equal(t, docs, func() { writeFile(t, filepath.Join(docs, "app.go"), sxAppEdited) })
	})

	t.Run("rename within package", func(t *testing.T) {
		docs := t.TempDir()
		writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/eqrn\n\ngo 1.21\n")
		writeFile(t, filepath.Join(docs, "a.go"), "package eqrn\n\n// Helper is called by Use.\nfunc Helper() int { return 1 }\n")
		writeFile(t, filepath.Join(docs, "b.go"), "package eqrn\n\n// Use calls Helper.\nfunc Use() int { return Helper() }\n")
		equal(t, docs, func() {
			if err := os.Rename(filepath.Join(docs, "a.go"), filepath.Join(docs, "c.go")); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("move to another package", func(t *testing.T) {
		docs := t.TempDir()
		writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/eqmv\n\ngo 1.21\n")
		writeFile(t, filepath.Join(docs, "app.go"), "package eqmv\n\nimport \"example.com/eqmv/sub\"\n\n// Use calls the moved function.\nfunc Use() int { return sub.F() }\n")
		writeFile(t, filepath.Join(docs, "sub", "f.go"), "package sub\n\n// F is moved to sub2 by the edit.\nfunc F() int { return 2 }\n")
		equal(t, docs, func() {
			writeFile(t, filepath.Join(docs, "sub2", "f.go"), "package sub2\n\n// F is moved to sub2 by the edit.\nfunc F() int { return 2 }\n")
			if err := os.Remove(filepath.Join(docs, "sub", "f.go")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(docs, "app.go"), "package eqmv\n\nimport \"example.com/eqmv/sub2\"\n\n// Use calls the moved function.\nfunc Use() int { return sub2.F() }\n")
		})
	})

	t.Run("deleted methods file", func(t *testing.T) {
		docs := t.TempDir()
		writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/eqmd\n\ngo 1.21\n")
		writeFile(t, filepath.Join(docs, "types.go"), "package eqmd\n\n// T is implemented across two files.\ntype T struct{}\n\n// M1 is the first method.\nfunc (T) M1() {}\n")
		writeFile(t, filepath.Join(docs, "methods.go"), "package eqmd\n\n// M2 is the second method, deleted with this file.\nfunc (T) M2() {}\n")
		writeFile(t, filepath.Join(docs, "iface.go"), "package eqmd\n\n// I needs both methods.\ntype I interface{ M1(); M2() }\n")
		equal(t, docs, func() {
			if err := os.Remove(filepath.Join(docs, "methods.go")); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("method removed interface in importer", func(t *testing.T) {
		docs := t.TempDir()
		writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/eqsi\n\ngo 1.21\n")
		writeFile(t, filepath.Join(docs, "app.go"), "package eqsi\n\nimport \"example.com/eqsi/store\"\n\n// Getter is implemented by store.Store.\ntype Getter interface{ Get() int }\n\n// Use dispatches through Getter.\nfunc Use(g Getter) int { return g.Get() }\n\nvar zero store.Store\n")
		writeFile(t, filepath.Join(docs, "store", "store.go"), "package store\n\n// Store implements the root Getter.\ntype Store struct{}\n\n// Get implements Getter.\nfunc (*Store) Get() int { return 1 }\n")
		equal(t, docs, func() {
			writeFile(t, filepath.Join(docs, "store", "store.go"), "package store\n\n// Store no longer implements Getter.\ntype Store struct{}\n")
		})
	})

	t.Run("type-error convergence", func(t *testing.T) {
		docs := t.TempDir()
		writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/eqte\n\ngo 1.21\n")
		writeFile(t, filepath.Join(docs, "a.go"), "package eqte\n\n// T has a method.\ntype T struct{}\n\n// M is called by Added.\nfunc (T) M() int { return 1 }\n")
		writeFile(t, filepath.Join(docs, "b.go"), "package eqte\n\n// Sibling is clean at build time.\nfunc Sibling() int { return 0 }\n")
		synced := newEnv(t)
		buildFormat2(t, synced, docs)
		// Added lands while the sibling has a type error: syntactic fallback.
		writeFile(t, filepath.Join(docs, "a.go"), "package eqte\n\n// T has a method.\ntype T struct{}\n\n// M is called by Added.\nfunc (T) M() int { return 1 }\n\n// Added is synced while b.go is broken.\nfunc (t T) Added() int { return t.M() }\n")
		writeFile(t, filepath.Join(docs, "b.go"), "package eqte\n\n// Sibling has a type error.\nfunc Sibling() int { return undefinedName }\n")
		syncGraph(t, synced)
		// Only the sibling is fixed; a.go must still converge to exact.
		writeFile(t, filepath.Join(docs, "b.go"), "package eqte\n\n// Sibling is clean again.\nfunc Sibling() int { return 0 }\n")
		syncGraph(t, synced)
		full := newEnv(t)
		buildFormat2(t, full, docs)
		assertGraphEquals(t, "type-error convergence", openGraphDB(t, synced), openGraphDB(t, full))
	})

	t.Run("go.work workspace", func(t *testing.T) {
		docs := t.TempDir()
		writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/eqwa\n\ngo 1.21\n")
		writeFile(t, filepath.Join(docs, "go.work"), "go 1.21\n\nuse (\n\t.\n\t./wb\n)\n")
		writeFile(t, filepath.Join(docs, "wb", "go.mod"), "module example.com/eqwb\n\ngo 1.21\n")
		writeFile(t, filepath.Join(docs, "wb", "wb.go"), "package eqwb\n\n// Reader is an interface of the second main module.\ntype Reader interface{ Read() int }\n\n// Buf implements Reader.\ntype Buf struct{}\n\n// Read implements Reader.\nfunc (Buf) Read() int { return 0 }\n")
		const app = "package eqwa\n\nimport \"example.com/eqwb\"\n\n// T looks like eqwb.Reader.\ntype T struct{}\n\n// Read looks like Reader.Read.\nfunc (T) Read() int { return 1 }\n\n// Use calls through the other module's type.\nfunc Use(b eqwb.Buf) int { return b.Read() }\n"
		writeFile(t, filepath.Join(docs, "app.go"), app)
		equal(t, docs, func() {
			writeFile(t, filepath.Join(docs, "app.go"), app+"\n// Extra is added by the edit.\nfunc Extra() int { return Use(eqwb.Buf{}) }\n")
		})
	})
}

// allSymbolRows dumps the symbol rows with their source anchors (file, lines,
// column, byte offset), so an equivalence check covers declarations as well as
// edges (a sync that drops or moves a symbol without changing the edge
// multiset must not pass).
func allSymbolRows(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT fqn || ' ' || name || ' ' || kind || ' ' || file || ' lines=' || start_line || '-' || end_line || ' col=' || start_col || ' off=' || byte_offset || ' test=' || is_test FROM symbols ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		b.WriteString(s + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// assertGraphEquals fails when the synced index's symbols (with anchors) or
// edges differ from a full build of the same tree.
func assertGraphEquals(t *testing.T, scenario string, synced, full *sql.DB) {
	t.Helper()
	if got, want := allSymbolRows(t, synced), allSymbolRows(t, full); got != want {
		t.Errorf("%s: SYMBOLS differ\nsync:\n%s\nfull build:\n%s", scenario, got, want)
	}
	if got, want := allEdgeRows(t, synced), allEdgeRows(t, full); got != want {
		t.Errorf("%s: EDGES differ\nsync:\n%s\nfull build:\n%s", scenario, got, want)
	}
}

func allEdgeRows(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT src || ' ' || kind || ' ' || dst || ' ' || confidence || ' ext=' || dst_external FROM edges ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		b.WriteString(s + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// An edit that removes a method makes the unchanged callers dirty. The dirty
// pass re-resolves them exactly, also in a package the changed file is not
// in: the call now reaches the promoted method.
func TestSyncReResolvesDirtyCallersExactly(t *testing.T) {
	env := newEnv(t)
	docs := syncExactFixture(t)
	buildFormat2(t, env, docs)

	// Store loses its own Get; s.Get() is Base.Get from now on.
	edited := strings.Replace(sxStore, "// Get shadows Base.Get.\nfunc (*Store) Get() int { return 2 }\n", "", 1)
	if edited == sxStore {
		t.Fatal("the fixture edit did not apply")
	}
	writeFile(t, filepath.Join(docs, "store", "store.go"), edited)
	out := syncGraph(t, env)
	if !strings.Contains(out, "dirty graph file") {
		t.Fatalf("the removed method did not make its callers dirty: %s", out)
	}
	db := openGraphDB(t, env)
	wantExact(t, db, "CALLS", [][2]string{
		{"/cli.Run", "/store.Base.Get"},    // dirty file of another package
		{".Runner.Old", "/store.Base.Get"}, // dirty file of the root package
		{".Use", ".Getter"},                // same dirty file, interface dispatch
		{".Other", ".Runner.Old"},          // untouched
	})
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE dst = ?`, syncExactPkg+"/store.Store.Get"); n != 0 {
		t.Errorf("%d edge(s) into the removed method", n)
	}
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty", n)
	}
	assertNoOrphans(t, db)
}

// A partial type-check sees only the interfaces of the changed packages and
// of what they import. store.Store implements Getter, declared in a package
// that imports store: the sync of store.go cannot re-derive that edge and
// must not drop it.
func TestSyncKeepsImplementsOfUnloadedInterfaces(t *testing.T) {
	env := newEnv(t)
	docs := syncExactFixture(t)
	buildFormat2(t, env, docs)

	writeFile(t, filepath.Join(docs, "store", "store.go"), sxStore+"\n// Len is added by the edit.\nfunc (*Store) Len() int { return 0 }\n")
	syncGraph(t, env)
	db := openGraphDB(t, env)
	wantExact(t, db, "IMPLEMENTS", [][2]string{{"/store.Store", ".Getter"}, {"/store.Spare", ".Getter"}})
	wantExact(t, db, "CALLS", sxBuiltCalls)

	// A carried edge goes away with its type.
	i := strings.Index(sxStore, "// Spare implements")
	if i < 0 {
		t.Fatal("the fixture edit did not apply")
	}
	writeFile(t, filepath.Join(docs, "store", "store.go"), sxStore[:i])
	syncGraph(t, env)
	db = openGraphDB(t, env)
	wantExact(t, db, "IMPLEMENTS", [][2]string{{"/store.Store", ".Getter"}})
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE src = ?`, syncExactPkg+"/store.Spare"); n != 0 {
		t.Errorf("%d edge(s) from the removed type", n)
	}
	assertNoOrphans(t, db)
}

// When the load fails the sync still succeeds, with the syntactic resolver,
// and says what the graph of the synced files lacks.
func TestSyncFallsBackWhenExactResolutionFails(t *testing.T) {
	const plain = "\n// Plain calls a function, which the syntactic resolver follows.\nfunc Plain() int { return Use(nil) }\n"

	t.Run("unloadable module", func(t *testing.T) {
		env := newEnv(t)
		docs := syncExactFixture(t)
		buildFormat2(t, env, docs)
		// No Go release satisfies this go line and downloads are off.
		writeFile(t, filepath.Join(docs, "go.mod"), "module "+syncExactPkg+"\n\ngo 1.999.0\n")
		writeFile(t, filepath.Join(docs, "app.go"), sxApp+plain)
		out := syncGraph(t, env)
		if !strings.Contains(out, "exact Go call resolution is unavailable for this sync") {
			t.Errorf("no warning about the syntactic fallback: %s", out)
		}
		db := openGraphDB(t, env)
		if conf := edgeConfidence(t, db, "CALLS", syncExactPkg+".Plain", syncExactPkg+".Use"); conf != "extracted" {
			t.Errorf("CALLS Plain -> Use = %q, want extracted (syntactic)", conf)
		}
		// The unchanged files keep what the full build resolved.
		wantExact(t, db, "CALLS", [][2]string{{"/cli.Run", "/store.Store.Get"}})
		assertNoOrphans(t, db)
	})

	t.Run("package with a type error", func(t *testing.T) {
		env := newEnv(t)
		docs := syncExactFixture(t)
		buildFormat2(t, env, docs)
		writeFile(t, filepath.Join(docs, "app.go"), sxApp+plain+"\nfunc Broken() int { return undefinedName }\n")
		out := syncGraph(t, env)
		if !strings.Contains(out, "exact Go call resolution is unavailable for this sync") || !strings.Contains(out, "undefinedName") {
			t.Errorf("the warning does not name the type error: %s", out)
		}
		db := openGraphDB(t, env)
		if conf := edgeConfidence(t, db, "CALLS", syncExactPkg+".Plain", syncExactPkg+".Use"); conf != "extracted" {
			t.Errorf("CALLS Plain -> Use = %q, want extracted (syntactic)", conf)
		}
		assertNoOrphans(t, db)

		// Once the package type-checks again, the next sync of the file is
		// exact again.
		writeFile(t, filepath.Join(docs, "app.go"), sxApp+plain)
		if out := syncGraph(t, env); strings.Contains(out, "warning") {
			t.Errorf("unexpected warning after the fix: %s", out)
		}
		db = openGraphDB(t, env)
		wantExact(t, db, "CALLS", [][2]string{{".Plain", ".Use"}, {".Runner.Old", "/store.Store.Get"}})
	})

	t.Run("one of two packages has a type error", func(t *testing.T) {
		env := newEnv(t)
		docs := syncExactFixture(t)
		buildFormat2(t, env, docs)
		writeFile(t, filepath.Join(docs, "app.go"), sxAppEdited)
		writeFile(t, filepath.Join(docs, "cli", "cli.go"), sxCLI+"\nfunc Broken() int { return undefinedName }\n")
		out := syncGraph(t, env)
		if !strings.Contains(out, "1 Go package(s) of this sync did not type-check") {
			t.Errorf("no warning about the skipped package: %s", out)
		}
		db := openGraphDB(t, env)
		wantExact(t, db, "CALLS", [][2]string{{".Runner.New", "/store.Store.Put"}})
		if conf := edgeConfidence(t, db, "CALLS", syncExactPkg+"/cli.Run", syncExactPkg+"/store.Store.Get"); conf != "" {
			t.Errorf("CALLS cli.Run -> Store.Get = %q in a package that does not type-check, want none", conf)
		}
		assertNoOrphans(t, db)
	})

	t.Run("no go toolchain", func(t *testing.T) {
		env := newEnv(t)
		docs := syncExactFixture(t)
		buildFormat2(t, env, docs)
		t.Setenv("PATH", t.TempDir())
		writeFile(t, filepath.Join(docs, "app.go"), sxApp+plain)
		out := syncGraph(t, env)
		if !strings.Contains(out, "exact Go call resolution is unavailable for this sync") {
			t.Errorf("no warning about the syntactic fallback: %s", out)
		}
		if conf := edgeConfidence(t, openGraphDB(t, env), "CALLS", syncExactPkg+".Plain", syncExactPkg+".Use"); conf != "extracted" {
			t.Errorf("CALLS Plain -> Use = %q, want extracted (syntactic)", conf)
		}
	})
}

// No go/packages load is started when it cannot help: with
// GLEANN_GRAPH_EXACT=0, and for a sync that changed no Go file at all. (A
// changed _test.go is a candidate now: the load type-checks the test variants,
// so a test-only sync re-resolves its file exactly.) The proof is the missing
// warning: without a go command on PATH every load fails.
func TestSyncSkipsTheLoadWhenItCannotHelp(t *testing.T) {
	t.Run("GLEANN_GRAPH_EXACT=0", func(t *testing.T) {
		env := newEnv(t)
		docs := syncExactFixture(t)
		buildFormat2(t, env, docs)
		t.Setenv("GLEANN_GRAPH_EXACT", "0")
		t.Setenv("PATH", t.TempDir())
		writeFile(t, filepath.Join(docs, "app.go"), sxAppEdited)
		if out := syncGraph(t, env); strings.Contains(out, "warning") {
			t.Errorf("a load was attempted with exact resolution off: %s", out)
		}
		db := openGraphDB(t, env)
		if n := countRows(t, db, `SELECT count(*) FROM edges WHERE file = 'app.go' AND confidence = 'exact'`); n != 0 {
			t.Errorf("%d exact edge(s) in app.go with exact resolution off", n)
		}
	})

	t.Run("only a document changed", func(t *testing.T) {
		env := newEnv(t)
		docs := syncExactFixture(t)
		writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n\nfirst\n")
		buildFormat2(t, env, docs)
		t.Setenv("PATH", t.TempDir())
		writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n\nsecond\n")
		if out := syncGraph(t, env); strings.Contains(out, "warning") {
			t.Errorf("a load was attempted for a sync without a Go file: %s", out)
		}
		wantExact(t, openGraphDB(t, env), "CALLS", sxBuiltCalls)
	})
}

// The code graph of a sync holds what a full build's holds: code source files.
// A changed document used to be stored as "block_N" symbols, whose appearance
// made every file of its directory dirty and re-extracted.
func TestSyncKeepsNonCodeFilesOutOfTheCodeGraph(t *testing.T) {
	env := newEnv(t)
	docs := syncExactFixture(t)
	writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n\nfirst\n")
	writeFile(t, filepath.Join(docs, "LICENSE"), "Permission is hereby granted.\n")
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	before := allEdgeRows(t, db)
	const nonCode = `SELECT count(*) FROM symbols WHERE file IN ('notes.md', 'LICENSE', 'go.mod')`
	if n := countRows(t, db, nonCode); n != 0 {
		t.Fatalf("the full build stored %d symbol(s) of non-code files", n)
	}

	writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n\nsecond\n")
	writeFile(t, filepath.Join(docs, "LICENSE"), "Permission is hereby granted, free of charge.\n")
	out := syncGraph(t, env)
	db = openGraphDB(t, env)
	if n := countRows(t, db, nonCode); n != 0 {
		t.Errorf("the sync stored %d symbol(s) of non-code files", n)
	}
	if strings.Contains(out, "dirty graph file") {
		t.Errorf("a changed document made code files dirty: %s", out)
	}
	if after := allEdgeRows(t, db); after != before {
		t.Errorf("a sync of documents changed the code graph\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// Rows an earlier sync wrote for a document go away when it is synced.
	st, err := store.Open(filepath.Join(env.cfg.IndexDir, "g", "g.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = graphstore.New(st).ApplyFileDelta(context.Background(), graphstore.FileDelta{
		File:    "notes.md",
		Symbols: []graphstore.Symbol{{FQN: syncExactPkg + ".block_1", Name: "block_1", Kind: "block", File: "notes.md", StartLine: 1, EndLine: 3}},
	})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(docs, "notes.md"), "# Notes\n\nthird\n")
	syncGraph(t, env)
	db = openGraphDB(t, env)
	if n := countRows(t, db, nonCode); n != 0 {
		t.Errorf("%d symbol(s) of a document left after its sync", n)
	}
	if after := allEdgeRows(t, db); after != before {
		t.Errorf("retracting a document's rows changed the code graph\nbefore:\n%s\nafter:\n%s", before, after)
	}
	assertNoOrphans(t, db)
}
