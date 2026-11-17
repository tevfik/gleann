package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestCmdMemoryFailureAddDedupsAndLists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(tmpDir, "mem"))
	t.Setenv("GLEANN_REMOTE_ADDR", "off")

	cmdMemory([]string{"failure", "add",
		"--command", "go test ./pkg/memory",
		"--error", "panic: nil pointer at /a/b/block.go:42",
		"--fix", "nil check",
		"--path", "pkg/memory/block.go"})
	// The same failure, differing only in the line number, must reinforce
	// the existing block rather than add a second one.
	cmdMemory([]string{"failure", "add",
		"--command", "go test ./pkg/memory",
		"--error", "panic: nil pointer at /a/b/block.go:99",
		"--fix", "nil check"})

	mgr, err := memory.DefaultManager()
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	failures, err := mgr.ListFailures()
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 {
		t.Fatalf("got %d failure blocks, want 1", len(failures))
	}
	if failures[0].Confirms != 2 {
		t.Fatalf("Confirms = %d, want 2", failures[0].Confirms)
	}
	if failures[0].Metadata[memory.FailureMetaFix] != "nil check" {
		t.Fatalf("fix = %q, want 'nil check'", failures[0].Metadata[memory.FailureMetaFix])
	}
}

func TestCmdMemoryFailureListOutput(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(tmpDir, "mem"))
	t.Setenv("GLEANN_REMOTE_ADDR", "off")

	cmdMemory([]string{"failure", "add", "--command", "go build", "--error", "undefined: X", "--fix", "import pkg"})

	out := captureHelp(t, func() { cmdMemory([]string{"failure", "list"}) })
	if !strings.Contains(out, "undefined: X") {
		t.Errorf("list should show the error, got:\n%s", out)
	}
	if !strings.Contains(out, "import pkg") {
		t.Errorf("list should show the fix, got:\n%s", out)
	}
}

func TestCmdMemoryContextSurfacesFailures(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(tmpDir, "mem"))
	t.Setenv("GLEANN_REMOTE_ADDR", "off")

	cmdMemory([]string{"failure", "add",
		"--command", "go test ./pkg/memory",
		"--error", "boom",
		"--fix", "nil check",
		"--path", "pkg/memory/block.go"})

	// No task → no failure section.
	out := captureHelp(t, func() { cmdMemory([]string{"context"}) })
	if strings.Contains(out, "<failure_memory>") {
		t.Errorf("failure should not surface without a task, got:\n%s", out)
	}

	// Naming the command surfaces the failure in its own section.
	out = captureHelp(t, func() { cmdMemory([]string{"context", "--command", "go test"}) })
	if !strings.Contains(out, "<failure_memory>") {
		t.Errorf("expected <failure_memory> section, got:\n%s", out)
	}
	if !strings.Contains(out, "nil check") {
		t.Errorf("expected the fix, got:\n%s", out)
	}
}
