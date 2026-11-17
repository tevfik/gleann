package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SQLite keeps a value of the wrong type in a column (flexible typing), so a
// damaged or hand-edited row can hold text where the store expects an
// integer. Every reader must report such a row instead of skipping it or
// reading it as zero.
func TestDamagedRowsAreReported(t *testing.T) {
	ctx := context.Background()

	t.Run("files", func(t *testing.T) {
		s := openTestStore(t)
		applySlotted(t, s, "a.go", "h", 0)
		if _, err := s.WriterDB().Exec(`UPDATE files SET size = 'big'`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Files(ctx); err == nil || !strings.Contains(err.Error(), "listing files") {
			t.Fatalf("Files err = %v", err)
		}
		tx, err := s.BeginWrite(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := s.ReplacePassagesTx(ctx, tx, nil, RebuildHooks{}); err == nil || !strings.Contains(err.Error(), "listing files") {
			t.Fatalf("ReplacePassagesTx err = %v", err)
		}
	})

	t.Run("vec_slot", func(t *testing.T) {
		s := openTestStore(t)
		ids := applySlotted(t, s, "a.go", "h", 0, 1)
		if _, err := s.WriterDB().Exec(`UPDATE passages SET vec_slot = 'x' WHERE vec_slot = 1`); err != nil {
			t.Fatal(err)
		}
		checks := map[string]error{}
		_, checks["AllSlots"] = s.AllSlots(ctx)
		_, _, checks["SourceSlots"] = s.SourceSlots(ctx, []string{"a.go"})
		_, checks["GetMany"] = s.GetMany(ctx, ids)
		_, checks["SelectByPredicate"] = s.SelectByPredicate(ctx, "1 = 1", nil, 0)
		checks["ForEach"] = s.ForEach(ctx, func(Passage) error { return nil })
		for name, err := range checks {
			if err == nil {
				t.Errorf("%s read a text vec_slot without an error", name)
			}
		}
	})
}

// ForEach stops at the first callback error and returns it.
func TestForEachStopsAtCallbackError(t *testing.T) {
	s := openTestStore(t)
	applySlotted(t, s, "a.go", "h", 0, 1, 2)
	seen := 0
	err := s.ForEach(context.Background(), func(Passage) error {
		seen++
		if seen == 2 {
			return os.ErrDeadlineExceeded
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "iterating passages") || seen != 2 {
		t.Fatalf("err = %v after %d passages, want the callback error after 2", err, seen)
	}
}

func TestAcquireLockOnDirectoryFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx.lock")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(dir); err == nil || !strings.Contains(err.Error(), "opening lock file") {
		t.Fatalf("err = %v", err)
	}
}
