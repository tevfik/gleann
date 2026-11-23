package memory

import "testing"

// Review finding "scoped rewrites" / "Global writes ... penetrate": a write
// may only lower the validity of blocks in scopes the writer can see (global
// blocks, its own scope and its ancestors). A write must never penalise a
// block in a scope the writer cannot see.

// The review's defect scenario: a near-duplicate written in a child scope
// must not raise Conflicts on the global block. (With the repaired polarity
// rule agreeing negatives no longer fire at all, so this also guards against
// a regression of that rule across scopes.)
func TestDefect_CrossScopeNearDuplicateFlaggedAsContradiction(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	s := openTestStore(t)
	mgr := NewManager(s)
	global, err := mgr.RememberBlock(&Block{
		Tier: TierLong, Label: "user_memory",
		Content: "The search index is not built yet on Windows",
	})
	if err != nil {
		t.Fatal(err)
	}
	written, err := mgr.RememberBlock(&Block{
		Tier: TierLong, Label: "user_memory", Scope: "projA",
		Content: "The search index is not built yet on Windows today",
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(global.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Conflicts != 0 {
		t.Errorf("global block Conflicts = %d after a child-scope near-duplicate write, want 0", after.Conflicts)
	}
	if written.Metadata["has_contradictions"] == "true" {
		t.Errorf("child-scope near-duplicate flagged as contradiction: %v", written.Metadata)
	}
}

// A global write must not lower the validity of a scoped block, even when
// the texts are a true contradiction pair: the global writer cannot see the
// scoped block, so it must leave it untouched. Same for an ancestor writing
// against a descendant's block.
func TestRememberBlock_WriteNeverPenalisesInvisibleScopes(t *testing.T) {
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	tests := []struct {
		name        string
		storedScope string
		writeScope  string
	}{
		{"global write vs scoped block", "projA", ""},
		{"ancestor write vs descendant block", "org/repo", "org"},
		{"global write vs deep scoped block", "org/repo/feature", ""},
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
			if after.Conflicts != 0 {
				t.Errorf("invisible block %q penalised by write in scope %q: Conflicts = %d, want 0",
					stored.Scope, tt.writeScope, after.Conflicts)
			}
			if written.Metadata["has_contradictions"] == "true" {
				t.Errorf("write in scope %q flagged against invisible scope %q: %v",
					tt.writeScope, tt.storedScope, written.Metadata)
			}
		})
	}
}

// Review section 5, weak test (5): the near-duplicate confirmation test only
// covered the global scope. The same guarantee must hold inside a project
// scope: the rewrite merges into the scoped block and confirms it.
func TestRememberBlock_NearDuplicateRewriteIsConfirmationScoped(t *testing.T) {
	t.Setenv(nearDedupEnv, "")
	t.Setenv("GLEANN_BLOCK_CHAR_LIMIT", "")
	s := openTestStore(t)
	mgr := NewManager(s)
	first, err := mgr.RememberBlock(&Block{
		Tier: TierLong, Label: "user_memory", Scope: "projA",
		Content: "The search index is not built yet on Windows",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := mgr.RememberBlock(&Block{
		Tier: TierLong, Label: "user_memory", Scope: "projA",
		Content: "The search index is not built yet on Windows today",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Errorf("scoped near-duplicate rewrite was not merged: %s != %s", second.ID, first.ID)
	}
	if second.Conflicts != 0 {
		t.Errorf("scoped near-duplicate rewrite counted %d conflicts against its merge target", second.Conflicts)
	}
	stored, err := s.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Conflicts != 0 {
		t.Errorf("scoped merge target Conflicts = %d, want 0", stored.Conflicts)
	}
	if stored.Confirms < 1 {
		t.Errorf("scoped merge target was not confirmed: Confirms = %d", stored.Confirms)
	}
}
