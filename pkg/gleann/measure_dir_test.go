package gleann

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeSized(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMeasureDir(t *testing.T) {
	dir := t.TempDir()
	writeSized(t, filepath.Join(dir, "config.json"), 10)
	writeSized(t, filepath.Join(dir, "models", "a", "m.bin"), 1000)
	writeSized(t, filepath.Join(dir, "models", "b.bin"), 500)
	writeSized(t, filepath.Join(dir, "indexes", "x", "x.db"), 200)
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink to a big file elsewhere must not be counted.
	outside := filepath.Join(t.TempDir(), "big")
	writeSized(t, outside, 5000)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Logf("symlinks unavailable: %v", err)
	}

	u, err := MeasureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if u.Bytes != 1710 {
		t.Errorf("Bytes = %d, want 1710", u.Bytes)
	}
	want := map[string]int64{"config.json": 10, "models": 1500, "indexes": 200}
	if len(u.Folders) != len(want) {
		t.Fatalf("Folders = %v, want %v", u.Folders, want)
	}
	for k, v := range want {
		if u.Folders[k] != v {
			t.Errorf("Folders[%q] = %d, want %d", k, u.Folders[k], v)
		}
	}
}

func TestMeasureDir_MissingIsEmpty(t *testing.T) {
	u, err := MeasureDir(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Bytes != 0 || len(u.Folders) != 0 || u.Folders == nil {
		t.Fatalf("got %+v", u)
	}
}

func TestMeasureDir_SymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	writeSized(t, filepath.Join(real, "models", "m.bin"), 300)
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	u, err := MeasureDir(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Bytes != 300 || u.Folders["models"] != 300 {
		t.Fatalf("got %+v", u)
	}
}

func TestMeasureDir_SkipsUnreadable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits not enforced here")
	}
	dir := t.TempDir()
	writeSized(t, filepath.Join(dir, "ok", "f"), 40)
	writeSized(t, filepath.Join(dir, "locked", "f"), 99)
	if err := os.Chmod(filepath.Join(dir, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o755) }()
	u, err := MeasureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if u.Bytes != 40 {
		t.Fatalf("Bytes = %d, want 40", u.Bytes)
	}
}
