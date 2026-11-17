package pathpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveRejectsNUL(t *testing.T) {
	if _, err := Resolve("a\x00b"); err == nil {
		t.Fatal("a path with a NUL byte must be refused")
	}
}

func TestResolveDanglingSymlink(t *testing.T) {
	base := t.TempDir()
	link := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "nowhere"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Lstat sees the link, EvalSymlinks cannot follow it: the path must be
	// refused rather than resolved to the link itself.
	if _, err := Resolve(filepath.Join(link, "child")); err == nil {
		t.Fatal("a path below a dangling symlink must not resolve")
	}
	if _, err := Contain(link, []string{base}); err == nil {
		t.Fatal("Contain must propagate the resolve error")
	}
}

func TestResolveRelativeWithoutWorkingDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the working directory cannot be removed on windows")
	}
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("PWD", "")
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove the working directory: %v", err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("this OS still reports the removed working directory (darwin)")
	}
	if _, err := Resolve("relative"); err == nil {
		t.Fatal("a relative path without a working directory must fail")
	}
}

func TestWithinRelativePath(t *testing.T) {
	root := t.TempDir()
	// filepath.Rel fails when one side is relative: the path is refused.
	if Within(root, "relative/path") {
		t.Fatal("a relative path is never within an absolute root")
	}
}

func TestContainSkipsUnusableRoots(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "f")
	// Empty and unresolvable roots are skipped; the good one still matches.
	got, err := Contain(file, []string{"", "bad\x00root", root})
	if err != nil {
		t.Fatalf("Contain: %v", err)
	}
	if filepath.Base(got) != "f" {
		t.Fatalf("resolved %q", got)
	}
	// With only unusable roots nothing is allowed.
	if _, err := Contain(file, []string{"", "bad\x00root"}); !errors.Is(err, ErrOutside) {
		t.Fatalf("want ErrOutside, got %v", err)
	}
}

func TestDefaultRootsWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "") // plan9
	if got := DefaultRoots(nil); got != nil {
		t.Fatalf("no home directory must give no roots, got %v", got)
	}
}
