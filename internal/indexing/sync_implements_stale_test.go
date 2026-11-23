package indexing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Stale method-set IMPLEMENTS edges (reviews Q3, G1, G7): a partial load does
// not see an interface declared in a package that imports the changed one, so
// the stored edge used to be carried over without re-validation. After a
// method removal (or the deletion of a file that held methods of a type
// declared in another file) the sync graph must equal a full build's: the
// type no longer satisfies the interface, the edge must go.

// Q3/G1: the interface lives in a package that imports the implementor's.
func TestSyncDropsStaleImplementsWhenMethodRemoved(t *testing.T) {
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/zzsi\n\ngo 1.21\n")
	const root = `package zzsi

import "example.com/zzsi/store"

// Getter is implemented by store.Store, a type of the package this one imports.
type Getter interface{ Get() int }

// Use dispatches through the interface.
func Use(g Getter) int { return g.Get() }

var zero store.Store
`
	const storeWithGet = `package store

// Store is the implementor.
type Store struct{}

// Get satisfies Getter.
func (*Store) Get() int { return 1 }
`
	writeFile(t, filepath.Join(docs, "iface.go"), root)
	writeFile(t, filepath.Join(docs, "store", "store.go"), storeWithGet)
	env := newEnv(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "IMPLEMENTS", "example.com/zzsi/store.Store", "example.com/zzsi.Getter"); conf != "exact" {
		t.Fatalf("the full build does not have Store IMPLEMENTS Getter exact (%q): bad fixture", conf)
	}

	// Remove Get: Store no longer satisfies Getter.
	writeFile(t, filepath.Join(docs, "store", "store.go"), strings.Replace(storeWithGet, "\n// Get satisfies Getter.\nfunc (*Store) Get() int { return 1 }\n", "", 1))
	syncGraph(t, env)
	db = openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "IMPLEMENTS", "example.com/zzsi/store.Store", "example.com/zzsi.Getter"); conf != "" {
		t.Errorf("IMPLEMENTS Store -> Getter = %q after Get was removed, want none (stale carried edge)", conf)
	}
	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "method removed, interface in importer", openGraphDB(t, env), openGraphDB(t, full))
	assertNoOrphans(t, openGraphDB(t, env))
}

// G7: the deleted file held methods of a type declared in another file; the
// interface is in the same package. The surviving type's declaration file is
// unchanged and must still be re-resolved.
func TestSyncDropsStaleImplementsWhenMethodFileDeleted(t *testing.T) {
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/zzmd\n\ngo 1.21\n")
	writeFile(t, filepath.Join(docs, "types.go"), "package zzmd\n\n// T is the implementor, declared here.\ntype T struct{}\n\n// M1 is the first method.\nfunc (T) M1() {}\n")
	writeFile(t, filepath.Join(docs, "methods.go"), "package zzmd\n\n// M2 is the second method, in a file of its own.\nfunc (T) M2() {}\n")
	writeFile(t, filepath.Join(docs, "iface.go"), "package zzmd\n\n// I needs both methods.\ntype I interface{ M1(); M2() }\n")
	env := newEnv(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "IMPLEMENTS", "example.com/zzmd.T", "example.com/zzmd.I"); conf != "exact" {
		t.Fatalf("the full build does not have T IMPLEMENTS I exact (%q): bad fixture", conf)
	}

	if err := os.Remove(filepath.Join(docs, "methods.go")); err != nil {
		t.Fatal(err)
	}
	syncGraph(t, env)
	db = openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "IMPLEMENTS", "example.com/zzmd.T", "example.com/zzmd.I"); conf != "" {
		t.Errorf("IMPLEMENTS T -> I = %q after the methods file was deleted, want none (stale edge)", conf)
	}
	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "method file deleted", openGraphDB(t, env), openGraphDB(t, full))
	assertNoOrphans(t, openGraphDB(t, env))
}

// The same, with the interface in another package (one that imports the
// implementor's) and the method removed by an edit instead of a deletion:
// the type's declaration file is unchanged, the interface's package is not
// among the changed files' imports.
func TestSyncDropsStaleImplementsCrossPackageMethodEdit(t *testing.T) {
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/zzmx\n\ngo 1.21\n")
	writeFile(t, filepath.Join(docs, "types.go"), "package zzmx\n\n// T is the implementor, declared here.\ntype T struct{}\n\n// M1 is the first method.\nfunc (T) M1() {}\n")
	const methods = "package zzmx\n\n// M2 is the second method, in a file of its own.\nfunc (T) M2() {}\n"
	writeFile(t, filepath.Join(docs, "methods.go"), methods)
	writeFile(t, filepath.Join(docs, "sub", "iface.go"), "package sub\n\nimport \"example.com/zzmx\"\n\n// I needs both methods.\ntype I interface{ M1(); M2() }\n\nvar _ zzmx.T\n")
	env := newEnv(t)
	buildFormat2(t, env, docs)
	db := openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "IMPLEMENTS", "example.com/zzmx.T", "example.com/zzmx/sub.I"); conf != "exact" {
		t.Fatalf("the full build does not have T IMPLEMENTS sub.I exact (%q): bad fixture", conf)
	}

	// Remove M2 by editing the methods file; types.go stays untouched.
	writeFile(t, filepath.Join(docs, "methods.go"), "package zzmx\n\n// M2 is gone; the file keeps a declaration so it stays in the graph.\nfunc Other() {}\n")
	syncGraph(t, env)
	db = openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "IMPLEMENTS", "example.com/zzmx.T", "example.com/zzmx/sub.I"); conf != "" {
		t.Errorf("IMPLEMENTS T -> sub.I = %q after M2 was removed, want none (stale edge)", conf)
	}
	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "cross-package method edit", openGraphDB(t, env), openGraphDB(t, full))
	assertNoOrphans(t, openGraphDB(t, env))
}
