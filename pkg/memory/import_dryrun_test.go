package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --dry-run previews exactly what a real import would do and writes nothing:
// no persisted rows, no short-term cache entries, no summaries.
func TestImportDryRunWritesNothing(t *testing.T) {
	src, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	t1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	for _, b := range []Block{
		{ID: "dry-long", Tier: TierLong, Label: "fact", Content: "zz dry long", CreatedAt: t1, UpdatedAt: t1},
		{ID: "dry-short", Tier: TierShort, Label: "note", Content: "zz dry short", CreatedAt: t1, UpdatedAt: t1},
	} {
		b := b
		if err := src.Add(&b); err != nil {
			t.Fatal(err)
		}
	}
	if err := src.SaveSummary(&Summary{ConversationID: "conv-dry", Title: "Dry", Content: "zz dry summary", CreatedAt: t1}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := src.Export(dir); err != nil { // lossless: includes the short block
		t.Fatal(err)
	}

	dst, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	preview, err := dst.ImportWithOptions(dir, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.DryRun || preview.BlocksTotal != 2 || preview.BlocksImported != 2 || preview.SummariesImported != 1 {
		t.Fatalf("preview = %+v, want 2 would-import + 1 summary", preview)
	}
	if blocks, err := dst.List(""); err != nil || len(blocks) != 0 {
		t.Fatalf("dry run persisted %d blocks (err %v), want 0", len(blocks), err)
	}
	if sums, err := dst.ListSummaries(); err != nil || len(sums) != 0 {
		t.Fatalf("dry run persisted %d summaries (err %v), want 0", len(sums), err)
	}

	// The real run reports the same numbers and actually writes.
	real, err := dst.Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	if real.BlocksImported != preview.BlocksImported || real.SummariesImported != preview.SummariesImported {
		t.Fatalf("real run %+v differs from preview %+v", real, preview)
	}
	if blocks, _ := dst.List(""); len(blocks) != 2 {
		t.Fatalf("real import stored %d blocks, want 2", len(blocks))
	}

	// A second preview is a no-op, just like a second real import.
	again, err := dst.ImportWithOptions(dir, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.BlocksImported != 0 || again.BlocksUnchanged != 2 || again.Conflicts != 0 {
		t.Fatalf("second preview = %+v, want 2 unchanged", again)
	}
}

// A hand-edited export file whose updated_at did not move forward is reported
// by name in both the preview and the real run, and the stored block wins
// until the file's updated_at is raised.
func TestImportHandEditedReport(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	b := Block{ID: "hand-edit", Tier: TierLong, Label: "fact", Content: "zz stored text", CreatedAt: t1, UpdatedAt: t1}
	if err := s.Add(&b); err != nil {
		t.Fatal(err)
	}
	// Add stamps UpdatedAt with the write time; edits must compare against
	// what is actually stored.
	storedB, err := s.Get(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := s.Export(dir); err != nil {
		t.Fatal(err)
	}
	fpath := filepath.Join(dir, safeBlockFilename(b.ID))

	// Edit the content by hand, leave updated_at where it was.
	edited := *storedB
	edited.Content = "zz hand-edited text"
	md, err := BlockToMarkdown(&edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fpath, md, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, dry := range []bool{true, false} {
		stats, err := s.ImportWithOptions(dir, ImportOptions{DryRun: dry})
		if err != nil {
			t.Fatal(err)
		}
		if stats.HandEdited != 1 || len(stats.HandEditedFiles) != 1 || stats.HandEditedFiles[0] != safeBlockFilename(b.ID) {
			t.Fatalf("dry=%v stats = %+v, want the hand-edited file reported", dry, stats)
		}
		if stats.BlocksUpdated != 0 || stats.BlocksUnchanged != 1 || stats.Conflicts != 1 {
			t.Fatalf("dry=%v stats = %+v, want unchanged + 1 conflict", dry, stats)
		}
		stored, err := s.Get(b.ID)
		if err != nil || stored.Content != "zz stored text" {
			t.Fatalf("dry=%v stored = %q (err %v), want the stored text kept", dry, stored.Content, err)
		}
	}

	// Raising updated_at imports the edit.
	edited.UpdatedAt = storedB.UpdatedAt.Add(time.Hour)
	md, err = BlockToMarkdown(&edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fpath, md, 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stats.BlocksUpdated != 1 || stats.HandEdited != 0 {
		t.Fatalf("raised updated_at: %+v, want 1 update and no hand-edit report", stats)
	}
	stored, _ := s.Get(b.ID)
	if stored.Content != "zz hand-edited text" {
		t.Fatalf("stored = %q, want the hand edit imported", stored.Content)
	}
}

// The preview counts updates and conflicts exactly like the real run.
func TestImportDryRunUpdateParity(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	b := Block{ID: "dry-update", Tier: TierLong, Label: "fact", Content: "zz before", CreatedAt: t1, UpdatedAt: t1}
	if err := s.Add(&b); err != nil {
		t.Fatal(err)
	}
	storedB, err := s.Get(b.ID) // Add stamps UpdatedAt with the write time
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := s.Export(dir); err != nil {
		t.Fatal(err)
	}

	newer := *storedB
	newer.Content = "zz after"
	newer.UpdatedAt = storedB.UpdatedAt.Add(2 * time.Hour)
	md, err := BlockToMarkdown(&newer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, safeBlockFilename(b.ID)), md, 0o644); err != nil {
		t.Fatal(err)
	}

	preview, err := s.ImportWithOptions(dir, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if preview.BlocksUpdated != 1 || preview.Conflicts != 1 || preview.BlocksImported != 0 {
		t.Fatalf("preview = %+v, want 1 update + 1 conflict", preview)
	}
	if stored, _ := s.Get(b.ID); stored.Content != "zz before" {
		t.Fatalf("preview wrote the update: %q", stored.Content)
	}
	real, err := s.Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	if real.BlocksUpdated != preview.BlocksUpdated || real.Conflicts != preview.Conflicts {
		t.Fatalf("real %+v differs from preview %+v", real, preview)
	}
}
