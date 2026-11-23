package indexing

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// syncTestPkg is the module of the test-file sync fixtures below. Their only
// non-trivial calls live in a _test.go file and go through a receiver
// variable: the syntactic resolver drops them, so they are an exact edge only
// when the sync type-checked the test file.
const syncTestPkg = "example.com/sxt"

const sxtProd = `package sxt

// S is the type the tests call through a receiver variable.
type S struct{}

// Run is called as svc.Run() in the tests.
func (s *S) Run() int { return 1 }

// New returns an S, so a test can call a method without naming the type.
func New() *S { return &S{} }
`

const sxtTestOne = `package sxt

import "testing"

func TestOne(t *testing.T) {
	svc := New()
	if got := svc.Run(); got != 1 {
		t.Fatalf("Run = %d", got)
	}
}
`

const sxtTestTwo = `package sxt

import "testing"

func TestOne(t *testing.T) {
	svc := New()
	if got := svc.Run(); got != 1 {
		t.Fatalf("Run = %d", got)
	}
}

func TestTwo(t *testing.T) {
	svc := New()
	if got := svc.Run(); got != 1 {
		t.Fatalf("Run = %d", got)
	}
}
`

const sxtBrokenTest = `package sxt

import "testing"

func TestBroken(t *testing.T) { undefinedName() }
`

// syncTestFixture writes a one-package module whose app_test.go holds testSrc
// (empty means no test file). It is deliberately small: a sync of the test
// file must be enough to keep or restore its exact edges.
func syncTestFixture(t *testing.T, testSrc string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+syncTestPkg+"\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "app.go"), sxtProd)
	if testSrc != "" {
		writeFile(t, filepath.Join(dir, "app_test.go"), testSrc)
	}
	return dir
}

// syncTestChanged applies one SyncChanged batch limited to rel files (slash
// paths relative to docs) and returns the sync output.
func syncTestChanged(t *testing.T, env *testEnv, docs string, rel ...string) string {
	t.Helper()
	var out bytes.Buffer
	files := make([]string, len(rel))
	for i, r := range rel {
		files[i] = filepath.Join(docs, filepath.FromSlash(r))
	}
	if _, err := SyncChanged(context.Background(), SyncOptions{Options: env.options(&out), Name: "g", DocsDir: docs}, files); err != nil {
		t.Fatalf("SyncChanged(%v): %v\n%s", rel, err, out.String())
	}
	return out.String()
}

// wantExactTests is wantExact for the module of this file (wantExact uses the
// module of graph_sync_exact_test.go).
func wantExactTests(t *testing.T, db *sql.DB, kind string, edges [][2]string) {
	t.Helper()
	for _, e := range edges {
		if conf := edgeConfidence(t, db, kind, syncTestPkg+e[0], syncTestPkg+e[1]); conf != "exact" {
			t.Errorf("%s %s -> %s = %q, want exact", kind, e[0], e[1], conf)
		}
	}
}

// Finding 1: a sync whose only changed file is a _test.go must keep the exact
// test-caller edges the full build produced and add the new ones. Before the
// fix, exactCandidate excluded test files, no load ran, and the file was
// re-extracted syntactically: the build's exact edge disappeared and the new
// one was never created.
func TestSyncChangedTestOnlyEditKeepsAndAddsExactTestCallers(t *testing.T) {
	env := newEnv(t)
	docs := syncTestFixture(t, sxtTestOne)
	buildFormat2(t, env, docs)

	db := openGraphDB(t, env)
	wantExactTests(t, db, "CALLS", [][2]string{{".TestOne", ".S.Run"}})
	if t.Failed() {
		t.Fatal("the full build is not exact on the test caller: nothing to compare the sync with")
	}

	writeFile(t, filepath.Join(docs, "app_test.go"), sxtTestTwo)
	out := syncTestChanged(t, env, docs, "app_test.go")
	if strings.Contains(out, "warning") {
		t.Errorf("unexpected warning for a test-only sync: %s", out)
	}

	db = openGraphDB(t, env)
	wantExactTests(t, db, "CALLS", [][2]string{
		{".TestOne", ".S.Run"}, // the edge the full build created must survive
		{".TestTwo", ".S.Run"}, // the edge the edit adds must be exact too
	})
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty after a clean test-only sync", n)
	}
	assertNoOrphans(t, db)
}

// Finding 1: adding only a new _test.go to a package must resolve its receiver
// calls exactly. Before the fix no load was started for the new file.
func TestSyncChangedAddedTestFileOnlyResolvesExactly(t *testing.T) {
	env := newEnv(t)
	docs := syncTestFixture(t, "")
	buildFormat2(t, env, docs)

	writeFile(t, filepath.Join(docs, "app_test.go"), sxtTestOne)
	out := syncTestChanged(t, env, docs, "app_test.go")
	if strings.Contains(out, "warning") {
		t.Errorf("unexpected warning for an added test file: %s", out)
	}

	db := openGraphDB(t, env)
	wantExactTests(t, db, "CALLS", [][2]string{{".TestOne", ".S.Run"}})
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty after adding a test file", n)
	}
	assertNoOrphans(t, db)
}

