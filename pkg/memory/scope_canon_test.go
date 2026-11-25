package memory

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// pathHashKey folds case and separators per OS; goos is injected so every rule
// runs on every host.
func TestPathHashKey(t *testing.T) {
	cases := []struct {
		name, goos, in, want string
	}{
		{"windows folds case", "windows", `C:\Users\Runner\Proj`, "c:/users/runner/proj"},
		{"windows case variants agree", "windows", `c:\USERS\runner\PROJ`, "c:/users/runner/proj"},
		{"windows slash style", "windows", "C:/Users/Runner/Proj", "c:/users/runner/proj"},
		{"windows trailing sep", "windows", `C:\Users\X\`, "c:/users/x"},
		{"linux keeps case", "linux", "/Home/User/Proj", "/Home/User/Proj"},
		{"darwin keeps case", "darwin", "/Users/Me/Proj", "/Users/Me/Proj"},
		{"linux keeps backslash", "linux", `/tmp/a\b`, `/tmp/a\b`},
		{"linux cleans", "linux", "/tmp//a/./b/", "/tmp/a/b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathHashKey(tc.goos, tc.in); got != tc.want {
				t.Errorf("pathHashKey(%q, %q) = %q, want %q", tc.goos, tc.in, got, tc.want)
			}
		})
	}
	if pathHashKey("windows", `C:\Users\X`) != pathHashKey("windows", `c:\users\x`) {
		t.Error("windows case variants hash differently")
	}
	if pathHashKey("linux", "/tmp/A") == pathHashKey("linux", "/tmp/a") {
		t.Error("linux must not fold case")
	}
}

// canonicalDir resolves symlinks and relative spellings to one string.
func TestCanonicalDir(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "proj")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := canonicalDir(real)
	if got := canonicalDir(filepath.Join(real, "sub", "..")); got != want {
		t.Errorf("dotdot spelling = %q, want %q", got, want)
	}
	if got := canonicalDir(real + string(filepath.Separator)); got != want {
		t.Errorf("trailing separator = %q, want %q", got, want)
	}
	// A missing path keeps its absolute, cleaned form.
	missing := filepath.Join(root, "nope", "x")
	if got := canonicalDir(missing); got == "" || !filepath.IsAbs(got) {
		t.Errorf("missing path = %q, want absolute", got)
	}
}

// Windows: upper/lower-case spellings of one directory share a scope.
func TestDetectRepoScopeWindowsCaseInsensitive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only: NTFS is case-insensitive")
	}
	dir := filepath.Join(t.TempDir(), "CaseProj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(filepath.Dir(dir), "CASEPROJ")
	lower := filepath.Join(filepath.Dir(dir), "caseproj")
	a, b := DetectRepoScopeAt(upper), DetectRepoScopeAt(lower)
	if keyHashOf(a) != keyHashOf(b) {
		t.Errorf("case variants hash differently: %q vs %q", a, b)
	}
}

// keyHashOf returns the "-<8hex>" hash suffix of a prefixed scope.
func keyHashOf(scope string) string {
	if len(scope) < 8 {
		return scope
	}
	return scope[len(scope)-8:]
}
