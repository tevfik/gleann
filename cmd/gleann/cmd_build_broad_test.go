package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBroadDocsTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows
	project := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.VolumeName(home) + string(filepath.Separator)
	for dir, want := range map[string]string{
		home:                            "your home directory",
		home + string(os.PathSeparator): "your home directory",
		root:                            "the file system root",
		project:                         "",
		filepath.Join(home, "work"):     "",
	} {
		if got := broadDocsTarget(dir); got != want {
			t.Errorf("broadDocsTarget(%q) = %q, want %q", dir, got, want)
		}
	}
}

// TestBuildRefusesHomeDirectory: `gleann index build x --docs .` typed in the
// home directory used to start reading every file below it. It now stops
// before any work, and --yes still allows it (shown here with --dry-run, which
// only estimates).
func TestBuildRefusesHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := captureRun(t, "", func() { cmdBuild([]string{"homeidx", "--docs", home, "--index-dir", t.TempDir()}) })
	if res.Code != 2 || !strings.Contains(res.Err, "your home directory") || !strings.Contains(res.Err, "--yes") {
		t.Fatalf("exit %d, stderr %q; want exit 2 naming the home directory and --yes", res.Code, res.Err)
	}

	res = captureRun(t, "", func() { cmdBuild([]string{"homeidx", "--docs", home, "--dry-run", "--index-dir", t.TempDir()}) })
	if res.Code != 0 || !strings.Contains(res.Out, "Files: 1") {
		t.Fatalf("--dry-run must still estimate: exit %d, out %q, err %q", res.Code, res.Out, res.Err)
	}
}
