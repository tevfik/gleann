package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMarkdownRoundTripEveryFieldAndTier(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "store1", "memory.db")
	s1, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s1.Close()

	// Create blocks covering every tier and all fields.
	now := time.Now().UTC().Truncate(time.Microsecond)
	tExpires := now.Add(48 * time.Hour)
	tAccessed := now.Add(-2 * time.Hour)

	bLong := Block{
		ID:             "blk-long-full-001",
		Tier:           TierLong,
		Label:          "user_preference",
		Content:        "Persistent architectural fact with code link.",
		Source:         "user",
		Tags:           []string{"architecture", "storage"},
		Metadata:       map[string]string{"priority": "high", "env": "prod"},
		CreatedAt:      now.Add(-24 * time.Hour),
		UpdatedAt:      now.Add(-1 * time.Hour),
		ExpiresAt:      &tExpires,
		CharLimit:      500,
		Scope:          "github.com/tevfik/gleann/backend",
		Repo:           "github.com/tevfik/gleann",
		Paths:          []string{"pkg/memory/store.go", "pkg/memory/sqlite.go"},
		Symbols:        []string{"memory.Store.Add", "memory.Store.Get"},
		Commit:         "9f8e7d6c5b4a",
		Suspect:        true,
		StaleReason:    "refactored in commit 9f8e7d",
		Confirms:       5,
		Conflicts:      2,
		AccessCount:    17,
		LastAccessedAt: &tAccessed,
	}

	bMedium := Block{
		ID:        "blk-medium-002",
		Tier:      TierMedium,
		Label:     "daily_digest",
		Content:   "Sprint 14 digest: performance improvements completed.",
		Source:    "auto_summary",
		Tags:      []string{"sprint-14"},
		Metadata:  map[string]string{"cycle": "daily"},
		CreatedAt: now.Add(-12 * time.Hour),
		UpdatedAt: now.Add(-6 * time.Hour),
	}

	bShort := Block{
		ID:        "blk-short-003",
		Tier:      TierShort,
		Label:     "session_note",
		Content:   "Ephemeral context note for active session.",
		Source:    "session",
		CreatedAt: now.Add(-10 * time.Minute),
		UpdatedAt: now.Add(-5 * time.Minute),
	}

	for _, b := range []*Block{&bLong, &bMedium, &bShort} {
		if err := s1.Add(b); err != nil {
			t.Fatalf("Add block %s failed: %v", b.ID, err)
		}
	}

	sum1 := Summary{
		ConversationID: "conv-roundtrip-42",
		Title:          "Memory Files Architecture Review",
		Content:        "Detailed discussion of markdown export and import architecture.",
		MessageCount:   28,
		IndexNames:     []string{"gleann", "docs"},
		Model:          "qwen3.5:9b",
		CreatedAt:      now.Add(-3 * time.Hour),
	}
	if err := s1.SaveSummary(&sum1); err != nil {
		t.Fatalf("SaveSummary failed: %v", err)
	}

	exportDir := filepath.Join(tmpDir, "exported")
	stats, err := s1.Export(exportDir)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if stats.Blocks != 3 {
		t.Errorf("Export stats.Blocks = %d, want 3", stats.Blocks)
	}
	if stats.Summaries != 1 {
		t.Errorf("Export stats.Summaries = %d, want 1", stats.Summaries)
	}

	// Verify files were created on disk.
	entries, err := os.ReadDir(exportDir)
	if err != nil {
		t.Fatalf("ReadDir exportDir failed: %v", err)
	}
	var mdCount int
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			mdCount++
		}
	}
	if mdCount != 3 {
		t.Errorf("found %d markdown files in exportDir, want 3", mdCount)
	}

	sumEntries, err := os.ReadDir(filepath.Join(exportDir, "summaries"))
	if err != nil {
		t.Fatalf("ReadDir summaries failed: %v", err)
	}
	if len(sumEntries) != 1 {
		t.Errorf("found %d summary files, want 1", len(sumEntries))
	}

	// Now open a brand new store and import into it.
	storePath2 := filepath.Join(tmpDir, "store2", "memory.db")
	s2, err := OpenStore(storePath2)
	if err != nil {
		t.Fatalf("OpenStore2 failed: %v", err)
	}
	defer s2.Close()

	impStats, err := s2.Import(exportDir)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}
	if impStats.BlocksImported != 3 {
		t.Errorf("Import stats.BlocksImported = %d, want 3", impStats.BlocksImported)
	}
	if impStats.SummariesImported != 1 {
		t.Errorf("Import stats.SummariesImported = %d, want 1", impStats.SummariesImported)
	}
	if impStats.Conflicts != 0 {
		t.Errorf("Import stats.Conflicts = %d, want 0", impStats.Conflicts)
	}

	// Verify all blocks in s2 match original fields losslessly.
	for _, orig := range []*Block{&bLong, &bMedium, &bShort} {
		got, err := s2.Get(orig.ID)
		if err != nil {
			t.Fatalf("Get block %s from new store failed: %v", orig.ID, err)
		}
		if got.ID != orig.ID {
			t.Errorf("ID mismatch: got %q, want %q", got.ID, orig.ID)
		}
		if got.Tier != orig.Tier {
			t.Errorf("Tier mismatch: got %q, want %q", got.Tier, orig.Tier)
		}
		if got.Label != orig.Label {
			t.Errorf("Label mismatch: got %q, want %q", got.Label, orig.Label)
		}
		if got.Content != orig.Content {
			t.Errorf("Content mismatch: got %q, want %q", got.Content, orig.Content)
		}
		if got.Source != orig.Source {
			t.Errorf("Source mismatch: got %q, want %q", got.Source, orig.Source)
		}
		if !slicesEqual(got.Tags, orig.Tags) {
			t.Errorf("Tags mismatch: got %v, want %v", got.Tags, orig.Tags)
		}
		if !mapsEqual(got.Metadata, orig.Metadata) {
			t.Errorf("Metadata mismatch: got %v, want %v", got.Metadata, orig.Metadata)
		}
		if !got.CreatedAt.Equal(orig.CreatedAt) {
			t.Errorf("CreatedAt mismatch: got %v, want %v", got.CreatedAt, orig.CreatedAt)
		}
		if !got.UpdatedAt.Equal(orig.UpdatedAt) {
			t.Errorf("UpdatedAt mismatch: got %v, want %v", got.UpdatedAt, orig.UpdatedAt)
		}
		if !timePtrEqual(got.ExpiresAt, orig.ExpiresAt) {
			t.Errorf("ExpiresAt mismatch: got %v, want %v", got.ExpiresAt, orig.ExpiresAt)
		}
		if got.CharLimit != orig.CharLimit {
			t.Errorf("CharLimit mismatch: got %d, want %d", got.CharLimit, orig.CharLimit)
		}
		if got.Scope != orig.Scope {
			t.Errorf("Scope mismatch: got %q, want %q", got.Scope, orig.Scope)
		}
		if got.Repo != orig.Repo {
			t.Errorf("Repo mismatch: got %q, want %q", got.Repo, orig.Repo)
		}
		if !slicesEqual(got.Paths, orig.Paths) {
			t.Errorf("Paths mismatch: got %v, want %v", got.Paths, orig.Paths)
		}
		if !slicesEqual(got.Symbols, orig.Symbols) {
			t.Errorf("Symbols mismatch: got %v, want %v", got.Symbols, orig.Symbols)
		}
		if got.Commit != orig.Commit {
			t.Errorf("Commit mismatch: got %q, want %q", got.Commit, orig.Commit)
		}
		if got.Suspect != orig.Suspect {
			t.Errorf("Suspect mismatch: got %v, want %v", got.Suspect, orig.Suspect)
		}
		if got.StaleReason != orig.StaleReason {
			t.Errorf("StaleReason mismatch: got %q, want %q", got.StaleReason, orig.StaleReason)
		}
		if got.Confirms != orig.Confirms {
			t.Errorf("Confirms mismatch: got %d, want %d", got.Confirms, orig.Confirms)
		}
		if got.Conflicts != orig.Conflicts {
			t.Errorf("Conflicts mismatch: got %d, want %d", got.Conflicts, orig.Conflicts)
		}
		if got.AccessCount != orig.AccessCount {
			t.Errorf("AccessCount mismatch: got %d, want %d", got.AccessCount, orig.AccessCount)
		}
		if !timePtrEqual(got.LastAccessedAt, orig.LastAccessedAt) {
			t.Errorf("LastAccessedAt mismatch: got %v, want %v", got.LastAccessedAt, orig.LastAccessedAt)
		}
	}

	// Verify summaries in s2.
	sums, err := s2.ListSummaries()
	if err != nil {
		t.Fatalf("ListSummaries failed: %v", err)
	}
	if len(sums) != 1 {
		t.Fatalf("len(sums) = %d, want 1", len(sums))
	}
	if sums[0].ConversationID != sum1.ConversationID {
		t.Errorf("Summary ConversationID mismatch: got %q, want %q", sums[0].ConversationID, sum1.ConversationID)
	}
	if sums[0].Title != sum1.Title {
		t.Errorf("Summary Title mismatch: got %q, want %q", sums[0].Title, sum1.Title)
	}
	if sums[0].Content != sum1.Content {
		t.Errorf("Summary Content mismatch: got %q, want %q", sums[0].Content, sum1.Content)
	}
	if sums[0].MessageCount != sum1.MessageCount {
		t.Errorf("Summary MessageCount mismatch: got %d, want %d", sums[0].MessageCount, sum1.MessageCount)
	}
	if !reflect.DeepEqual(sums[0].IndexNames, sum1.IndexNames) {
		t.Errorf("Summary IndexNames mismatch: got %v, want %v", sums[0].IndexNames, sum1.IndexNames)
	}
	if sums[0].Model != sum1.Model {
		t.Errorf("Summary Model mismatch: got %q, want %q", sums[0].Model, sum1.Model)
	}
	if !sums[0].CreatedAt.Equal(sum1.CreatedAt) {
		t.Errorf("Summary CreatedAt mismatch: got %v, want %v", sums[0].CreatedAt, sum1.CreatedAt)
	}
}

