package gleann

import (
	"os"
	"path/filepath"
	"testing"
)

// Directories and files that vanished between the walk and the manifest are
// skipped, and a nil-to-nil diff is empty.
func TestManifestSkipsDirectoriesAndVanishedFiles(t *testing.T) {
	src := t.TempDir()
	keep := filepath.Join(src, "keep.go")
	if err := os.WriteFile(keep, []byte("package keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(src, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(src, "gone.go")
	files := []string{keep, sub, gone}

	m, err := BuildManifest("idx", src, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Entries) != 1 || m.Entries["keep.go"].Hash == "" {
		t.Fatalf("entries = %+v", m.Entries)
	}
	diff, next, err := QuickDiffDirectory(m, src, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Entries) != 1 || len(diff.Added)+len(diff.Modified)+len(diff.Deleted) != 0 {
		t.Fatalf("diff of an unchanged tree = %+v", diff)
	}
	if _, err := HashFileContent(gone); err == nil {
		t.Fatal("hashed a missing file")
	}
	var nilManifest *Manifest
	if d := nilManifest.Diff(nil); len(d.Added)+len(d.Modified)+len(d.Deleted) != 0 {
		t.Fatalf("nil diff = %+v", d)
	}

	// SaveManifest refuses a path below a regular file.
	if err := SaveManifest(filepath.Join(keep, "m.json"), m); err == nil {
		t.Fatal("saved a manifest below a file")
	}
}
