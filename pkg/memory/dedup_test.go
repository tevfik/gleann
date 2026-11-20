package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

func openDedupStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addBlock(t *testing.T, s *Store, scope string, tier Tier, content string) *Block {
	t.Helper()
	b := &Block{Tier: tier, Label: "user_memory", Content: content, Scope: scope}
	if err := s.Add(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func countBlocks(t *testing.T, s *Store) int {
	t.Helper()
	all, err := s.List("")
	if err != nil {
		t.Fatal(err)
	}
	return len(all)
}

// Fails on the old code: the ID only lowercased and trimmed, so a trailing
// period or a whitespace run produced a second block.
func TestAddExactDuplicateIsCaseWhitespacePunctuationRobust(t *testing.T) {
	s := openDedupStore(t)
	first := addBlock(t, s, "proj", TierLong, "Use Postgres for the billing service")
	for _, variant := range []string{
		"use postgres for the billing service.",
		"  Use   Postgres\tfor the  billing service!\n",
		"\"USE POSTGRES FOR THE BILLING SERVICE\"",
	} {
		got := addBlock(t, s, "proj", TierLong, variant)
		if got.ID != first.ID {
			t.Errorf("%q: got ID %s, want %s", variant, got.ID, first.ID)
		}
	}
	if n := countBlocks(t, s); n != 1 {
		t.Fatalf("blocks = %d, want 1", n)
	}
	b, _ := s.Get(first.ID)
	if b.Confirms != 3 {
		t.Errorf("Confirms = %d, want 3", b.Confirms)
	}
}

// Fails on the old code. Blocks stored by older versions keep their old IDs;
// a new write with the same normalised content must still find them, with
// neither a migration nor the similarity step (disabled here).
func TestAddFindsBlocksStoredUnderLegacyIDs(t *testing.T) {
	t.Setenv(nearDedupEnv, "off")
	s := openDedupStore(t)

	// Stored with the legacy formula: trailing period is part of the hash.
	old := &Block{Tier: TierLong, Label: "user_memory", Content: "Use Postgres.", Scope: "proj"}
	old.ID = legacyBlockID(old)
	if old.ID == generateBlockID(old) {
		t.Fatal("test needs differing legacy and current IDs")
	}
	if err := s.Add(old); err != nil {
		t.Fatal(err)
	}

	got := addBlock(t, s, "proj", TierLong, "use postgres")
	if got.ID != old.ID {
		t.Fatalf("merged into %s, want legacy block %s", got.ID, old.ID)
	}
	if n := countBlocks(t, s); n != 1 {
		t.Fatalf("blocks = %d, want 1", n)
	}

	// Same content as the legacy formula would hash it: found by legacy ID.
	old2 := &Block{Tier: TierLong, Label: "user_memory", Content: "Keep  two spaces", Scope: "proj"}
	old2.ID = legacyBlockID(old2)
	if err := s.Add(old2); err != nil {
		t.Fatal(err)
	}
	got = addBlock(t, s, "proj", TierLong, "Keep  two spaces")
	if got.ID != old2.ID || countBlocks(t, s) != 2 {
		t.Fatalf("legacy-ID block not found: id %s, blocks %d", got.ID, countBlocks(t, s))
	}
}

// Fails on the old code: each wording was stored as its own block.
func TestAddMergesNearDuplicates(t *testing.T) {
	s := openDedupStore(t)
	first := addBlock(t, s, "proj", TierLong, "Always run gofmt and go vet before committing Go code")
	got := addBlock(t, s, "proj", TierLong, "Always run gofmt and go vet before you commit the Go code.")
	if got.ID != first.ID {
		t.Fatalf("near duplicate stored as %s, want merge into %s", got.ID, first.ID)
	}
	// The new wording only adds words ("you", "the"), so the longer, newer
	// text replaces the content; the ID stays.
	if got.Content != "Always run gofmt and go vet before you commit the Go code." {
		t.Errorf("merged block content = %q, want the longer new text", got.Content)
	}
	if n := countBlocks(t, s); n != 1 {
		t.Fatalf("blocks = %d, want 1", n)
	}
	b, _ := s.Get(first.ID)
	if b.Confirms != 1 {
		t.Errorf("Confirms = %d, want 1", b.Confirms)
	}
}

func TestAddNearDuplicateRespectsScopeAndTier(t *testing.T) {
	s := openDedupStore(t)
	a := addBlock(t, s, "proj-a", TierLong, "Always run gofmt and go vet before committing Go code")
	b := addBlock(t, s, "proj-b", TierLong, "Always run gofmt and go vet before you commit Go code")
	c := addBlock(t, s, "proj-a", TierMedium, "Always run gofmt and go vet before you commit Go code")
	d := addBlock(t, s, "", TierLong, "Always run gofmt and go vet before you commit Go code")
	ids := map[string]bool{a.ID: true, b.ID: true, c.ID: true, d.ID: true}
	if len(ids) != 4 || countBlocks(t, s) != 4 {
		t.Fatalf("merged across scope or tier: %d ids, %d blocks", len(ids), countBlocks(t, s))
	}
}

func TestAddNearDuplicateConservative(t *testing.T) {
	cases := []struct{ name, a, b string }{
		{"negation", "Always use CGo for the core engine build", "Never use CGo for the core engine build"},
		{"negation with not", "The server must verify the token signature on every request", "The server must not verify the token signature on every request"},
		{"enable vs disable", "Enable the query cache in production deployments", "Disable the query cache in production deployments"},
		{"different number", "The REST server listens on port 8081 by default", "The REST server listens on port 9090 by default"},
		{"short text, one word differs", "Use Postgres", "Use MySQL"},
		{"short with extra word", "Use the Postgres pool", "Use Postgres pool sizing"},
		{"unrelated sharing words", "Run the tests before pushing to main", "Run the benchmark before pushing the release"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := openDedupStore(t)
			a := addBlock(t, s, "p", TierLong, c.a)
			b := addBlock(t, s, "p", TierLong, c.b)
			if a.ID == b.ID || countBlocks(t, s) != 2 {
				t.Fatalf("merged %q into %q", c.b, c.a)
			}
		})
	}
}

func TestAddNearDuplicateEnvOff(t *testing.T) {
	t.Setenv(nearDedupEnv, "off")
	s := openDedupStore(t)
	a := addBlock(t, s, "p", TierLong, "Always run gofmt and go vet before committing Go code")
	b := addBlock(t, s, "p", TierLong, "Always run gofmt and go vet before you commit the Go code.")
	if a.ID == b.ID || countBlocks(t, s) != 2 {
		t.Fatal("near duplicate merged with GLEANN_MEMORY_NEAR_DEDUP=off")
	}
	// Exact (normalised) duplicates still merge.
	c := addBlock(t, s, "p", TierLong, "ALWAYS run gofmt and go vet before committing Go code.")
	if c.ID != a.ID {
		t.Fatal("normalised duplicate not merged with the knob off")
	}
}

func TestAddNearDuplicateShortTier(t *testing.T) {
	s := openDedupStore(t)
	a := addBlock(t, s, "p", TierShort, "Currently debugging the flaky watch test in the cli package")
	b := addBlock(t, s, "p", TierShort, "Currently debugging the flaky watch test in the cli package today")
	if a.ID != b.ID || len(s.shortTerm) != 1 {
		t.Fatalf("short-tier near duplicate not merged: %d blocks", len(s.shortTerm))
	}
}

// Failure blocks keep their own signature-based dedup: similar but distinct
// failures must stay separate, and a failure never absorbs a plain memory.
func TestAddNeverNearMergesFailureBlocks(t *testing.T) {
	s := openDedupStore(t)
	add := func(label, content string) *Block {
		b := &Block{Tier: TierLong, Label: label, Content: content}
		if err := s.Add(b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	a := add(FailureLabel, "go test ./pkg/memory failed with exit status 1 in TestFoo")
	b := add(FailureLabel, "go test ./pkg/memory failed with exit status 2 in TestFoo")
	c := add(FailureLabel, "go test ./pkg/memory failed with exit status 1 in TestFoo again")
	d := add("user_memory", "go test ./pkg/memory failed with exit status 1 in TestFoo today")
	ids := map[string]bool{a.ID: true, b.ID: true, c.ID: true, d.ID: true}
	if len(ids) != 4 {
		t.Fatalf("failure blocks merged: %d distinct of 4", len(ids))
	}
}

// A negation is a conflict, not a duplicate: RememberBlock must still flag
// it, and a block flagged as contradicting is never merged as a near
// duplicate.
func TestRememberContradictionNotMerged(t *testing.T) {
	s := openDedupStore(t)
	m := NewManager(s)
	first, err := m.Remember("The query cache is enabled in production deployments")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Remember("The query cache is not enabled in production deployments")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || countBlocks(t, s) != 2 {
		t.Fatal("contradicting statement merged into the original")
	}
	if second.Metadata["has_contradictions"] != "true" {
		t.Errorf("contradiction not flagged: %v", second.Metadata)
	}
	orig, _ := s.Get(first.ID)
	if orig.Conflicts != 1 {
		t.Errorf("original Conflicts = %d, want 1", orig.Conflicts)
	}

	// The metadata gate on its own: wording that would merge, flagged.
	x := addBlock(t, s, "g", TierLong, "Always run gofmt and go vet before committing Go code")
	y := &Block{Tier: TierLong, Label: "user_memory", Scope: "g",
		Content:  "Always run gofmt and go vet before you commit the Go code",
		Metadata: map[string]string{"has_contradictions": "true"}}
	if err := s.Add(y); err != nil {
		t.Fatal(err)
	}
	if y.ID == x.ID {
		t.Fatal("block flagged as contradicting was merged as a duplicate")
	}
}

func TestFindDuplicateIsReadOnly(t *testing.T) {
	s := openDedupStore(t)
	first := addBlock(t, s, "p", TierLong, "Always run gofmt and go vet before committing Go code")
	probe := &Block{Tier: TierLong, Scope: "p", Content: "Always run gofmt and go vet before you commit Go code"}
	got, err := s.FindDuplicate(probe)
	if err != nil || got == nil || got.ID != first.ID {
		t.Fatalf("FindDuplicate = %v, %v; want %s", got, err, first.ID)
	}
	if probe.ID != "" {
		t.Error("FindDuplicate modified its argument")
	}
	if b, _ := s.Get(first.ID); b.Confirms != 0 {
		t.Errorf("FindDuplicate reinforced the block: Confirms = %d", b.Confirms)
	}
	other := &Block{Tier: TierLong, Scope: "p", Content: "Totally different memory about deploys"}
	if got, _ := s.FindDuplicate(other); got != nil {
		t.Errorf("FindDuplicate(other) = %v", got)
	}
}

// Fails on the old code: the engine recorded a near-duplicate as "added"
// because only the exact ID was looked up, so RevertReflection deleted the
// user's pre-existing block (the merge target).
func TestSleepTimeNearDuplicateIsNotRecordedAsAdded(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		tmpDir := t.TempDir()
		s, err := OpenStore(filepath.Join(tmpDir, "m.db"))
		if err != nil {
			t.Fatal(err)
		}
		mgr := NewManager(s)

		orig, err := mgr.Remember("Gleann uses pure Go for default build", "architecture")
		if err != nil {
			t.Fatal(err)
		}

		convDir := filepath.Join(tmpDir, "convs")
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatal(err)
		}
		convStore := conversations.NewStore(convDir)
		if err := convStore.Save(&conversations.Conversation{
			ID: "conv-near-001", Title: "Build", CreatedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now(),
			Messages: []conversations.Message{{Role: "user", Content: "Gleann uses pure Go for the default build now."}},
		}); err != nil {
			t.Fatal(err)
		}

		_, sumCfg := setupFakeSummarizer(t, "fact: Gleann uses pure Go for the default build now\n")
		engine := NewSleepTimeEngine(mgr, SleepTimeConfig{Enabled: true, MaxConvs: 5, ConvStore: convStore, SummarizerCfg: sumCfg})
		cs, err := engine.Reflect(context.Background(), dryRun)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs.BlocksAdded) != 0 {
			t.Fatalf("dryRun=%v: near duplicate recorded as added: %v", dryRun, cs.BlocksAdded)
		}
		if !dryRun {
			if _, err := engine.Revert(cs.RunID); err != nil {
				t.Fatal(err)
			}
		}
		remaining, err := s.List("")
		if err != nil || len(remaining) != 1 || remaining[0].ID != orig.ID {
			t.Fatalf("dryRun=%v: user block lost or duplicated after revert: %v, %v", dryRun, remaining, err)
		}
		mgr.Close()
		s.Close()
	}
}
