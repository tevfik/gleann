package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestCmdMemoryExportImportRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	memDir1 := filepath.Join(tmpDir, "mem1")
	t.Setenv("GLEANN_MEMORY_DIR", memDir1)

	// Add memories via CLI
	cmdMemory([]string{"remember", "Architecture: Hexagonal design pattern", "--tag", "architecture,core", "--label", "convention"})
	cmdMemory([]string{"add", "medium", "Sprint 15 retrospective digest", "--tag", "sprint-15", "--label", "digest"})

	exportDir := filepath.Join(tmpDir, "export")
	cmdMemory([]string{"export", "--dir", exportDir})

	// Check exported directory has files
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("ReadDir exportDir failed: %v", err)
	}
	var mdCount int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount < 2 {
		t.Errorf("got %d markdown files in exportDir, want at least 2", mdCount)
	}

	// Switch to a new empty memory store
	memDir2 := filepath.Join(tmpDir, "mem2")
	t.Setenv("GLEANN_MEMORY_DIR", memDir2)

	// Import into new store
	cmdMemory([]string{"import", "--dir", exportDir})

	// Verify imported blocks exist in the new store
	s2, err := memory.DefaultStore()
	if err != nil {
		t.Fatalf("DefaultStore failed: %v", err)
	}
	defer s2.Close()

	blocks, err := s2.List("")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks after import, want 2", len(blocks))
	}

	foundHex := false
	foundSprint := false
	for _, b := range blocks {
		if strings.Contains(b.Content, "Hexagonal") {
			foundHex = true
			if b.Label != "convention" {
				t.Errorf("expected label 'convention', got %q", b.Label)
			}
			if len(b.Tags) != 2 {
				t.Errorf("expected 2 tags, got %v", b.Tags)
			}
		}
		if strings.Contains(b.Content, "Sprint 15") {
			foundSprint = true
			if b.Tier != memory.TierMedium {
				t.Errorf("expected tier 'medium', got %q", b.Tier)
			}
		}
	}
	if !foundHex || !foundSprint {
		t.Errorf("imported blocks missing expected content: hex=%v, sprint=%v", foundHex, foundSprint)
	}

	// Idempotent re-import into store 2
	cmdMemory([]string{"import", "--dir", exportDir})
	blocks2, err := s2.List("")
	if err != nil {
		t.Fatalf("List2 failed: %v", err)
	}
	if len(blocks2) != 2 {
		t.Errorf("re-import changed block count: got %d, want 2", len(blocks2))
	}
}

func TestCmdMemoryExportImportFlags(t *testing.T) {
	// Missing --dir exits with code 1
	if got := runExit(t, func() { cmdMemory([]string{"export"}) }); got != 1 {
		t.Errorf("gleann memory export without --dir: exit status %d, want 1", got)
	}
	if got := runExit(t, func() { cmdMemory([]string{"import"}) }); got != 1 {
		t.Errorf("gleann memory import without --dir: exit status %d, want 1", got)
	}

	// Unknown flags exit with code 2
	if got := runExit(t, func() { cmdMemory([]string{"export", "--dir", "/tmp", "--bogus"}) }); got != 2 {
		t.Errorf("gleann memory export with unknown flag: exit status %d, want 2", got)
	}
	if got := runExit(t, func() { cmdMemory([]string{"import", "--dir", "/tmp", "--bogus"}) }); got != 2 {
		t.Errorf("gleann memory import with unknown flag: exit status %d, want 2", got)
	}
}
