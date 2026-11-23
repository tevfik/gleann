package indexing

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A rename inside a package is a deletion plus an addition. The sync must
// retract the deleted file's graph rows before it applies the added one:
// otherwise the first-declaration-wins dedupe drops the new file's
// declarations (the old file still owns their FQNs) and the retraction of the
// old file then removes them for good (review sync-exact Q1). Both index
// paths are covered: the vector path's passage step does not retract graph
// rows of deleted sources, the lexical (--instant) path's does.

const (
	rnMod = "example.com/zzrn"
	rnA   = "package zzrn\n\n// Helper does the work.\nfunc Helper() int { return 1 }\n"
	rnB   = "package zzrn\n\n// Use calls Helper.\nfunc Use() int { return Helper() }\n"
)

func renameFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module "+rnMod+"\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "a.go"), rnA)
	writeFile(t, filepath.Join(dir, "b.go"), rnB)
	return dir
}

// buildGraphMode builds a format-2 index with the graph, on the vector path
// (instant=false) or the lexical-only path (instant=true).
func buildGraphMode(t *testing.T, env *testEnv, docs string, instant bool) {
	t.Helper()
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: "g", DocsDir: docs, Graph: true, InstantOnly: instant}); err != nil {
		t.Fatalf("Build: %v", err)
	}
}

// syncGraphMode syncs index "g" on the same path it was built on.
func syncGraphMode(t *testing.T, env *testEnv, instant bool) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(&out), Name: "g", InstantOnly: instant}); err != nil {
		t.Fatalf("Sync: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestSyncRenameWithinPackage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		instant bool
	}{
		{"vector", false},
		{"lexical", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := renameFixture(t)
			env := newEnv(t)
			buildGraphMode(t, env, docs, tc.instant)
			db := openGraphDB(t, env)
			if conf := edgeConfidence(t, db, "CALLS", rnMod+".Use", rnMod+".Helper"); conf != "exact" {
				t.Fatalf("the full build is not exact (CALLS Use -> Helper = %q): nothing to compare the sync with", conf)
			}

			if err := os.Rename(filepath.Join(docs, "a.go"), filepath.Join(docs, "c.go")); err != nil {
				t.Fatal(err)
			}
			syncGraphMode(t, env, tc.instant)
			db = openGraphDB(t, env)
			if n := dirtyCount(t, db); n != 0 {
				t.Errorf("%d file(s) left dirty after the rename sync", n)
			}
			// A second sync must not change the graph further (the review's
			// re-sync did not repair the loss).
			syncGraphMode(t, env, tc.instant)

			full := newEnv(t)
			buildGraphMode(t, full, docs, tc.instant)
			assertGraphEquals(t, "rename a.go->c.go", openGraphDB(t, env), openGraphDB(t, full))
			assertNoOrphans(t, openGraphDB(t, env))
		})
	}
}

// A rename that also moves the file to another package: the old package's
// rows go away, the new package's declarations and the caller's re-bound
// call land as a full build of the moved tree has them.
func TestSyncRenameAcrossPackages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		instant bool
	}{
		{"vector", false},
		{"lexical", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := t.TempDir()
			writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/zzmv\n\ngo 1.21\n")
			writeFile(t, filepath.Join(docs, "a.go"), "package zzmv\n\n// Helper does the work.\nfunc Helper() int { return 1 }\n")
			writeFile(t, filepath.Join(docs, "b.go"), "package zzmv\n\n// Use calls Helper.\nfunc Use() int { return Helper() }\n")
			env := newEnv(t)
			buildGraphMode(t, env, docs, tc.instant)

			if err := os.Remove(filepath.Join(docs, "a.go")); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(docs, "sub", "c.go"), "package sub\n\n// Helper does the work.\nfunc Helper() int { return 1 }\n")
			writeFile(t, filepath.Join(docs, "b.go"), "package zzmv\n\nimport \"example.com/zzmv/sub\"\n\n// Use calls Helper.\nfunc Use() int { return sub.Helper() }\n")
			syncGraphMode(t, env, tc.instant)
			db := openGraphDB(t, env)
			if n := dirtyCount(t, db); n != 0 {
				t.Errorf("%d file(s) left dirty after the move sync", n)
			}

			full := newEnv(t)
			buildGraphMode(t, full, docs, tc.instant)
			assertGraphEquals(t, "move a.go->sub/c.go", openGraphDB(t, env), openGraphDB(t, full))
			assertNoOrphans(t, openGraphDB(t, env))
		})
	}
}
