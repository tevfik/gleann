package pathpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestContain(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data", "a")
	sibling := filepath.Join(base, "data", "ab")
	outside := filepath.Join(base, "other")
	for _, d := range []string{data, sibling, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "ok.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := []string{data}

	tests := []struct {
		name string
		path string
		ok   bool
	}{
		{"root itself", data, true},
		{"file below root", filepath.Join(data, "ok.md"), true},
		{"not yet existing below root", filepath.Join(data, "new", "x.md"), true},
		{"dotdot inside root", filepath.Join(data, "sub", "..", "ok.md"), true},
		{"dotdot escape", filepath.Join(data, "..", "..", "other", "secret"), false},
		{"absolute outside", filepath.Join(outside, "secret"), false},
		{"sibling with common prefix", sibling, false},
		{"parent of root", filepath.Dir(data), false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Contain(tc.path, roots)
			if tc.ok && err != nil {
				t.Fatalf("want allowed, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("want refused for %q", tc.path)
			}
		})
	}

	if _, err := Contain(outside, nil); !errors.Is(err, ErrOutside) {
		t.Fatalf("no roots must refuse, got %v", err)
	}
}

func TestContainSymlinkEscape(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, p := range []string{link, filepath.Join(link, "secret"), filepath.Join(link, "missing", "x")} {
		if _, err := Contain(p, []string{root}); err == nil {
			t.Errorf("symlink escape %q was allowed", p)
		}
	}
	// A symlinked root still works.
	rootLink := filepath.Join(base, "rootlink")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Contain(filepath.Join(root, "f"), []string{rootLink}); err != nil {
		t.Errorf("symlinked root refused: %v", err)
	}
}

func TestWithinWindowsCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	if !Within(`C:\Data\A`, `c:\data\a\x`) {
		t.Error("case must not matter on windows")
	}
	if Within(`C:\Data`, `D:\Data\x`) {
		t.Error("different volume must be refused")
	}
}

func TestDefaultRoots(t *testing.T) {
	if got := DefaultRoots([]string{"/a", " "}); len(got) != 1 || got[0] != "/a" {
		t.Errorf("configured roots: %v", got)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got := DefaultRoots(nil)
	if len(got) != 1 || !strings.EqualFold(got[0], home) {
		t.Errorf("default root %v, want %s", got, home)
	}
}
