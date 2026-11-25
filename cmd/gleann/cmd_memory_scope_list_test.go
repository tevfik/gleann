package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

// memory list defaults to the detected repository scope plus global, like
// memory context; --scope all lists every scope and --scope <name> one.
func TestCmdMemoryListScopeDefault(t *testing.T) {
	setupFailureMem(t)

	runMem(t, "remember", "zz list global fact")
	runMem(t, "remember", "zz list repo fact", "--scope", "repo")
	runMem(t, "remember", "zz list foreign fact", "--scope", "foreign-repo")

	out := runMem(t, "list")
	if !strings.Contains(out, "zz list global fact") || !strings.Contains(out, "zz list repo fact") {
		t.Fatalf("default list must show global + detected repo, got %q", out)
	}
	if strings.Contains(out, "zz list foreign fact") {
		t.Fatalf("default list leaked a foreign scope: %q", out)
	}
	if !strings.Contains(out, "Memories (2, all tiers)") {
		t.Fatalf("default list count wrong: %q", out)
	}

	for _, all := range []string{"all", "*"} {
		out = runMem(t, "list", "--scope", all)
		if !strings.Contains(out, "Memories (3, all tiers)") || !strings.Contains(out, "zz list foreign fact") {
			t.Fatalf("--scope %s must list every scope, got %q", all, out)
		}
	}

	out = runMem(t, "list", "--scope", "foreign-repo")
	if !strings.Contains(out, "zz list foreign fact") || strings.Contains(out, "zz list repo fact") {
		t.Fatalf("--scope foreign-repo = %q", out)
	}

	// The repo alias resolves to the detected scope.
	out = runMem(t, "list", "--scope", "repo")
	if !strings.Contains(out, "zz list repo fact") || strings.Contains(out, "zz list foreign fact") {
		t.Fatalf("--scope repo = %q", out)
	}

	// An empty scope view explains how to widen it. Every scope sees global
	// blocks, so drop the global one first.
	runMem(t, "forget", "zz list global fact")
	out = runMem(t, "list", "--scope", "no-such-scope")
	if !strings.Contains(out, "No memories stored.") || !strings.Contains(out, "--scope all") {
		t.Fatalf("empty scoped list should hint at --scope all, got %q", out)
	}
}

// memory export --scope narrows the dump; import --dry-run previews without
// writing anything.
func TestCmdMemoryExportScopeImportDryRun(t *testing.T) {
	setupFailureMem(t)

	runMem(t, "remember", "zz cli global fact")
	runMem(t, "remember", "zz cli repo fact", "--scope", "repo")
	runMem(t, "remember", "zz cli foreign fact", "--scope", "foreign-repo")

	tmp := t.TempDir()
	scopedDir := filepath.Join(tmp, "scoped")
	out := runMem(t, "export", "--dir", scopedDir, "--scope", "repo")
	if !strings.Contains(out, "Exported 2 memory block(s)") { // global + detected repo
		t.Fatalf("scoped export = %q", out)
	}
	fullDir := filepath.Join(tmp, "full")
	out = runMem(t, "export", "--dir", fullDir)
	if !strings.Contains(out, "Exported 3 memory block(s)") || !strings.Contains(out, "[scope: all]") {
		t.Fatalf("full export = %q", out)
	}

	// Import the full dump into a fresh store as a dry run: nothing lands.
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(tmp, "mem2"))
	out = runMem(t, "import", "--dir", fullDir, "--dry-run")
	if !strings.Contains(out, "Dry run") || !strings.Contains(out, "3 would import") || !strings.Contains(out, "nothing written") {
		t.Fatalf("dry-run import = %q", out)
	}
	s2, err := memory.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := s2.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Fatalf("dry run wrote %d blocks, want 0", len(blocks))
	}
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}

	// The real import writes all three.
	out = runMem(t, "import", "--dir", fullDir)
	if !strings.Contains(out, "3 imported") {
		t.Fatalf("real import = %q", out)
	}
	s3, err := memory.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	blocks, err = s3.List("")
	if err != nil || len(blocks) != 3 {
		t.Fatalf("real import stored %d blocks (err %v), want 3", len(blocks), err)
	}
}
