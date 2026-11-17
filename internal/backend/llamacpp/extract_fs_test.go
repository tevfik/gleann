package llamacpp

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

func fakeAssets() fstest.MapFS {
	return fstest.MapFS{
		"bin/p/llama-server-x": {Data: []byte("server v2")},
		"bin/p/libfoo.so":      {Data: []byte("lib")},
		"bin/p/sub/ignored":    {Data: []byte("nested files are not extracted")},
	}
}

func TestExtractFSWritesFilesAndReturnsMainPath(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nested", "bin")
	got, err := extractFS(fakeAssets(), "bin/p", dest, "llama-server-x")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dest, "llama-server-x"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	for name, want := range map[string]string{"llama-server-x": "server v2", "libfoo.so": "lib"} {
		b, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(b) != want {
			t.Errorf("%s = %q, %v; want %q", name, b, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "sub")); !os.IsNotExist(err) {
		t.Errorf("subdirectory must not be extracted (err=%v)", err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(got)
		if fi.Mode()&0o111 == 0 {
			t.Errorf("extracted server not executable: %v", fi.Mode())
		}
	}
}

func TestExtractFSReplacesChangedAndRemovesStale(t *testing.T) {
	dest := t.TempDir()
	stale := filepath.Join(dest, "libllama.so")
	old := filepath.Join(dest, "llama-server-x")
	if err := os.WriteFile(stale, []byte("old abi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("server v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := extractFS(fakeAssets(), "bin/p", dest, "llama-server-x"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(old); string(b) != "server v2" {
		t.Errorf("changed server not replaced: %q", b)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale library kept (err=%v)", err)
	}
}

func TestExtractFSUnchangedContentIsNotRewritten(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("inode identity is not exposed on Windows")
	}
	dest := t.TempDir()
	if _, err := extractFS(fakeAssets(), "bin/p", dest, "llama-server-x"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dest, "llama-server-x")
	before, _ := os.Stat(p)
	if _, err := extractFS(fakeAssets(), "bin/p", dest, "llama-server-x"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	if !os.SameFile(before, after) {
		t.Error("identical content was rewritten (file replaced)")
	}
}

func TestExtractFSNothingEmbedded(t *testing.T) {
	for name, fsys := range map[string]fs.FS{
		"missing dir": fstest.MapFS{},
		"empty dir":   fstest.MapFS{"bin/p": {Mode: fs.ModeDir}},
	} {
		_, err := extractFS(fsys, "bin/p", t.TempDir(), "x")
		if err == nil || !strings.Contains(err.Error(), "no embedded llama-server") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

type unreadableFS struct{ fstest.MapFS }

func (u unreadableFS) ReadFile(name string) ([]byte, error) { return nil, errors.New("boom") }
func (u unreadableFS) Open(name string) (fs.File, error) {
	if strings.HasSuffix(name, "libfoo.so") || strings.HasSuffix(name, "llama-server-x") {
		return nil, errors.New("boom")
	}
	return u.MapFS.Open(name)
}

func TestExtractFSAssetReadError(t *testing.T) {
	_, err := extractFS(unreadableFS{fakeAssets()}, "bin/p", t.TempDir(), "llama-server-x")
	if err == nil || !strings.Contains(err.Error(), "failed to read embedded asset") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractFSDestNotCreatable(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractFS(fakeAssets(), "bin/p", filepath.Join(blocker, "bin"), "x"); err == nil {
		t.Fatal("expected MkdirAll to fail below a regular file")
	}
}

func TestExtractFSWriteFailureIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions are not enforced the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dest := t.TempDir()
	if err := os.Chmod(dest, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dest, 0o755) })
	_, err := extractFS(fakeAssets(), "bin/p", dest, "llama-server-x")
	if err == nil || !strings.Contains(err.Error(), "failed to extract") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractAllBinariesWithoutHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("os.UserHomeDir reads other variables on this OS")
	}
	t.Setenv("HOME", "")
	if _, err := extractAllBinaries("x"); err == nil {
		t.Fatal("expected an error when $HOME is unset")
	}
}

func TestWriteIfChangedCreatesAndOverwrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := writeIfChanged(p, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := writeIfChanged(p, []byte("two")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, []byte("two")) {
		t.Errorf("content = %q", b)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("temp files leaked: %v", entries)
	}
	if err := writeIfChanged(filepath.Join(filepath.Dir(p), "no", "such", "dir", "f"), []byte("x")); err == nil {
		t.Error("expected an error for a missing directory")
	}
}
