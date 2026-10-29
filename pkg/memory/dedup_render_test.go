package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStore_Add_Deduplication(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "dedup_test.db")
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	// 1. Initial block
	b1 := &Block{
		ID:        "blk-dedup-1",
		Tier:      TierMedium,
		Label:     "architecture",
		Content:   "Use Clean Architecture",
		Tags:      []string{"taga"},
		Repo:      "",
		Commit:    "",
		Paths:     []string{"internal/domain/user.go"},
		Symbols:   []string{"domain.User"},
		CreatedAt: time.Now(),
	}
	if err := s.Add(b1); err != nil {
		t.Fatalf("Initial Add: %v", err)
	}

	// 2. Add same block ID with new tag, repo, commit, new path, and new symbol
	b2 := &Block{
		ID:        "blk-dedup-1",
		Tier:      TierMedium,
		Label:     "architecture",
		Content:   "Use Clean Architecture",
		Tags:      []string{"taga", "tagb"}, // taga duplicate, tagb new
		Repo:      "github.com/tevfik/gleann",
		Commit:    "abc1234",
		Paths:     []string{"internal/domain/user.go", "internal/domain/order.go"}, // one dup, one new
		Symbols:   []string{"domain.User", "domain.Order"},                         // one dup, one new
		CreatedAt: time.Now(),
	}
	if err := s.Add(b2); err != nil {
		t.Fatalf("Second Add (dedup): %v", err)
	}

	// Verify merged state
	stored, err := s.Get("blk-dedup-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Confirms != 1 {
		t.Errorf("expected Confirms to be 1, got %d", stored.Confirms)
	}
	if stored.Repo != "github.com/tevfik/gleann" || stored.Commit != "abc1234" {
		t.Errorf("expected repo/commit to be set, got repo=%s, commit=%s", stored.Repo, stored.Commit)
	}
	if len(stored.Tags) != 2 || len(stored.Paths) != 2 || len(stored.Symbols) != 2 {
		t.Errorf("expected 2 tags, 2 paths, 2 symbols, got %d tags, %d paths, %d symbols",
			len(stored.Tags), len(stored.Paths), len(stored.Symbols))
	}
}

func TestContextWindow_Render_SuspectAndProvenance(t *testing.T) {
	// 1. Block with Suspect = true and empty StaleReason
	bSuspectDefault := Block{
		Label:   "note",
		Content: "Some suspect note with default reason",
		Suspect: true,
	}

	// 2. Block with Paths only (no symbols)
	bPathsOnly := Block{
		Label:   "file-note",
		Content: "File based note",
		Paths:   []string{"pkg/memory/block.go"},
	}

	cw := ContextWindow{
		ShortTerm:  []Block{bSuspectDefault},
		MediumTerm: []Block{bPathsOnly},
	}

	rendered := cw.Render()
	if !strings.Contains(rendered, "[⚠️ SUSPECT: code changed]") {
		t.Errorf("expected default suspect label in rendered output: %s", rendered)
	}
	if !strings.Contains(rendered, "(files: pkg/memory/block.go)") {
		t.Errorf("expected files provenance in rendered output: %s", rendered)
	}
}
