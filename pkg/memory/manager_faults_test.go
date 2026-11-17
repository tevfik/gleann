package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// breakStore closes both database pools, so every later database access of
// the store fails the way it does after the file went away under a running
// process. Close stays safe: closing a closed pool is a no-op.
func breakStore(t *testing.T, s *Store) {
	t.Helper()
	if err := s.writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultManager_StoreUnavailable(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(file, "memory"))
	mgr, err := DefaultManager()
	if err == nil {
		mgr.Close()
		t.Fatal("DefaultManager under a regular file succeeded")
	}
	wantErr(t, "DefaultManager", err, "mkdir")
}

// Every manager entry point reports a failing database instead of returning
// empty results.
func TestManager_DatabaseFailures(t *testing.T) {
	s := openTestStore(t)
	mgr := NewManager(s)
	mgr.MediumTermMaxAge = time.Hour
	breakStore(t, s)

	calls := map[string]func() error{
		"SearchScoped": func() error { _, err := mgr.SearchScoped("repo", "q"); return err },
		"ListScoped":   func() error { _, err := mgr.ListScoped("repo", TierLong); return err },
		"BuildContext": func() error { _, err := mgr.BuildContext(); return err },
		"BuildScopedContext": func() error {
			_, err := mgr.BuildScopedContext("repo")
			return err
		},
		"Forget":         func() error { _, err := mgr.Forget("some phrase"); return err },
		"RunMaintenance": mgr.RunMaintenance,
		"RecordAccess":   func() error { return mgr.RecordAccess("missing") },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s on a closed database returned nil error", name)
		}
	}

	// Callers that cannot report an error degrade to "nothing found" rather
	// than guessing.
	if got := mgr.checkContradictions("anything is not true"); got != nil {
		t.Errorf("checkContradictions = %v, want nil", got)
	}
	if stale, err := mgr.StaleBlocks(); err != nil || len(stale) != 0 {
		t.Errorf("StaleBlocks = %v, %v", stale, err)
	}
	if n, err := mgr.Compact(0.5); err != nil || n != 0 {
		t.Errorf("Compact = %d, %v", n, err)
	}
}

// Two stored blocks that the new content contradicts are both flagged and
// listed in the new block's metadata, even when the caller gave it none.
func TestRememberBlock_SeveralContradictions(t *testing.T) {
	s := openTestStore(t)
	mgr := NewManager(s)
	a, err := mgr.Remember("Database standard is MySQL", "database")
	if err != nil {
		t.Fatal(err)
	}
	b, err := mgr.Remember("Database engines is MySQL", "database")
	if err != nil {
		t.Fatal(err)
	}

	saved, err := mgr.RememberBlock(&Block{Tier: TierLong, Label: "database", Content: "Database standard is not MySQL"})
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Split(saved.Metadata["contradicts"], ",")
	if saved.Metadata["has_contradictions"] != "true" || len(ids) != 2 {
		t.Fatalf("metadata = %v, want two contradicted ids", saved.Metadata)
	}
	for _, id := range []string{a.ID, b.ID} {
		old, err := s.Get(id)
		if err != nil || old.Conflicts != 1 {
			t.Fatalf("block %s = %+v, %v; want its conflict counted once", id, old, err)
		}
	}
}

func TestRevertReflection_ChangeSetNotDeletable(t *testing.T) {
	s := openTestStore(t)
	mgr := NewManager(s)
	if err := s.SaveChangeSet(&ChangeSet{RunID: "run-1", Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	abortOn(t, s, "DELETE", "reflection_runs")
	_, err := mgr.RevertReflection("run-1")
	wantErr(t, "RevertReflection", err, "reverting reflection run run-1")
	if _, err := s.GetChangeSet("run-1"); err != nil {
		t.Fatalf("change set lost although the revert failed: %v", err)
	}
}

// Compact with the default threshold drops short-term notes whose confidence
// decayed away and keeps the fresh ones.
func TestCompact_DefaultThresholdPrunesStaleShortTerm(t *testing.T) {
	s := openTestStore(t)
	mgr := NewManager(s)
	long := time.Now().Add(-2 * 365 * 24 * time.Hour)
	s.shortTerm = []Block{
		{ID: "stale", Tier: TierShort, Content: "old note", UpdatedAt: long},
		{ID: "fresh", Tier: TierShort, Content: "new note", UpdatedAt: time.Now()},
	}
	n, err := mgr.Compact(0)
	if err != nil || n != 1 {
		t.Fatalf("Compact(0) = %d, %v; want 1 pruned", n, err)
	}
	if len(s.shortTerm) != 1 || s.shortTerm[0].ID != "fresh" {
		t.Fatalf("shortTerm = %+v", s.shortTerm)
	}
}

// EndSession drops expired short-term notes, promotes the rest, and keeps a
// note whose promotion failed so it is not lost.
func TestEndSession_PromotionSkipsExpiredAndKeepsFailures(t *testing.T) {
	s := openTestStore(t)
	mgr := NewManager(s)
	mgr.AutoPromote = true
	past := time.Now().Add(-time.Hour)
	s.shortTerm = []Block{
		{ID: "gone", Tier: TierShort, Content: "expired note", ExpiresAt: &past},
		{ID: "keep", Tier: TierShort, Content: "note that cannot be promoted"},
	}
	abortOn(t, s, "INSERT", "blocks")
	if err := mgr.EndSession(); err != nil {
		t.Fatal(err)
	}
	if len(s.shortTerm) != 1 || s.shortTerm[0].Content != "note that cannot be promoted" {
		t.Fatalf("shortTerm = %+v, want only the unpromotable note", s.shortTerm)
	}
}
