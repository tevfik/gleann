package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// R1: a symlinked subdirectory inside the export dir must not let a prune
// delete a file outside it.
func TestExportPruneNeverFollowsSymlinks(t *testing.T) {
	s, _ := seedExportBlocks(t)
	dir := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim-deadbeef.md")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "summaries")); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName),
		[]byte(`{"version":1,"files":{"all":["summaries/victim-deadbeef.md"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// MkdirAll tolerates the symlinked dir; the run must still not delete.
	_, _ = s.ExportWithOptions(dir, ExportOptions{})
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("file outside the export dir was deleted: %v", err)
	}
}

// R1: a manifest entry that is not a generated file name is never deleted.
func TestExportPruneIgnoresForeignManifestNames(t *testing.T) {
	s, _ := seedExportBlocks(t)
	dir := t.TempDir()
	foreign := filepath.Join(dir, "important.txt")
	notesMD := filepath.Join(dir, "notes.md")
	for _, f := range []string{foreign, notesMD} {
		if err := os.WriteFile(f, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName),
		[]byte(`{"version":1,"files":{"all":["important.txt","notes.md"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := s.ExportWithOptions(dir, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0", st.Pruned)
	}
	for _, f := range []string{foreign, notesMD} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s deleted: %v", f, err)
		}
	}
}

// R2: a symlinked path and the real path are one scope.
func TestDetectRepoScopeResolvesSymlinks(t *testing.T) {
	real := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable: ", err)
	}
	if a, b := DetectRepoScopeAt(real), DetectRepoScopeAt(link); a != b {
		t.Fatalf("real %q != symlink %q", a, b)
	}
}

// R3: importing a pre-v1.22 backup into a fresh store makes its bare-scope
// blocks visible from the matching directory, and only from it.
func TestImportRegistersLegacyAlias(t *testing.T) {
	src, err := OpenStore(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, b := range []Block{
		{ID: "bk-app", Tier: TierLong, Label: "fact", Content: "zz app fact", Scope: "app"},
		{ID: "bk-other", Tier: TierLong, Label: "fact", Content: "zz other fact", Scope: "other"},
	} {
		b := b
		if err := src.Add(&b); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if _, err := src.Export(dir); err != nil {
		t.Fatal(err)
	}

	dst, err := OpenStore(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(dst)
	const cur = "dir:app-1a2b3c4d"
	if _, err := dst.ImportWithOptions(dir, ImportOptions{CurrentScope: cur}); err != nil {
		t.Fatal(err)
	}
	got, _ := m.ListScoped(cur, TierLong)
	if len(got) != 1 || got[0].ID != "bk-app" {
		t.Fatalf("ListScoped(%q) = %v, want only bk-app", cur, got)
	}
	// A different folder name is not aliased to "app".
	got, _ = m.ListScoped("dir:zzz-1a2b3c4d", TierLong)
	if len(got) != 0 {
		t.Fatalf("unrelated dir sees %v", got)
	}
	// The alias survives a reopen.
	path := dst.path
	dst.Close()
	re, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer re.Close()
	got, _ = NewManager(re).ListScoped(cur, TierLong)
	if len(got) != 1 {
		t.Fatalf("after reopen: %v", got)
	}
}
