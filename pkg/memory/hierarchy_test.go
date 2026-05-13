package memory

import (
	"path/filepath"
	"testing"
)

// TestScopeAncestors verifies that path-style scopes generate the right
// ancestor visibility set (used by hierarchical project memory).
func TestScopeAncestors(t *testing.T) {
	cases := []struct {
		scope string
		want  []string
	}{
		{"", []string{""}},
		{"acme", []string{"", "acme"}},
		{"acme/web", []string{"", "acme", "acme/web"}},
		{"yaver-go/social/feature-x", []string{
			"", "yaver-go", "yaver-go/social", "yaver-go/social/feature-x",
		}},
		// Stray slashes do not create empty entries.
		{"a//b", []string{"", "a", "a/b", "a//b"}},
	}
	for _, tc := range cases {
		got := scopeAncestors(tc.scope)
		if len(got) != len(tc.want) {
			t.Errorf("scope %q: got %d ancestors, want %d (%v)", tc.scope, len(got), len(tc.want), got)
			continue
		}
		for _, w := range tc.want {
			if _, ok := got[w]; !ok {
				t.Errorf("scope %q: missing ancestor %q (got %v)", tc.scope, w, got)
			}
		}
	}
}

// TestFilterScope_Hierarchical verifies that ancestor-scoped blocks are
// returned when filtering by a deeper scope.
func TestFilterScope_Hierarchical(t *testing.T) {
	blocks := []Block{
		{ID: "g", Scope: ""},
		{ID: "p", Scope: "yaver-go"},
		{ID: "ps", Scope: "yaver-go/social"},
		{ID: "psf", Scope: "yaver-go/social/feature-x"},
		{ID: "other", Scope: "different-project"},
	}

	got := filterScope(blocks, "yaver-go/social/feature-x")
	wantIDs := map[string]bool{"g": true, "p": true, "ps": true, "psf": true}

	if len(got) != len(wantIDs) {
		t.Fatalf("got %d blocks, want %d (%+v)", len(got), len(wantIDs), got)
	}
	for _, b := range got {
		if !wantIDs[b.ID] {
			t.Errorf("unexpected block %q in result", b.ID)
		}
	}
}

// TestFilterScope_DescendantNotMatched ensures that querying a parent scope
// does NOT pull in blocks scoped to descendants (intentional asymmetric
// inheritance: parents are visible to children, not the reverse).
func TestFilterScope_DescendantNotMatched(t *testing.T) {
	blocks := []Block{
		{ID: "p", Scope: "yaver-go"},
		{ID: "psf", Scope: "yaver-go/social/feature-x"},
	}
	got := filterScope(blocks, "yaver-go")
	if len(got) != 1 || got[0].ID != "p" {
		t.Errorf("expected only the parent block, got %+v", got)
	}
}

// TestFilterScope_GlobalQueryReturnsAll checks that empty scope (no project
// context) returns every block.
func TestFilterScope_GlobalQueryReturnsAll(t *testing.T) {
	blocks := []Block{
		{ID: "g", Scope: ""},
		{ID: "p", Scope: "x"},
		{ID: "px", Scope: "x/y"},
	}
	got := filterScope(blocks, "")
	if len(got) != 3 {
		t.Errorf("global query expected to return all %d blocks, got %d", len(blocks), len(got))
	}
}

// TestFilterScope_PathStyleVsLegacy confirms that flat (non-path) scopes
// behave the same as before — backwards compatibility.
func TestFilterScope_PathStyleVsLegacy(t *testing.T) {
	blocks := []Block{
		{ID: "g", Scope: ""},
		{ID: "a", Scope: "acme"},
		{ID: "b", Scope: "different"},
	}
	got := filterScope(blocks, "acme")
	if len(got) != 2 {
		t.Errorf("flat scope: expected 2 blocks (global + acme), got %d", len(got))
	}
	for _, b := range got {
		if b.ID == "b" {
			t.Errorf("flat scope %q should not match unrelated scope %q", "acme", b.Scope)
		}
	}
}

// Ensure the hierarchy helper plays nicely with file-path-shaped scopes
// (commonly used when an agent passes its repo path as a scope).
func TestScopeAncestors_FilesystemPath(t *testing.T) {
	scope := filepath.ToSlash("yaver-go/internal/agent/social")
	got := scopeAncestors(scope)
	for _, want := range []string{"", "yaver-go", "yaver-go/internal", "yaver-go/internal/agent", "yaver-go/internal/agent/social"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing ancestor %q in %v", want, got)
		}
	}
}