func TestMarkdownRoundTripEdgeContent(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "memory.db")
	s, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// 1. Unicode content
	bUnicode := Block{
		ID:        "blk-unicode",
		Tier:      TierLong,
		Label:     "i18n_test",
		Content:   "Türkçe: ğüşıöç ĞÜŞİÖÇ. 中文：你好世界。Emoji: 🚀🧠🔍. Special: <tag> & 'single' \"double\" `backtick`.",
		Source:    "user",
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}

	// 2. Newlines content (leading, trailing, and multiple internal empty lines)
	bNewlines := Block{
		ID:        "blk-newlines",
		Tier:      TierLong,
		Label:     "newlines_test",
		Content:   "\n\nParagraph 1 starts after newlines.\n\n\nParagraph 2 has blank lines above.\nTrailing newlines below.\n\n",
		Source:    "user",
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}

	// 3. Front-matter-looking content (has --- delimiters in body)
	bFrontMatterLooking := Block{
		ID:    "blk-yaml-body",
		Tier:  TierLong,
		Label: "yaml_body_test",
		Content: `---
fake_yaml: true
title: Nested Front Matter
tags: [one, two]
---
This content body literally contains markdown front matter syntax.
---
Another horizontal divider line
`,
		Source:    "user",
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}

	for _, b := range []*Block{&bUnicode, &bNewlines, &bFrontMatterLooking} {
		if err := s.Add(b); err != nil {
			t.Fatalf("Add block %s failed: %v", b.ID, err)
		}
	}

	exportDir := filepath.Join(tmpDir, "exported")
	if _, err := s.Export(exportDir); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Import into fresh store
	s2, err := OpenStore(filepath.Join(tmpDir, "store2", "memory.db"))
	if err != nil {
		t.Fatalf("OpenStore2 failed: %v", err)
	}
	defer s2.Close()

	if _, err := s2.Import(exportDir); err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	for _, orig := range []*Block{&bUnicode, &bNewlines, &bFrontMatterLooking} {
		got, err := s2.Get(orig.ID)
		if err != nil {
			t.Fatalf("Get block %s failed: %v", orig.ID, err)
		}
		if got.Content != orig.Content {
			t.Errorf("Block %s content mismatch:\nGOT:\n%q\nWANT:\n%q", orig.ID, got.Content, orig.Content)
		}
	}
}

