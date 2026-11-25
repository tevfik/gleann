package memory

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Finding H7: two directories that share a folder name — a remote-less
// repository and a plain directory — must not see each other's memory now
// that DetectRepoScope hashes the canonical path and prefixes the kind.
func TestH7CollisionSafeScopesIsolateMemory(t *testing.T) {
	repo := initScopeRepo(t, "") // remote-less git repository
	plain := t.TempDir()         // plain directory, unrelated path

	repoScope := DetectRepoScopeAt(repo)
	plainScope := DetectRepoScopeAt(plain)
	if repoScope == plainScope {
		t.Fatalf("scopes still collide: %q", repoScope)
	}

	store, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	mgr := NewManager(store)

	if _, err := mgr.AddScopedNote(repoScope, TierLong, "rule", "zz repo-only fact"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddScopedNote(plainScope, TierLong, "rule", "zz plain-only fact"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.AddScopedNote("", TierLong, "rule", "zz global fact"); err != nil {
		t.Fatal(err)
	}

	repoBlocks, err := mgr.ListScoped(repoScope, TierLong)
	if err != nil {
		t.Fatal(err)
	}
	plainBlocks, err := mgr.ListScoped(plainScope, TierLong)
	if err != nil {
		t.Fatal(err)
	}
	assertContents := func(blocks []Block, want, banned string) {
		t.Helper()
		var got []string
		for _, b := range blocks {
			got = append(got, b.Content)
		}
		joined := strings.Join(got, "\n")
		if !strings.Contains(joined, want) || !strings.Contains(joined, "zz global fact") {
			t.Errorf("scope view missing own/global fact: %v", got)
		}
		if strings.Contains(joined, banned) {
			t.Errorf("scope view leaked the other directory's fact: %v", got)
		}
	}
	assertContents(repoBlocks, "zz repo-only fact", "zz plain-only fact")
	assertContents(plainBlocks, "zz plain-only fact", "zz repo-only fact")

	// The injected context obeys the same isolation.
	cw, err := mgr.BuildScopedContext(repoScope)
	if err != nil {
		t.Fatal(err)
	}
	rendered := cw.Render()
	if !strings.Contains(rendered, "zz repo-only fact") || strings.Contains(rendered, "zz plain-only fact") {
		t.Errorf("repo context leaked or lost blocks:\n%s", rendered)
	}
}

// dropCollisionLedger simulates a genuine pre-v1.22 database: old binaries
// never wrote the collision-migration ledger keys, so remove the ones the
// seeding open recorded.
func dropCollisionLedger(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", sqlitePathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, key := range []string{scopeCollisionMigration, scopeLegacyNamesKey} {
		if _, err := raw.Exec(`DELETE FROM memory_meta WHERE key = ?`, key); err != nil {
			t.Fatal(err)
		}
	}
}

// Blocks stored before v1.22 under a bare folder name stay readable from the
// collision-safe scope of the directory they came from, without ever being
// rewritten; new writes use the prefixed scope.
func TestH7LegacyBareScopeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")

	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &Block{ID: "legacy-app", Tier: TierLong, Label: "fact", Content: "zz legacy app fact", Scope: "app"}
	if err := s1.Add(legacy); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	dropCollisionLedger(t, path)

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	m2 := NewManager(s2)

	// The migration recorded the bare name and never touched the row.
	if v, err := s2.Meta(scopeCollisionMigration); err != nil || v != "1" {
		t.Fatalf("ledger = %q (err %v), want 1", v, err)
	}
	if names := s2.LegacyScopeNames(); len(names) != 1 || names[0] != "app" {
		t.Fatalf("LegacyScopeNames = %v, want [app]", names)
	}
	stored, err := s2.Get("legacy-app")
	if err != nil || stored.Scope != "app" {
		t.Fatalf("legacy row rewritten: %+v (err %v)", stored, err)
	}

	// Both collision-safe forms of "app" read the legacy block plus global.
	for _, scope := range []string{"local:app-1a2b3c4d", "dir:app-1a2b3c4d"} {
		got, err := m2.ListScoped(scope, TierLong)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Content != "zz legacy app fact" {
			t.Errorf("ListScoped(%q) = %v, want the legacy block", scope, got)
		}
	}

	// A new write from the prefixed scope lands in the prefixed scope, not
	// back into the bare name.
	if _, err := m2.AddScopedNote("local:app-1a2b3c4d", TierLong, "fact", "zz new app fact"); err != nil {
		t.Fatal(err)
	}
	got, _ := m2.ListScoped("local:app-1a2b3c4d", TierLong)
	if len(got) != 2 {
		t.Fatalf("prefixed view = %d blocks, want 2 (legacy + new)", len(got))
	}
	for _, b := range got {
		if b.Content == "zz new app fact" && b.Scope != "local:app-1a2b3c4d" {
			t.Errorf("new block stored under %q, want the prefixed scope", b.Scope)
		}
	}

	// Search and context see the legacy block too.
	found, err := m2.SearchScoped("dir:app-1a2b3c4d", "legacy")
	if err != nil || len(found) != 1 {
		t.Errorf("SearchScoped legacy = %v (err %v), want 1", found, err)
	}
	cw, err := m2.BuildScopedContext("local:app-1a2b3c4d")
	if err != nil || !strings.Contains(cw.Render(), "zz legacy app fact") {
		t.Errorf("context missing legacy block (err %v):\n%s", err, cw.Render())
	}

	// Reopening keeps the inventory stable and does not rescan.
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
	s3, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if names := s3.LegacyScopeNames(); len(names) != 1 || names[0] != "app" {
		t.Fatalf("LegacyScopeNames after reopen = %v, want [app]", names)
	}
}

