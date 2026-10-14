package gleann

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"
)

func writeManifestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sortedKeys(m map[string]ManifestEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestManifest_RelativeSourceDirKeepsSubdirectoryKeys(t *testing.T) {
	// Regression: `gleann index build x --docs ./src` passes a relative docs
	// dir and relative file paths. filepath.Rel(absSource, relFile) failed and
	// every entry collapsed to its basename, so a/x.go and b/x.go collided and
	// the first sync saw every file as added/deleted.
	root := t.TempDir()
	writeManifestFile(t, filepath.Join(root, "src", "a", "x.go"), "package a")
	writeManifestFile(t, filepath.Join(root, "src", "b", "x.go"), "package b")
	t.Chdir(root)

	m, err := BuildManifest("idx", "src", []string{
		filepath.Join("src", "a", "x.go"),
		filepath.Join("src", "b", "x.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sortedKeys(m.Entries), []string{"a/x.go", "b/x.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if !filepath.IsAbs(m.SourceDir) {
		t.Fatalf("SourceDir should be absolute, got %q", m.SourceDir)
	}

	// The sync path uses absolute paths; it must see no changes.
	absSrc := filepath.Join(root, "src")
	diff, _, err := QuickDiffDirectory(m, absSrc, []string{
		filepath.Join(absSrc, "a", "x.go"),
		filepath.Join(absSrc, "b", "x.go"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !diff.IsEmpty() {
		t.Fatalf("relative build vs absolute sync should be identical, diff=%+v", diff)
	}
}

func TestManifest_QuickDiffScenarios(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "sub", "b.md")
	c := filepath.Join(dir, "c.txt")
	writeManifestFile(t, a, "package a")
	writeManifestFile(t, b, "# B")
	writeManifestFile(t, c, "c")
	base, err := BuildManifest("idx", dir, []string{a, b, c})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("unchanged", func(t *testing.T) {
		diff, nm, err := QuickDiffDirectory(base, dir, []string{c, b, a}) // order must not matter
		if err != nil || !diff.IsEmpty() {
			t.Fatalf("diff=%+v err=%v", diff, err)
		}
		if nm.RootHash != base.RootHash {
			t.Fatalf("root hash changed for unchanged tree")
		}
		if nm.IndexName != "idx" {
			t.Fatalf("index name not carried over: %q", nm.IndexName)
		}
	})

	t.Run("rename is delete plus add", func(t *testing.T) {
		renamed := filepath.Join(dir, "sub", "renamed.md")
		writeManifestFile(t, renamed, "# B")
		diff, _, _ := QuickDiffDirectory(base, dir, []string{a, renamed, c})
		want := ManifestDiff{Added: []string{"sub/renamed.md"}, Deleted: []string{"sub/b.md"}}
		if !reflect.DeepEqual(diff.Added, want.Added) || !reflect.DeepEqual(diff.Deleted, want.Deleted) || len(diff.Modified) != 0 {
			t.Fatalf("diff=%+v, want %+v", diff, want)
		}
	})

	t.Run("deleted file in eligible list is ignored and reported deleted", func(t *testing.T) {
		gone := filepath.Join(dir, "gone.go")
		diff, nm, _ := QuickDiffDirectory(base, dir, []string{a, b, gone})
		if !reflect.DeepEqual(diff.Deleted, []string{"c.txt"}) || len(diff.Added) != 0 {
			t.Fatalf("diff=%+v", diff)
		}
		if _, ok := nm.Entries["gone.go"]; ok {
			t.Fatalf("missing file must not get a manifest entry")
		}
	})

	t.Run("directories and empty dirs are skipped", func(t *testing.T) {
		empty := filepath.Join(dir, "emptydir")
		if err := os.MkdirAll(empty, 0o755); err != nil {
			t.Fatal(err)
		}
		diff, _, _ := QuickDiffDirectory(base, dir, []string{a, b, c, empty, filepath.Join(dir, "sub")})
		if !diff.IsEmpty() {
			t.Fatalf("directories must not appear in diff: %+v", diff)
		}
	})

	t.Run("touch without content change is not a modification", func(t *testing.T) {
		future := time.Now().Add(2 * time.Hour)
		if err := os.Chtimes(a, future, future); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			fi := base.Entries["a.go"]
			ts := time.Unix(0, fi.ModTime)
			_ = os.Chtimes(a, ts, ts)
		})
		diff, nm, _ := QuickDiffDirectory(base, dir, []string{a, b, c})
		if !diff.IsEmpty() {
			t.Fatalf("touch-only change should not be modified: %+v", diff)
		}
		// Root hash covers mtimes, so it changes even though content did not.
		if nm.RootHash == base.RootHash {
			t.Fatalf("expected root hash to reflect the new mtime")
		}
		if nm.Entries["a.go"].Hash != base.Entries["a.go"].Hash {
			t.Fatalf("content hash must be stable across touch")
		}
	})

	t.Run("content change with preserved size and mtime is not detected (fast-path contract)", func(t *testing.T) {
		mt := time.Unix(0, base.Entries["c.txt"].ModTime)
		writeManifestFile(t, c, "C") // same size
		if err := os.Chtimes(c, mt, mt); err != nil {
			t.Fatal(err)
		}
		diff, _, _ := QuickDiffDirectory(base, dir, []string{a, b, c})
		if !diff.IsEmpty() {
			t.Fatalf("stat fast path should reuse the old entry, got %+v", diff)
		}
		// A full BuildManifest re-hashes and does see it.
		full, _ := BuildManifest("idx", dir, []string{a, b, c})
		if d := base.Diff(full); !reflect.DeepEqual(d.Modified, []string{"c.txt"}) {
			t.Fatalf("full rebuild should detect modification, got %+v", d)
		}
	})
}

func TestManifest_SymlinkedFileIsHashedByTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.go")
	link := filepath.Join(dir, "link.go")
	writeManifestFile(t, target, "package real")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	m, err := BuildManifest("idx", dir, []string{target, link})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 2 || m.Entries["link.go"].Hash != m.Entries["real.go"].Hash {
		t.Fatalf("symlink should be hashed by target content: %+v", m.Entries)
	}
	writeManifestFile(t, target, "package real // changed")
	diff, _, _ := QuickDiffDirectory(m, dir, []string{target, link})
	if !reflect.DeepEqual(diff.Modified, []string{"link.go", "real.go"}) {
		t.Fatalf("changing the target must mark both paths modified, got %+v", diff)
	}
}

func TestManifest_SaveLoadPreservesRootHash(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.go")
	writeManifestFile(t, f, "package x")
	m, _ := BuildManifest("idx", dir, []string{f})
	p := filepath.Join(dir, "nested", "idx.manifest.json")
	if err := SaveManifest(p, m); err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.RootHash != m.RootHash || ComputeRootHash(got.Entries) != m.RootHash {
		t.Fatalf("root hash not stable through save/load")
	}
	if _, err := LoadManifest(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected error for missing manifest")
	}
	writeManifestFile(t, p, "{not json")
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("expected error for corrupt manifest")
	}
}

func TestComputeRootHash_EmptyAndSensitivity(t *testing.T) {
	empty := ComputeRootHash(nil)
	if empty != ComputeRootHash(map[string]ManifestEntry{}) {
		t.Fatal("nil and empty maps must hash identically")
	}
	base := map[string]ManifestEntry{"a": {Path: "a", Size: 1, ModTime: 2, Hash: "h"}}
	h0 := ComputeRootHash(base)
	mutations := []ManifestEntry{
		{Path: "b", Size: 1, ModTime: 2, Hash: "h"},
		{Path: "a", Size: 9, ModTime: 2, Hash: "h"},
		{Path: "a", Size: 1, ModTime: 9, Hash: "h"},
		{Path: "a", Size: 1, ModTime: 2, Hash: "x"},
	}
	for i, m := range mutations {
		if ComputeRootHash(map[string]ManifestEntry{"a": m}) == h0 {
			t.Errorf("mutation %d did not change root hash", i)
		}
	}
}

// FuzzManifestDiff checks Diff set semantics and root-hash determinism over
// arbitrary manifests derived from fuzz bytes.
func FuzzManifestDiff(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3}, []byte{2, 3, 4, 5})
	f.Add([]byte{}, []byte{1})
	f.Add([]byte{7, 7, 7}, []byte{})
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8}, []byte{8, 7, 6, 5, 4, 3, 2, 1})
	f.Fuzz(func(t *testing.T, a, b []byte) {
		mk := func(bs []byte) *Manifest {
			e := map[string]ManifestEntry{}
			for i, x := range bs {
				p := fmt.Sprintf("d%d/f%d.go", x%4, x%16)
				e[p] = ManifestEntry{Path: p, Size: int64(x), ModTime: int64(i), Hash: fmt.Sprintf("%x", x/16)}
			}
			return &Manifest{Entries: e, RootHash: ComputeRootHash(e)}
		}
		ma, mb := mk(a), mk(b)
		d := ma.Diff(mb)

		for _, s := range [][]string{d.Added, d.Modified, d.Deleted} {
			if !sort.StringsAreSorted(s) {
				t.Fatalf("diff lists must be sorted: %+v", d)
			}
		}
		seen := map[string]string{}
		for kind, s := range map[string][]string{"added": d.Added, "modified": d.Modified, "deleted": d.Deleted} {
			for _, p := range s {
				if prev, ok := seen[p]; ok {
					t.Fatalf("%s appears in both %s and %s", p, prev, kind)
				}
				seen[p] = kind
			}
		}
		for p, ea := range ma.Entries {
			eb, ok := mb.Entries[p]
			switch {
			case !ok && seen[p] != "deleted":
				t.Fatalf("%s missing from new but not deleted", p)
			case ok && ea.Hash != eb.Hash && seen[p] != "modified":
				t.Fatalf("%s hash changed but not modified", p)
			case ok && ea.Hash == eb.Hash && seen[p] != "":
				t.Fatalf("%s unchanged but reported %s", p, seen[p])
			}
		}
		for p := range mb.Entries {
			if _, ok := ma.Entries[p]; !ok && seen[p] != "added" {
				t.Fatalf("%s new but not added", p)
			}
		}
		// Applying the diff to A yields B's key set.
		keys := map[string]bool{}
		for p := range ma.Entries {
			keys[p] = true
		}
		for _, p := range d.Deleted {
			delete(keys, p)
		}
		for _, p := range d.Added {
			keys[p] = true
		}
		if len(keys) != len(mb.Entries) {
			t.Fatalf("applying diff gives %d keys, want %d", len(keys), len(mb.Entries))
		}
		// Self-diff is empty and nil handling is symmetric.
		if sd := ma.Diff(ma); !sd.IsEmpty() {
			t.Fatalf("self diff not empty: %+v", sd)
		}
		var nilM *Manifest
		if nd := nilM.Diff(mb); len(nd.Added) != len(mb.Entries) {
			t.Fatalf("nil.Diff(b) should add everything")
		}
		if nd := ma.Diff(nil); len(nd.Deleted) != len(ma.Entries) {
			t.Fatalf("a.Diff(nil) should delete everything")
		}
		// Root hash depends only on entries; equal entry sets => equal hash.
		if reflect.DeepEqual(ma.Entries, mb.Entries) != (ma.RootHash == mb.RootHash) {
			t.Fatalf("root hash equality disagrees with entry equality")
		}
	})
}