func TestImportIdempotentAndConflicts(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "memory.db")
	s, err := OpenStore(storePath)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	t1 := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	b1 := Block{
		ID:        "blk-conflict-test",
		Tier:      TierLong,
		Label:     "convention",
		Content:   "Original convention text",
		Source:    "user",
		CreatedAt: t1,
		UpdatedAt: t1,
		Confirms:  2,
	}
	if err := s.Add(&b1); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	exportDir := filepath.Join(tmpDir, "exported")
	if _, err := s.Export(exportDir); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// 1. Re-import into same store: MUST be idempotent (0 changes, 0 conflicts)
	impStats, err := s.Import(exportDir)
	if err != nil {
		t.Fatalf("Re-import failed: %v", err)
	}
	if impStats.BlocksImported != 0 {
		t.Errorf("BlocksImported = %d, want 0", impStats.BlocksImported)
	}
	if impStats.BlocksUpdated != 0 {
		t.Errorf("BlocksUpdated = %d, want 0", impStats.BlocksUpdated)
	}
	if impStats.BlocksUnchanged != 1 {
		t.Errorf("BlocksUnchanged = %d, want 1", impStats.BlocksUnchanged)
	}
	if impStats.Conflicts != 0 {
		t.Errorf("Conflicts = %d, want 0", impStats.Conflicts)
	}

	checkB, _ := s.Get(b1.ID)
	if checkB.Content != b1.Content || checkB.Confirms != b1.Confirms {
		t.Errorf("Re-import modified block in place! Got content %q, confirms %d", checkB.Content, checkB.Confirms)
	}

	// 2. Modify file in exportDir to have NEWER updated_at -> Conflict, kept incoming
	tBase := checkB.UpdatedAt
	t2 := tBase.Add(2 * time.Hour)
	newerBlock := *checkB
	newerBlock.Content = "Updated convention text by newer file"
	newerBlock.UpdatedAt = t2

	mdData, err := BlockToMarkdown(&newerBlock)
	if err != nil {
		t.Fatalf("BlockToMarkdown failed: %v", err)
	}
	// Write over the exported file
	safeName := safeBlockFilename(b1.ID)
	if err := os.WriteFile(filepath.Join(exportDir, safeName), mdData, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	impStats2, err := s.Import(exportDir)
	if err != nil {
		t.Fatalf("Import newer failed: %v", err)
	}
	if impStats2.Conflicts != 1 {
		t.Errorf("Conflicts = %d, want 1", impStats2.Conflicts)
	}
	if impStats2.BlocksUpdated != 1 {
		t.Errorf("BlocksUpdated = %d, want 1", impStats2.BlocksUpdated)
	}
	checkB2, _ := s.Get(b1.ID)
	if checkB2.Content != "Updated convention text by newer file" {
		t.Errorf("Expected updated content from newer file, got %q", checkB2.Content)
	}
	if !checkB2.UpdatedAt.Equal(t2) {
		t.Errorf("Expected updated UpdatedAt %v, got %v", t2, checkB2.UpdatedAt)
	}

	// 3. Modify file in exportDir to have OLDER updated_at (tOlder < t2) -> Conflict, kept existing
	tOlder := tBase.Add(-2 * time.Hour)
	olderBlock := *checkB
	olderBlock.Content = "Stale convention text from old backup"
	olderBlock.UpdatedAt = tOlder // older than current in DB (which is t2)

	mdDataOlder, err := BlockToMarkdown(&olderBlock)
	if err != nil {
		t.Fatalf("BlockToMarkdown older failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(exportDir, safeName), mdDataOlder, 0o644); err != nil {
		t.Fatalf("WriteFile older failed: %v", err)
	}

	impStats3, err := s.Import(exportDir)
	if err != nil {
		t.Fatalf("Import older failed: %v", err)
	}
	if impStats3.Conflicts != 1 {
		t.Errorf("Conflicts = %d, want 1", impStats3.Conflicts)
	}
	if impStats3.BlocksUpdated != 0 {
		t.Errorf("BlocksUpdated = %d, want 0", impStats3.BlocksUpdated)
	}
	if impStats3.BlocksUnchanged != 1 {
		t.Errorf("BlocksUnchanged = %d, want 1", impStats3.BlocksUnchanged)
	}
	checkB3, _ := s.Get(b1.ID)
	if checkB3.Content != "Updated convention text by newer file" {
		t.Errorf("Expected existing newer content to be preserved, got %q", checkB3.Content)
	}
	if !checkB3.UpdatedAt.Equal(t2) {
		t.Errorf("Expected existing UpdatedAt %v preserved, got %v", t2, checkB3.UpdatedAt)
	}
}

func TestWindowsSafeFilenames(t *testing.T) {
	cases := []struct {
		id     string
		prefix string
	}{
		{"CON", "_CON-"},
		{"aux", "_aux-"},
		{"prn", "_prn-"},
		{"NUL", "_NUL-"},
		{"com1", "_com1-"},
		{"LPT3", "_LPT3-"},
		{"normal-id", "normal-id-"},
		{"id/with/slashes", "id_with_slashes-"},
		{"id:with:colons", "id_with_colons-"},
		{"id*with?wildcards", "id_with_wildcards-"},
		{"trailing.dot.", "trailing.dot-"},
		{"trailing space ", "trailing space-"},
	}

	for _, tc := range cases {
		got := safeBlockFilename(tc.id)
		if !strings.HasPrefix(got, tc.prefix) || !strings.HasSuffix(got, ".md") || len(got) != len(tc.prefix)+8+3 {
			t.Errorf("safeBlockFilename(%q) = %q, want %q + 8 hex + .md", tc.id, got, tc.prefix)
		}
	}
}

// IDs that sanitize alike or differ only in case must not share a file: on
// Windows and macOS "Ab.md" and "ab.md" are the same file, and the export
// would silently keep only one of them.
func TestFilenamesNeverCollide(t *testing.T) {
	ids := []string{"a:b", "a_b", "a/b", "Ab", "ab", "AB", strings.Repeat("x", 300), strings.Repeat("x", 301)}
	seen := map[string]string{}
	for _, id := range ids {
		name := strings.ToLower(safeBlockFilename(id))
		if other, ok := seen[name]; ok {
			t.Fatalf("IDs %q and %q share the file name %q", other, id, name)
		}
		seen[name] = id
		if n := len([]rune(name)); n > maxNameRunes+12 {
			t.Errorf("file name for a %d-rune ID has %d runes", len(id), n)
		}
	}
	t0 := time.Date(2026, 11, 16, 12, 0, 0, 1, time.UTC)
	if safeSummaryFilename(t0, "c") == safeSummaryFilename(t0.Add(time.Millisecond), "c") {
		t.Fatal("two summaries of one conversation in the same second share a file name")
	}
}