// A bare scope first written AFTER the switch is not widened: the inventory
// only covers names that already existed at migration time.
func TestH7NewBareScopeNotWidened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := OpenStore(path) // migration runs on an empty store
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := NewManager(s)

	if _, err := m.AddScopedNote("app", TierLong, "fact", "zz explicit app fact"); err != nil {
		t.Fatal(err)
	}
	if names := s.LegacyScopeNames(); len(names) != 0 {
		t.Fatalf("LegacyScopeNames = %v, want empty (name appeared after the switch)", names)
	}
	for _, scope := range []string{"local:app-1a2b3c4d", "dir:app-1a2b3c4d"} {
		got, err := m.ListScoped(scope, TierLong)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("ListScoped(%q) = %v, want 0 (post-switch bare scope not aliased)", scope, got)
		}
	}
	// The explicit scope itself still reads its own block.
	got, err := m.ListScoped("app", TierLong)
	if err != nil || len(got) != 1 {
		t.Errorf("ListScoped(app) = %v (err %v), want 1", got, err)
	}
}

// Concurrent openers of a pre-v1.22 database must all end up with the same
// legacy alias cache, whichever of them runs the inventory scan.
func TestH7LegacyMigrationConcurrentOpeners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Add(&Block{ID: "legacy-app", Tier: TierLong, Label: "fact", Content: "zz legacy app", Scope: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	dropCollisionLedger(t, path)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := OpenStore(path)
			if err != nil {
				t.Errorf("concurrent open: %v", err)
				return
			}
			defer s.Close()
			got, err := NewManager(s).ListScoped("local:app-1a2b3c4d", TierLong)
			if err != nil || len(got) != 1 || got[0].Content != "zz legacy app" {
				t.Errorf("concurrent opener view = %v (err %v), want the legacy block", got, err)
			}
		}()
	}
	wg.Wait()
}

// Remote-backed and path-shaped scopes have no legacy alias, and prefixed
// scopes with a different basename never alias each other.
func TestH7AliasOnlyForMatchingBasename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.db")
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Add(&Block{ID: "legacy-app", Tier: TierLong, Label: "fact", Content: "zz legacy app", Scope: "app"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	dropCollisionLedger(t, path)

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	m2 := NewManager(s2)

	for _, scope := range []string{"local:other-1a2b3c4d", "dir:other-1a2b3c4d", "github.com/org/app"} {
		got, err := m2.ListScoped(scope, TierLong)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("ListScoped(%q) = %v, want 0 (no alias for a different name)", scope, got)
		}
	}
}
