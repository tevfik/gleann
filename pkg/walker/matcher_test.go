package walker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchDoublestar(t *testing.T) {
	tests := []struct {
		pat      string
		path     string
		expected bool
	}{
		// Multi-doublestar fallback
		{"foo**bar**baz", "testfoobarbaz123", true},
		{"foo**bar**baz", "otherpath", false},

		// Standard doublestar
		{"src/**", "src/a/b/c.go", true},
		{"src/**", "other/a/b/c.go", false},
		{"**/test/*.go", "src/test/unit.go", true},
		{"**/test/*.go", "test/unit.go", true},
		{"**/test/*.go", "src/test/unit.txt", false},
		{"src/**/foo.go", "src/a/b/foo.go", true},
		{"src/**/foo.go", "src/foo.go", true},
		{"src/**/foo.go", "other/foo.go", false},
		{"src/**/foo.go", "src/a/bar.go", false},
	}

	for _, tc := range tests {
		got := matchDoublestar(tc.pat, tc.path)
		if got != tc.expected {
			t.Errorf("matchDoublestar(%q, %q) = %v; want %v", tc.pat, tc.path, got, tc.expected)
		}
	}
}

func TestMatchScopedPattern(t *testing.T) {
	// 1. Scoped to a subdirectory
	pScope := scopedPattern{
		glob:     "*.tmp",
		scopeDir: "sub/dir",
	}

	// Outside scope
	if matchScopedPattern(pScope, "other/dir/test.tmp", false) {
		t.Errorf("expected out-of-scope path to not match")
	}

	// Inside scope
	if !matchScopedPattern(pScope, "sub/dir/test.tmp", false) {
		t.Errorf("expected in-scope path to match")
	}

	// Exactly the scope directory itself
	pDir := scopedPattern{
		glob:     "dir",
		scopeDir: "sub/dir",
	}
	if !matchScopedPattern(pDir, "sub/dir", true) {
		t.Errorf("expected scope dir itself to match")
	}

	// Slash in pattern (anchored)
	pSlash := scopedPattern{
		glob:     "/build/*.o",
		scopeDir: "",
	}
	if !matchScopedPattern(pSlash, "build/main.o", false) {
		t.Errorf("expected anchored pattern to match")
	}
	if matchScopedPattern(pSlash, "other/build/main.o", false) {
		t.Errorf("did not expect anchored pattern to match nested dir")
	}

	// Directory match with trailing slash check
	pDirSlash := scopedPattern{
		glob:     "build/bin/",
		scopeDir: "",
	}
	if !matchScopedPattern(pDirSlash, "build/bin", true) {
		t.Errorf("expected dir to match pattern with trailing check")
	}
}

func TestMatcher_IgnoreAndNegation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gleann-matcher-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	subDir := filepath.Join(tmpDir, "pkg", "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	gitignoreContent := `
# Comment line
   # Indented comment

*.log
!important.log
build/
`
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewMatcher(tmpDir, false, []string{".gitignore"})

	// Verify ignored
	if !m.ShouldIgnore(filepath.Join(tmpDir, "app.log"), false) {
		t.Errorf("app.log should be ignored")
	}

	// Verify negated
	if m.ShouldIgnore(filepath.Join(tmpDir, "important.log"), false) {
		t.Errorf("important.log should NOT be ignored (negated)")
	}

	// Directory pattern
	if !m.ShouldIgnore(filepath.Join(tmpDir, "build"), true) {
		t.Errorf("build dir should be ignored")
	}

	// File with same name as dir pattern should not be ignored if isDir=false
	if m.ShouldIgnore(filepath.Join(tmpDir, "build"), false) {
		t.Errorf("build regular file should not be ignored when pattern ends with /")
	}

	// EnsureDirIgnoresLoaded on clean subdir
	m.EnsureDirIgnoresLoaded(subDir)
}
