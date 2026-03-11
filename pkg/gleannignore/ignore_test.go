package gleannignore

import (
	"os"
	"path/filepath"
	"testing"
)

func writeIgnoreFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".gleannignore"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadNoFile(t *testing.T) {
	m := Load(t.TempDir())
	if m == nil {
		t.Fatal("expected non-nil matcher when no file")
	}
	if m.Match("foo.go", false) {
		t.Error("empty matcher should match nothing")
	}
}

func TestBasicPatterns(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, `
# comment
*.log
build/
!important.log
`)

	m := Load(dir)

	tests := []struct {
		path   string
		isDir  bool
		expect bool
	}{
		{"app.log", false, true},        // matches *.log (not negated — !important.log only negates important.log)
		{"server.log", false, true},     // matches *.log
		{"important.log", false, false}, // negated by !important.log
		{"build", true, true},           // matches build/
		{"build", false, false},         // build/ is dir-only pattern
		{"src/main.go", false, false},   // no match
		{"src/debug.log", false, true},  // matches *.log
	}

	for _, tt := range tests {
		got := m.Match(tt.path, tt.isDir)
		if got != tt.expect {
			t.Errorf("Match(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
		}
	}
}

func TestDirectoryPattern(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "vendor/\nnode_modules/\n")

	m := Load(dir)

	if !m.Match("vendor", true) {
		t.Error("vendor dir should be ignored")
	}
	if m.Match("vendor", false) {
		t.Error("vendor file should not be ignored (dir-only pattern)")
	}
	if !m.Match("node_modules", true) {
		t.Error("node_modules dir should be ignored")
	}
}

func TestWildcardPath(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "docs/**/*.pdf\n*.tmp\n")

	m := Load(dir)

	tests := []struct {
		path   string
		isDir  bool
		expect bool
	}{
		{"docs/guide/manual.pdf", false, true},
		{"docs/manual.pdf", false, true},
		{"src/manual.pdf", false, false},
		{"temp.tmp", false, true},
		{"src/temp.tmp", false, true},
	}

	for _, tt := range tests {
		got := m.Match(tt.path, tt.isDir)
		if got != tt.expect {
			t.Errorf("Match(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.expect)
		}
	}
}

func TestHasFile(t *testing.T) {
	dir := t.TempDir()
	if HasFile(dir) {
		t.Error("expected HasFile=false when no .gleannignore")
	}

	writeIgnoreFile(t, dir, "*.log\n")
	if !HasFile(dir) {
		t.Error("expected HasFile=true when .gleannignore exists")
	}
}

func TestEmptyLines(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "\n\n# just comments\n\n")

	m := Load(dir)
	if m.Match("anything.go", false) {
		t.Error("empty pattern set should match nothing")
	}
}

func TestNegation(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreFile(t, dir, "*.txt\n!README.txt\n")

	m := Load(dir)
	if !m.Match("notes.txt", false) {
		t.Error("notes.txt should be ignored")
	}
	if m.Match("README.txt", false) {
		t.Error("README.txt should NOT be ignored (negated)")
	}
}
