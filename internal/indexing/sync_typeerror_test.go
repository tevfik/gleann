package indexing

import (
	"path/filepath"
	"strings"
	"testing"
)

// A file synced while its package had type errors is resolved syntactically;
// once the package type-checks again the file must be re-resolved exactly
// even when it was not edited itself (reviews Q4, G5). Files excluded by
// build tags are NOT such a case: their package type-checks, they are simply
// not part of it, and an index holding them must still report "already up to
// date" instead of retrying them on every sync.

func TestSyncTypeErrorConvergence(t *testing.T) {
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/zzte\n\ngo 1.21\n")
	const aBase = "package zzte\n\n// T is a type with a method.\ntype T struct{}\n\n// M does the work.\nfunc (t T) M() int { return 1 }\n"
	const aAdded = aBase + "\n// Added calls through the receiver; only go/types follows it.\nfunc (t T) Added() int { return t.M() }\n"
	writeFile(t, filepath.Join(docs, "a.go"), aBase)
	writeFile(t, filepath.Join(docs, "b.go"), "package zzte\n\n// B is fine.\nfunc B() int { return 0 }\n")
	env := newEnv(t)
	buildFormat2(t, env, docs)

	// Add the receiver call while the sibling has a type error.
	writeFile(t, filepath.Join(docs, "a.go"), aAdded)
	writeFile(t, filepath.Join(docs, "b.go"), "package zzte\n\n// B is broken for now.\nfunc B() int { return undefinedName }\n")
	out := syncGraph(t, env)
	if !strings.Contains(out, "warning:") || !strings.Contains(out, "undefinedName") {
		t.Fatalf("no warning naming the package's type error: %s", out)
	}
	db := openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "CALLS", "example.com/zzte.T.Added", "example.com/zzte.T.M"); conf != "" {
		t.Fatalf("the broken package resolved exactly (%q): the fixture does not reproduce the fallback", conf)
	}

	// Fix ONLY the sibling. a.go is unchanged; the next sync must still
	// re-resolve it exactly.
	writeFile(t, filepath.Join(docs, "b.go"), "package zzte\n\n// B is fine again.\nfunc B() int { return 0 }\n")
	syncGraph(t, env)
	db = openGraphDB(t, env)
	if conf := edgeConfidence(t, db, "CALLS", "example.com/zzte.T.Added", "example.com/zzte.T.M"); conf != "exact" {
		t.Errorf("CALLS T.Added -> T.M = %q after the sibling fixed the package, want exact", conf)
	}
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty after the package type-checks again", n)
	}
	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "type-error convergence", openGraphDB(t, env), openGraphDB(t, full))

	// And the converged index is up to date: no perpetual retry.
	if out := syncGraph(t, env); !strings.Contains(out, "already up to date") {
		t.Errorf("the converged sync is not up to date: %s", out)
	}
}

func TestSyncBuildTaggedFilesStayUpToDate(t *testing.T) {
	docs := t.TempDir()
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/zzbt\n\ngo 1.21\n")
	writeFile(t, filepath.Join(docs, "app.go"), "package zzbt\n\n// Use is the platform-independent entry.\nfunc Use() int { return 0 }\n")
	// Excluded on linux by its name suffix and by an explicit build tag; the
	// full build extracts both syntactically, a sync must not retry them.
	writeFile(t, filepath.Join(docs, "app_windows.go"), "package zzbt\n\n// Win is windows-only.\nfunc Win() int { return Use() }\n")
	writeFile(t, filepath.Join(docs, "tagged.go"), "//go:build ignore\n\npackage zzbt\n\n// Ignored is excluded by its build tag.\nfunc Ignored() int { return Use() }\n")
	env := newEnv(t)
	buildFormat2(t, env, docs)

	writeFile(t, filepath.Join(docs, "app.go"), "package zzbt\n\n// Use is the platform-independent entry.\nfunc Use() int { return 1 }\n")
	syncGraph(t, env)
	db := openGraphDB(t, env)
	if n := dirtyCount(t, db); n != 0 {
		t.Errorf("%d file(s) left dirty after a sync of a package with build-tagged files", n)
	}
	full := newEnv(t)
	buildFormat2(t, full, docs)
	assertGraphEquals(t, "build-tagged files", openGraphDB(t, env), openGraphDB(t, full))

	if out := syncGraph(t, env); !strings.Contains(out, "already up to date") {
		t.Errorf("an index with build-tagged files is not up to date: %s", out)
	}
}
