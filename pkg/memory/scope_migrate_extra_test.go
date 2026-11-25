package memory

import (
	"os"
	"path/filepath"
	"testing"
)

// Two remote-less repositories with the same folder name get different scopes.
func TestH7SameNamedDirectoriesGetDistinctScopes(t *testing.T) {
	a := filepath.Join(t.TempDir(), "app")
	b := filepath.Join(t.TempDir(), "app")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sa, sb := DetectRepoScopeAt(a), DetectRepoScopeAt(b)
	if sa == sb {
		t.Fatalf("same-named directories share scope %q", sa)
	}
	if base, ok := legacyAliasScope(sa); !ok || base != "app" {
		t.Fatalf("legacyAliasScope(%q) = %q, %v", sa, base, ok)
	}

	s, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := NewManager(s)
	if _, err := m.AddScopedNote(sa, TierLong, "fact", "zz only in a"); err != nil {
		t.Fatal(err)
	}
	got, err := m.ListScoped(sb, TierLong)
	if err != nil || len(got) != 0 {
		t.Fatalf("scope b sees %v (err %v), want nothing from a", got, err)
	}
}

// The inventory is taken once: re-opening, even after a new bare scope has
// appeared, keeps the recorded names and leaves every block row untouched.
func TestH7MigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Add(&Block{ID: "old", Tier: TierLong, Label: "fact", Content: "zz old", Scope: "app"}); err != nil {
		t.Fatal(err)
	}
	s1.Close()
	dropCollisionLedger(t, path)

	for i := 0; i < 3; i++ {
		s, err := OpenStore(path)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 { // a bare scope created after the switch is never absorbed
			if err := s.Add(&Block{ID: "new", Tier: TierLong, Label: "fact", Content: "zz new", Scope: "other"}); err != nil {
				t.Fatal(err)
			}
		}
		if names := s.LegacyScopeNames(); len(names) != 1 || names[0] != "app" {
			t.Fatalf("open %d: LegacyScopeNames = %v, want [app]", i, names)
		}
		old, err := s.Get("old")
		if err != nil || old.Scope != "app" || old.Content != "zz old" {
			t.Fatalf("open %d: legacy row changed: %+v (err %v)", i, old, err)
		}
		s.Close()
	}
}

// Failure listings, task context and export honour the legacy alias like list
// and search do.
func TestH7LegacyAliasInFailuresAndExport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m1 := NewManager(s1)
	if _, err := m1.RecordFailureScoped("app", "app", Failure{Command: "go test ./app", Error: "boom", Fix: "fix"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Add(&Block{ID: "old", Tier: TierLong, Label: "fact", Content: "zz old", Scope: "app"}); err != nil {
		t.Fatal(err)
	}
	s1.Close()
	dropCollisionLedger(t, path)

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := NewManager(s)
	const scope = "local:app-1a2b3c4d"

	fs, err := m.ListFailuresScoped(scope)
	if err != nil || len(fs) != 1 {
		t.Fatalf("ListFailuresScoped = %d (err %v), want 1", len(fs), err)
	}
	cw, err := m.BuildTaskContextFor(scope, "go test ./app", nil, nil)
	if err != nil || len(cw.Failures) != 1 {
		t.Fatalf("task context failures = %v (err %v), want 1", cw, err)
	}
	if _, err := s.ExportWithOptions(t.TempDir(), ExportOptions{Scope: scope}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := s.ExportWithOptions(dir, ExportOptions{Scope: scope})
	if err != nil || st.Blocks < 2 {
		t.Fatalf("scoped export blocks = %v (err %v), want legacy block and failure", st, err)
	}
}

// A scoped export prunes only files its own manifest bucket listed: another
// scope's files and a user's own files survive.
func TestExportPruneOnlyOwnManifest(t *testing.T) {
	s, ids := seedExportBlocks(t)
	dir := t.TempDir()
	own := filepath.Join(dir, "mine.md")
	if err := os.WriteFile(own, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	const a, b = "github.com/org/repo-a", "github.com/org/repo-b"
	for _, sc := range []string{a, b} {
		if _, err := s.ExportWithOptions(dir, ExportOptions{Scope: sc}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Delete("exp-repo-a"); err != nil {
		t.Fatal(err)
	}
	st, err := s.ExportWithOptions(dir, ExportOptions{Scope: a})
	if err != nil {
		t.Fatal(err)
	}
	if st.Pruned != 1 {
		t.Fatalf("Pruned = %d, want 1", st.Pruned)
	}
	files := exportedFiles(t, dir)
	if files[ids["exp-repo-a"]] {
		t.Error("forgotten repo-a file survived")
	}
	if !files[ids["exp-repo-b"]] || !files["mine.md"] {
		t.Errorf("a file outside repo-a's manifest was pruned: %v", files)
	}
}
