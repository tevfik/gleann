package indexing

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// sourcesOf lists the "source" metadata of every passage of an index.
func sourcesOf(t *testing.T, cfg gleann.Config, name string) []string {
	t.Helper()
	var out []string
	for _, p := range passageSet(t, cfg, name) {
		out = append(out, strings.SplitN(p, "|", 2)[0])
	}
	return out
}

// F7: renaming a directory under the source root reaches SyncChanged as the
// old directory path (gone) and the new files. The old files' passages must go
// and the new ones must appear.
func TestSyncChangedDirectoryRename(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "m")
	oldDir := filepath.Join(docs, "pkg")
	writeFile(t, filepath.Join(oldDir, "a.go"), "package pkg\n\nfunc A() int { return 1 }\n")
	writeFile(t, filepath.Join(oldDir, "deep", "b.go"), "package deep\n\nfunc B() int { return 2 }\n")
	if _, err := Sync(context.Background(), SyncOptions{Options: env.options(nil), Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if !anyUnder(sourcesOf(t, env.cfg, "m"), "pkg") {
		t.Fatalf("setup: nothing indexed under pkg: %v", sourcesOf(t, env.cfg, "m"))
	}

	newDir := filepath.Join(docs, "renamed")
	if err := os.Rename(oldDir, newDir); err != nil {
		t.Fatal(err)
	}
	// What the watcher records: the old directory, plus the new files.
	batch := []string{oldDir, filepath.Join(newDir, "a.go"), filepath.Join(newDir, "deep", "b.go")}
	res, err := SyncChanged(context.Background(), SyncOptions{Options: env.options(nil), Name: "m", DocsDir: docs}, batch)
	if err != nil {
		t.Fatalf("SyncChanged: %v", err)
	}
	if len(res.Deleted) != 3 {
		t.Errorf("Deleted = %v, want the directory plus its two files", res.Deleted)
	}
	got := sourcesOf(t, env.cfg, "m")
	if anyUnder(got, "pkg") {
		t.Errorf("passages from the old directory remain: %v", got)
	}
	if !anyUnder(got, "renamed") {
		t.Errorf("no passages under the new directory: %v", got)
	}
	m, err := gleann.LoadIndexManifest(env.cfg.IndexDir, "m")
	if err != nil {
		t.Fatal(err)
	}
	for k := range m.Entries {
		if strings.HasPrefix(k, "pkg/") {
			t.Errorf("manifest still lists %s", k)
		}
	}
}

// anyUnder reports whether a source (relative to the docs root) is under dir.
func anyUnder(sources []string, dir string) bool {
	for _, s := range sources {
		if strings.HasPrefix(filepath.ToSlash(s), dir+"/") {
			return true
		}
	}
	return false
}

// manifestOf builds n entries spread over dirs directories, three levels deep.
func manifestOf(n, dirs int) map[string]gleann.ManifestEntry {
	e := make(map[string]gleann.ManifestEntry, n)
	for i := 0; i < n; i++ {
		e[fmt.Sprintf("d%d/sub/f%d.go", i%dirs, i)] = gleann.ManifestEntry{}
	}
	return e
}

// The expansion is one pass over the manifest: the number of entries examined
// does not depend on how many paths were deleted, and the result is exact on a
// path boundary ("d1" must not take "d10").
func TestExpandVanishedFromBoundaryAndSinglePass(t *testing.T) {
	docs := t.TempDir()
	entries := manifestOf(2000, 20)
	var deleted []string
	for _, d := range []string{"d1", "d10"} {
		deleted = append(deleted, filepath.Join(docs, d))
	}
	got := expandVanishedFrom(docs, deleted, entries)
	want := 2 + 100 + 100 // two dirs plus 100 files under each
	if len(got) != want {
		t.Fatalf("expanded to %d paths, want %d", len(got), want)
	}
	only := expandVanishedFrom(docs, []string{filepath.Join(docs, "d1")}, entries)
	for _, p := range only[1:] {
		if !strings.Contains(filepath.ToSlash(p), "/d1/") {
			t.Fatalf("d1 took %s", p)
		}
	}
}

// The expansion is one manifest pass plus one ancestor-map lookup per path
// separator of each entry: the counts are exact, so the test cannot flake on a
// slow runner, and a per-deleted-path rescan (the old quadratic scan) would
// show as entries examined > len(entries).
func TestExpandVanishedFromLinear(t *testing.T) {
	docs := t.TempDir()
	for _, n := range []int{2000, 8000} {
		entries := manifestOf(n, n/10)
		var deleted []string
		for i := 0; i < n/10; i++ {
			deleted = append(deleted, filepath.Join(docs, fmt.Sprintf("d%d", i)))
		}
		maxLookups := 0
		for key := range entries {
			maxLookups += strings.Count(key[1:], "/")
		}
		var st expandStats
		expandVanishedCounted(docs, deleted, entries, &st)
		if st.entries != len(entries) {
			t.Errorf("n=%d: examined %d entries, want exactly %d (one pass)", n, st.entries, len(entries))
		}
		if st.lookups > maxLookups {
			t.Errorf("n=%d: %d ancestor lookups, want at most %d (one per separator)", n, st.lookups, maxLookups)
		}
		if st.stats > len(entries) {
			t.Errorf("n=%d: %d stats for %d entries", n, st.stats, len(entries))
		}
	}
}

func BenchmarkExpandVanishedFrom(b *testing.B) {
	docs := b.TempDir()
	entries := manifestOf(100000, 5000)
	var deleted []string
	for i := 0; i < 5000; i++ {
		deleted = append(deleted, filepath.Join(docs, fmt.Sprintf("d%d", i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		expandVanishedFrom(docs, deleted, entries)
	}
}
