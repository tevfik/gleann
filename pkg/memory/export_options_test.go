package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedExportBlocks stores one block per interesting category and returns the
// store plus the block IDs.
func seedExportBlocks(t *testing.T) (*Store, map[string]string) {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	past := time.Now().Add(-time.Hour)
	ids := map[string]string{}
	for _, b := range []Block{
		{ID: "exp-global", Tier: TierLong, Label: "fact", Content: "zz global fact", Scope: ""},
		{ID: "exp-repo-a", Tier: TierLong, Label: "fact", Content: "zz repo-a fact", Scope: "github.com/org/repo-a"},
		{ID: "exp-repo-b", Tier: TierLong, Label: "fact", Content: "zz repo-b fact", Scope: "github.com/org/repo-b"},
		{ID: "exp-short", Tier: TierShort, Label: "note", Content: "zz short note", Scope: ""},
		{ID: "exp-expired", Tier: TierLong, Label: "fact", Content: "zz expired fact", Scope: "", ExpiresAt: &past},
	} {
		b := b
		if err := s.Add(&b); err != nil {
			t.Fatal(err)
		}
		ids[b.ID] = safeBlockFilename(b.ID)
	}
	return s, ids
}

func exportedFiles(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			out[e.Name()] = true
		}
	}
	return out
}

// The CLI default export skips short-term and expired blocks; the include
// flags and the plain lossless Export widen it back to the full dump.
func TestExportOptionsShortAndExpired(t *testing.T) {
	s, ids := seedExportBlocks(t)
	dir := t.TempDir()

	stats, err := s.ExportWithOptions(dir, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Blocks != 3 {
		t.Fatalf("default export wrote %d blocks, want 3 (no short, no expired)", stats.Blocks)
	}
	files := exportedFiles(t, dir)
	for _, id := range []string{"exp-global", "exp-repo-a", "exp-repo-b"} {
		if !files[ids[id]] {
			t.Errorf("default export missing %s", id)
		}
	}
	for _, id := range []string{"exp-short", "exp-expired"} {
		if files[ids[id]] {
			t.Errorf("default export must skip %s", id)
		}
	}

	full, err := s.ExportWithOptions(dir, ExportOptions{IncludeShort: true, IncludeExpired: true})
	if err != nil {
		t.Fatal(err)
	}
	if full.Blocks != 5 {
		t.Fatalf("full export wrote %d blocks, want 5", full.Blocks)
	}
	// The plain Export stays the lossless full dump.
	plain, err := s.Export(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if plain.Blocks != 5 {
		t.Fatalf("plain Export wrote %d blocks, want 5", plain.Blocks)
	}
}

// --scope narrows the export to the scope plus global and its path ancestors;
// the all sentinel exports every scope.
func TestExportOptionsScopeFilter(t *testing.T) {
	s, ids := seedExportBlocks(t)

	for _, tc := range []struct {
		scope string
		want  []string
	}{
		{"github.com/org/repo-a", []string{"exp-global", "exp-repo-a"}},
		// Like reads, an ancestor scope sees its own blocks plus global, not
		// the blocks of its children.
		{"github.com/org", []string{"exp-global"}},
		{"github.com/org/repo-b", []string{"exp-global", "exp-repo-b"}},
		{"all", []string{"exp-global", "exp-repo-a", "exp-repo-b"}},
		{"", []string{"exp-global", "exp-repo-a", "exp-repo-b"}},
	} {
		dir := t.TempDir()
		stats, err := s.ExportWithOptions(dir, ExportOptions{Scope: tc.scope})
		if err != nil {
			t.Fatal(err)
		}
		if stats.Blocks != len(tc.want) {
			t.Errorf("scope %q exported %d blocks, want %d", tc.scope, stats.Blocks, len(tc.want))
		}
		files := exportedFiles(t, dir)
		for _, id := range tc.want {
			if !files[ids[id]] {
				t.Errorf("scope %q missing %s", tc.scope, id)
			}
		}
		if files[ids["exp-repo-b"]] && tc.scope == "github.com/org/repo-a" {
			t.Errorf("scope %q leaked repo-b", tc.scope)
		}
	}
}

// A re-export prunes the files the previous run of the same scope wrote but
// this one no longer writes — and nothing else: not the user's own files, not
// another scope's bucket.
func TestExportManifestPrune(t *testing.T) {
	s, ids := seedExportBlocks(t)
	dir := t.TempDir()

	// The user's own file shares the backup folder.
	own := filepath.Join(dir, "my-notes.md")
	if err := os.WriteFile(own, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Export(dir); err != nil { // full dump, bucket "all"
		t.Fatal(err)
	}
	if err := s.Delete("exp-repo-b"); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Export(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pruned != 1 {
		t.Fatalf("Pruned = %d, want 1 (forgotten repo-b file)", stats.Pruned)
	}
	files := exportedFiles(t, dir)
	if files[ids["exp-repo-b"]] {
		t.Error("stale repo-b file survived the re-export")
	}
	if !files["my-notes.md"] {
		t.Error("the user's own file was pruned")
	}
	if !files[exportManifestName] {
		t.Error("manifest missing after export")
	}

	// A scoped run only prunes its own bucket: repo-b's file is already gone,
	// so forget the global block and check a repo-a run leaves repo-a's file
	// alone while an unrelated scope's files stay untouched.
	if _, err := s.ExportWithOptions(dir, ExportOptions{Scope: "github.com/org/repo-a"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("exp-global"); err != nil {
		t.Fatal(err)
	}
	scoped, err := s.ExportWithOptions(dir, ExportOptions{Scope: "github.com/org/repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Pruned != 1 {
		t.Fatalf("scoped Pruned = %d, want 1 (global file dropped from the repo-a bucket)", scoped.Pruned)
	}
	files = exportedFiles(t, dir)
	if !files[ids["exp-repo-a"]] {
		t.Error("scoped re-export pruned its own live block")
	}
	if !files["my-notes.md"] {
		t.Error("scoped run touched the user's file")
	}

	// The manifest is valid JSON keyed by scope.
	data, err := os.ReadFile(filepath.Join(dir, exportManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var m exportManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m.Version != exportManifestVersion || len(m.Files["all"]) == 0 || len(m.Files["github.com/org/repo-a"]) == 0 {
		t.Errorf("manifest buckets wrong: %+v", m)
	}
}

// A hand-crafted manifest must never delete files outside the export
// directory or files it does not list.
func TestExportManifestRejectsUnsafePaths(t *testing.T) {
	s, _ := seedExportBlocks(t)
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	manifest := exportManifest{
		Version: exportManifestVersion,
		Files: map[string][]string{
			"all": {"../outside.md", outside, "never-written.md", ""},
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Export(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("export deleted a file outside the export directory: %v", err)
	}
}

// A corrupt or missing manifest is treated as empty: nothing is pruned.
func TestExportManifestCorruptIsSafe(t *testing.T) {
	s, ids := seedExportBlocks(t)
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, exportManifestName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stale file gleann did not record must survive.
	stale := filepath.Join(dir, ids["exp-repo-b"])
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := s.ExportWithOptions(dir, ExportOptions{Scope: "github.com/org/repo-a"})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Pruned != 0 {
		t.Fatalf("Pruned = %d with a corrupt manifest, want 0", stats.Pruned)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("corrupt manifest caused a prune: %v", err)
	}
}
