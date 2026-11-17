package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWarnPendingSyncBackups(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs.sync-bak"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "healthy"), 0o755); err != nil {
		t.Fatal(err)
	}
	var got []string
	warnPendingSyncBackups(dir, func(m string) { got = append(got, m) })
	if len(got) != 1 || !strings.Contains(got[0], `"docs"`) || !strings.Contains(got[0], "gleann index sync docs") {
		t.Fatalf("warnings = %q", got)
	}
}
