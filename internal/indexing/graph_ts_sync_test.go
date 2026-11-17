//go:build treesitter

package indexing

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const pySvc = "class Reg:\n    def add(self, x):\n        pass\n"
const pyApp = "def run(r):\n    r.add(1)\n\n\nrun(None)\n"

// modulePrefix strips the module part of an FQN ("003.run" becomes "run").
var modulePrefix = regexp.MustCompile(`(^|\n| )[^ \n.]*\.`)

const pyEdgesQuery = `SELECT src, dst, dst_external FROM edges WHERE kind = 'CALLS' AND file LIKE '%.py'`

func edgeRows(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, b string
		var ext int
		if err := rows.Scan(&a, &b, &ext); err != nil {
			t.Fatal(err)
		}
		out = append(out, a+" -> "+b+" ext="+strconv.Itoa(ext))
	}
	sort.Strings(out)
	return out
}

// Incremental sync of Python files keeps the graph referentially consistent:
// a method that disappears from one file leaves no internal edge into it from
// an unchanged file, and the edge binds to the method again when it returns.
func TestFormat2PythonIncrementalSyncKeepsIntegrity(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "svc.py"), pySvc)
	writeFile(t, filepath.Join(docs, "app.py"), pyApp)
	buildFormat2(t, env, docs)
	sync := func() {
		t.Helper()
		if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	db := openGraphDB(t, env)
	got := modulePrefix.ReplaceAllString(strings.Join(edgeRows(t, db, pyEdgesQuery), "\n"), "$1")
	for _, want := range []string{"run -> Reg::add ext=0", "<script:app_py> -> run ext=0"} {
		if !strings.Contains(got, want) {
			t.Errorf("initial graph lacks %q:\n%s", want, got)
		}
	}
	assertNoOrphans(t, db)

	// The method is renamed: the edge from the unchanged app.py cannot stay
	// internal.
	writeFile(t, filepath.Join(docs, "svc.py"), "class Reg:\n    def put(self, x):\n        pass\n")
	sync()
	db = openGraphDB(t, env)
	assertNoOrphans(t, db)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND dst LIKE '%Reg::add' AND dst_external = 0`); n != 0 {
		t.Errorf("%d internal edge(s) into the removed method", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE name = 'Reg::add'`); n != 0 {
		t.Errorf("removed method still declared")
	}

	// It comes back: app.py is unchanged, yet its call binds to the method again.
	writeFile(t, filepath.Join(docs, "svc.py"), pySvc)
	sync()
	db = openGraphDB(t, env)
	assertNoOrphans(t, db)
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE kind = 'CALLS' AND src LIKE '%run' AND dst LIKE '%Reg::add' AND dst_external = 0`); n != 1 {
		t.Errorf("edge to the restored method = %d, want 1\n%s", n, strings.Join(edgeRows(t, db, pyEdgesQuery), "\n"))
	}

	// Deleting the calling file removes its script symbol and edges.
	if err := os.Remove(filepath.Join(docs, "app.py")); err != nil {
		t.Fatal(err)
	}
	sync()
	db = openGraphDB(t, env)
	assertNoOrphans(t, db)
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE file = 'app.py'`); n != 0 {
		t.Errorf("%d symbol(s) of the deleted file survived", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM edges WHERE file = 'app.py'`); n != 0 {
		t.Errorf("%d edge(s) of the deleted file survived", n)
	}
}

// Two Python files of one directory with top-level calls each keep their own
// script caller; a sync after editing one of them leaves the other's edges.
func TestFormat2PythonScriptCallersPerFile(t *testing.T) {
	env := newEnv(t)
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "a.py"), "def f():\n    pass\n\n\nf()\n")
	writeFile(t, filepath.Join(docs, "b.py"), "def g():\n    pass\n\n\ng()\n")
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if n := countRows(t, db, `SELECT count(*) FROM symbols WHERE kind = 'script'`); n != 2 {
		t.Fatalf("script symbols = %d, want 2", n)
	}
	before := strings.Join(edgeRows(t, db, pyEdgesQuery), "\n")

	writeFile(t, filepath.Join(docs, "b.py"), "def g():\n    pass\n\n\ng()\ng()\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "g"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	db = openGraphDB(t, env)
	assertNoOrphans(t, db)
	if after := strings.Join(edgeRows(t, db, pyEdgesQuery), "\n"); after != before {
		t.Errorf("edges changed by a sync that only repeated a call:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
