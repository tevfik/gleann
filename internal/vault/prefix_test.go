package vault

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRemovePathsWithPrefix(t *testing.T) {
	tr, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	ctx := context.Background()
	base := t.TempDir()
	up1 := filepath.Join(base, "gleann_upload_1", "a.pdf")
	up2 := filepath.Join(base, "gleann_upload_2", "b.pdf")
	up3 := filepath.Join(base, "gleann_upload_3", "shared.pdf")
	real := filepath.Join(base, "docs", "shared.pdf")
	other := filepath.Join(base, "docs", "gleann_uploaded.txt")
	for _, r := range []struct{ hash, path string }{
		{"h1", up1}, {"h2", up2}, {"h3", up3}, {"h3", real}, {"h4", other},
	} {
		if err := tr.UpsertRecord(ctx, r.hash, r.path, 0, 1); err != nil {
			t.Fatal(err)
		}
	}

	n, err := tr.RemovePathsWithPrefix(ctx, filepath.Join(base, "gleann_upload_"))
	if err != nil || n != 3 {
		t.Fatalf("removed %d, err %v", n, err)
	}
	for _, p := range []string{up1, up2, up3} {
		if rec, _ := tr.GetRecordByPath(ctx, p); rec != nil {
			t.Errorf("%s still tracked", p)
		}
	}
	for _, p := range []string{real, other} {
		if rec, _ := tr.GetRecordByPath(ctx, p); rec == nil {
			t.Errorf("%s lost its record", p)
		}
	}
	if _, err := tr.GetPathByHash(ctx, "h1"); err == nil {
		t.Error("hash record of an upload path was kept")
	}
	// h3's file record points at the real file (written last): it stays.
	if _, err := tr.GetPathByHash(ctx, "h3"); err != nil {
		t.Errorf("hash shared with a real file was removed: %v", err)
	}
	if _, err := tr.RemovePathsWithPrefix(ctx, " "); err == nil {
		t.Error("empty prefix must be refused")
	}
}

// The temp directory may be stored resolved while the prefix goes through a symlink.
func TestRemovePathsWithPrefix_SymlinkedDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "tmp")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	tr, err := NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	ctx := context.Background()
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.UpsertRecord(ctx, "h", filepath.Join(resolved, "gleann_upload_9", "x.md"), 0, 1); err != nil {
		t.Fatal(err)
	}
	n, err := tr.RemovePathsWithPrefix(ctx, filepath.Join(link, "gleann_upload_"))
	if err != nil || n != 1 {
		t.Fatalf("removed %d, err %v", n, err)
	}
}

func TestNormPathCase(t *testing.T) {
	same := normPath(`/Tmp/Gleann_Upload_1`) == normPath(`/tmp/gleann_upload_1`)
	if runtime.GOOS == "windows" && !same {
		t.Error("paths must compare case-insensitively on Windows")
	}
	if runtime.GOOS != "windows" && same {
		t.Error("paths must stay case-sensitive here")
	}
}
