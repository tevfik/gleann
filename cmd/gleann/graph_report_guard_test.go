//go:build treesitter

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExistingReportIndex(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := existingReportIndex(filepath.Join(dir, "missing.md")); got != "" {
		t.Errorf("missing file: got %q", got)
	}
	if got := existingReportIndex(write("a.md", "# Graph Report: code\n\nGenerated: x\n")); got != "code" {
		t.Errorf("got %q, want code", got)
	}
	if got := existingReportIndex(write("b.md", "# My own notes\n")); got != "" {
		t.Errorf("foreign file must not look like a report, got %q", got)
	}
	if p := absPath("x.md"); !filepath.IsAbs(p) {
		t.Errorf("absPath not absolute: %q", p)
	}
}