// Finding 1: a test file that broke its package's test variant is retried when
// it alone is fixed. Before the fix the fix-sync started no load, so the exact
// edges were never restored and the retry promise (FailedFiles ->
// graph_dirty) was not honoured.
func TestSyncRetriesAFixedTestFile(t *testing.T) {
	env := newEnv(t)
	docs := syncTestFixture(t, sxtTestOne)
	buildFormat2(t, env, docs)

	// Break the test variant: its file keeps the syntactic resolver and is
	// re-marked dirty for the next sync.
	writeFile(t, filepath.Join(docs, "app_test.go"), sxtTestOne+"\nfunc TestBroken(t *testing.T) { undefinedName() }\n")
	out := syncTestChanged(t, env, docs, "app_test.go")
	if !strings.Contains(out, "undefinedName") {
		t.Errorf("the failed test variant was not reported: %s", out)
	}
	db := openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "CALLS", syncTestPkg+".TestOne", syncTestPkg+".S.Run"); conf == "exact" {
		t.Error("a broken test file kept an exact edge")
	}
	if n := dirtyCount(t, db); n == 0 {
		t.Error("the broken test file was not left dirty for retry")
	}

	// Fix only the test file: the sync must load it again, restore the exact
	// edges and clear the dirty flag.
	writeFile(t, filepath.Join(docs, "app_test.go"), sxtTestTwo)
	out = syncTestChanged(t, env, docs, "app_test.go")
	if strings.Contains(out, "warning") {
		t.Errorf("unexpected warning after fixing the test file: %s", out)
	}
	db = openGraphDB(t, env)
	wantExactTests(t, db, "CALLS", [][2]string{{".TestOne", ".S.Run"}, {".TestTwo", ".S.Run"}})
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d dirty row(s) after the fix-sync, want 0", n)
	}
	assertNoOrphans(t, db)
}

// Finding 1: a build with a broken _test.go leaves that test file syntactic;
// fixing only the test file must restore its exact edges on the next sync (the
// sequence the cross-review demonstrated as lost).
func TestSyncBuildWithBrokenTestThenFixOnlyTheTest(t *testing.T) {
	env := newEnv(t)
	docs := syncTestFixture(t, sxtBrokenTest)
	buildFormat2(t, env, docs)

	db := openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "CALLS", syncTestPkg+".TestOne", syncTestPkg+".S.Run"); conf == "exact" {
		t.Fatal("the broken test file was resolved exactly at build time")
	}

	writeFile(t, filepath.Join(docs, "app_test.go"), sxtTestOne)
	syncTestChanged(t, env, docs, "app_test.go")
	db = openGraphDB(t, env)
	wantExactTests(t, db, "CALLS", [][2]string{{".TestOne", ".S.Run"}})
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty after fixing the only broken test", n)
	}
	assertNoOrphans(t, db)
}

// Finding 1 edge case: a directory holding only _test.go files has no
// production view. Loading it must stay quiet (no spurious skipped-package
// warning) and must not mark its file failed/dirty forever; the file keeps the
// syntactic resolver, as in a full build.
func TestSyncTestOnlyPackageStaysSyntacticWithoutWarnings(t *testing.T) {
	env := newEnv(t)
	docs := syncTestFixture(t, sxtTestOne)
	buildFormat2(t, env, docs)

	const rel = "onlytest/x_test.go"
	writeFile(t, filepath.Join(docs, filepath.FromSlash(rel)), "package onlytest\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	out := syncTestChanged(t, env, docs, rel)
	if strings.Contains(out, "warning") {
		t.Errorf("a test-only package produced a warning: %s", out)
	}
	db := openGraphDB(t, env)
	if k := countRows(t, db, `SELECT count(*) FROM files WHERE path = ? AND graph_dirty = 1`, rel); k != 0 {
		t.Errorf("the test-only file was marked dirty")
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE fqn = ?`, syncTestPkg+"/onlytest.TestX"); n != 1 {
		t.Errorf("the test-only file was not indexed syntactically (%d symbol rows)", n)
	}

	// A second sync of the same directory must stay just as quiet and clean.
	out = syncTestChanged(t, env, docs, rel)
	if strings.Contains(out, "warning") {
		t.Errorf("a repeated sync of a test-only package produced a warning: %s", out)
	}
	if k := countRows(t, db, `SELECT count(*) FROM files WHERE path = ? AND graph_dirty = 1`, rel); k != 0 {
		t.Errorf("the test-only file was marked dirty by the repeated sync")
	}
}
