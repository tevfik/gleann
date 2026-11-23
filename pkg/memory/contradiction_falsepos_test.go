package memory

import (
	"testing"
)

// Tests for audit item mem-contradiction-false-positives: the contradiction
// heuristic must not penalise identical or unrelated blocks, and must stay
// inside the scopes visible to the new block. Every store here is a temporary
// one (openTestStore uses t.TempDir); nothing touches the real home directory.

// Identical text can never contradict itself. Before the fix the substring
// negation pairs ("cannot" contains "can", "is not" contains "is", "don't"
// contains "do") made every block with a negation contradict its own rewrite.
func TestContradictionCheck_IdenticalText(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"cannot", "The parser cannot handle empty input"},
		{"is not", "The index is not built yet"},
		{"dont", "I don't use vim for editing"},
		{"should not", "The deployment should not use Docker containers"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Block{Content: tt.content}
			if b.ContradictionCheck(tt.content) {
				t.Errorf("identical text flagged as contradiction: %q", tt.content)
			}
		})
	}
}

// Unrelated statements must not be flagged. The first case exposes both the
// substring gate ("is" inside "his") and the single-shared-word weakness; the
// second is the audit's example (a guard: it already passes before the fix,
// because no word longer than four characters is shared).
func TestContradictionCheck_Unrelated(t *testing.T) {
	tests := []struct {
		name, old, new string
	}{
		{"substring gate, one shared word", "his server runs fast", "the server is not slow"},
		{"audit example", "this list is long", "the index is not built"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Block{Content: tt.old}
			if b.ContradictionCheck(tt.new) {
				t.Errorf("unrelated content flagged as contradiction: %q vs %q", tt.old, tt.new)
			}
		})
	}
}

// Writing the same fact twice is a confirmation, not a conflict: the stored
// block must keep Conflicts == 0 and the write must merge (same ID).
func TestRememberBlock_RewriteIsConfirmation(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	tests := []struct {
		name    string
		content string
	}{
		{"cannot", "The parser cannot handle empty input"},
		{"is not", "The index is not built yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestStore(t)
			mgr := NewManager(s)
			first, err := mgr.Remember(tt.content)
			if err != nil {
				t.Fatal(err)
			}
			second, err := mgr.Remember(tt.content)
			if err != nil {
				t.Fatal(err)
			}
			if second.ID != first.ID {
				t.Errorf("rewrite was not deduplicated: %s != %s", second.ID, first.ID)
			}
			if second.Conflicts != 0 {
				t.Errorf("rewrite counted %d conflicts against its own block", second.Conflicts)
			}
			if second.Confirms < 1 {
				t.Errorf("rewrite did not confirm the block: Confirms = %d", second.Confirms)
			}
			stored, err := s.Get(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Conflicts != 0 {
				t.Errorf("stored block Conflicts = %d after an identical rewrite, want 0", stored.Conflicts)
			}
		})
	}
}

// A near-duplicate rewrite (the stored text plus extra words, same negation)
// must merge into the block it duplicates instead of being flagged as a
// contradiction of it. Before the fix the flag also blocked the merge itself.
func TestRememberBlock_NearDuplicateRewriteIsConfirmation(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	s := openTestStore(t)
	mgr := NewManager(s)
	first, err := mgr.Remember("The search index is not built yet on Windows")
	if err != nil {
		t.Fatal(err)
	}
	second, err := mgr.Remember("The search index is not built yet on Windows today")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Errorf("near-duplicate rewrite was not merged: %s != %s", second.ID, first.ID)
	}
	if second.Conflicts != 0 {
		t.Errorf("near-duplicate rewrite counted %d conflicts against its merge target", second.Conflicts)
	}
	stored, err := s.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Conflicts != 0 {
		t.Errorf("merge target Conflicts = %d, want 0", stored.Conflicts)
	}
}

// Contradiction checks must stay inside the scopes visible to the new block
// (global blocks, the scope itself and its ancestors), not span all projects.
func TestRememberBlock_ContradictionScope(t *testing.T) {
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	tests := []struct {
		name         string
		storedScope  string
		writeScope   string
		wantConflict bool
	}{
		{"sibling scopes", "projA", "projB", false},
		{"same scope", "projA", "projA", true},
		{"global block, scoped write", "", "projB", true},
		{"ancestor scope", "org", "org/repo", true},
		// A write may only lower the validity of blocks in scopes the
		// writer can see. A global writer sees only global blocks, so a
		// scoped block must survive a global write untouched (repair of
		// the first patch, which expected true here).
		{"scoped block, global write", "projA", "", false},
		{"descendant block, ancestor write", "org/repo", "org", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestStore(t)
			mgr := NewManager(s)
			stored, err := mgr.RememberBlock(&Block{
				Tier: TierLong, Label: "user_memory", Scope: tt.storedScope,
				Content: "The build cache is enabled",
			})
			if err != nil {
				t.Fatal(err)
			}
			written, err := mgr.RememberBlock(&Block{
				Tier: TierLong, Label: "user_memory", Scope: tt.writeScope,
				Content: "The build cache is not enabled",
			})
			if err != nil {
				t.Fatal(err)
			}
			after, err := s.Get(stored.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantConflict {
				if after.Conflicts != 1 {
					t.Errorf("visible contradiction not counted: Conflicts = %d, want 1", after.Conflicts)
				}
				if written.Metadata["has_contradictions"] != "true" {
					t.Errorf("visible contradiction not flagged: %v", written.Metadata)
				}
			} else {
				if after.Conflicts != 0 {
					t.Errorf("cross-scope conflict counted: Conflicts = %d, want 0", after.Conflicts)
				}
				if written.Metadata["has_contradictions"] == "true" {
					t.Errorf("cross-scope conflict flagged: %v", written.Metadata)
				}
			}
		})
	}
}

// The audit's measure on the search-floor fixture: write every memory twice
// and no block may end up with Conflicts > 0. The floor fixture carries no
// negations, so negation-bearing memories are added as well: an identical
// negative rewrite must not contradict itself (review section 5 — the original
// test never exercised the polarity path).
func TestRememberBlock_FixtureDoubleWriteNoConflicts(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	s := openTestStore(t)
	mgr := NewManager(s)
	negations := []struct{ key, content string }{
		{"neg-parser", "The parser cannot handle empty input"},
		{"neg-vim", "I don't use vim for editing"},
		{"neg-staging", "The staging server is not reachable from the build network"},
		{"neg-webhook", "The webhook must not retry on a 4xx response"},
	}
	for pass := 0; pass < 2; pass++ {
		for _, f := range floorFixture {
			if _, err := mgr.Remember(f.content); err != nil {
				t.Fatalf("pass %d, %s: %v", pass, f.key, err)
			}
		}
		for _, f := range negations {
			if _, err := mgr.Remember(f.content); err != nil {
				t.Fatalf("pass %d, %s: %v", pass, f.key, err)
			}
		}
	}
	blocks, err := s.List(TierLong)
	if err != nil {
		t.Fatal(err)
	}
	flagged := 0
	for _, b := range blocks {
		if b.Conflicts > 0 {
			flagged++
			t.Errorf("block %s (%q) has Conflicts = %d after identical rewrites", b.ID, b.Content, b.Conflicts)
		}
	}
	if flagged > 0 {
		t.Errorf("%d of %d blocks flagged as conflicting after writing each memory twice", flagged, len(blocks))
	}
}
