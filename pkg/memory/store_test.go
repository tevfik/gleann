package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "test_memory.db")
}

func TestStoreAddAndGet(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Add long-term block.
	block := &Block{
		Tier:    TierLong,
		Label:   "test",
		Content: "hello world",
		Source:  "user",
	}
	if err := s.Add(block); err != nil {
		t.Fatal(err)
	}
	if block.ID == "" {
		t.Fatal("expected ID to be generated")
	}

	// Retrieve.
	got, err := s.Get(block.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello world" {
		t.Errorf("expected 'hello world', got %q", got.Content)
	}
	if got.Tier != TierLong {
		t.Errorf("expected tier long, got %q", got.Tier)
	}
}

func TestStoreShortTerm(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	block := &Block{
		Tier:    TierShort,
		Label:   "note",
		Content: "session note",
		Source:  "user",
	}
	if err := s.Add(block); err != nil {
		t.Fatal(err)
	}

	// Should be in short-term (memory only).
	blocks, err := s.List(TierShort)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 short-term block, got %d", len(blocks))
	}
	if blocks[0].Content != "session note" {
		t.Errorf("expected 'session note', got %q", blocks[0].Content)
	}
}

func TestStoreDelete(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	block := &Block{Tier: TierLong, Label: "test", Content: "to delete"}
	if err := s.Add(block); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete(block.ID); err != nil {
		t.Fatal(err)
	}

	_, err = s.Get(block.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestStoreSearch(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Add(&Block{Tier: TierLong, Label: "pref", Content: "I prefer dark mode"})
	s.Add(&Block{Tier: TierLong, Label: "fact", Content: "The project uses Go"})
	s.Add(&Block{Tier: TierShort, Label: "note", Content: "Remember to test dark theme"})

	results, err := s.Search("dark")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'dark', got %d", len(results))
	}
}

func TestStoreClearTier(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Add(&Block{Tier: TierLong, Label: "a", Content: "long1"})
	s.Add(&Block{Tier: TierLong, Label: "b", Content: "long2"})
	s.Add(&Block{Tier: TierMedium, Label: "c", Content: "medium1"})

	count, err := s.ClearTier(TierLong)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 deleted, got %d", count)
	}

	// Medium should still be there.
	blocks, err := s.List(TierMedium)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Errorf("expected 1 medium block, got %d", len(blocks))
	}
}

func TestStoreExpiration(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	past := time.Now().Add(-1 * time.Hour)
	s.Add(&Block{Tier: TierShort, Label: "expired", Content: "old", ExpiresAt: &past})
	s.Add(&Block{Tier: TierShort, Label: "active", Content: "new"})

	blocks, err := s.List(TierShort)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 active block, got %d", len(blocks))
	}
	if blocks[0].Content != "new" {
		t.Errorf("expected 'new', got %q", blocks[0].Content)
	}
}

func TestStoreSummary(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sum := &Summary{
		ConversationID: "abc123",
		Title:          "Test Conversation",
		Content:        "Discussed testing strategies",
		MessageCount:   5,
	}
	if err := s.SaveSummary(sum); err != nil {
		t.Fatal(err)
	}

	summaries, err := s.ListSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].Title != "Test Conversation" {
		t.Errorf("expected 'Test Conversation', got %q", summaries[0].Title)
	}
}

func TestStoreStats(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Add(&Block{Tier: TierShort, Label: "a", Content: "short"})
	s.Add(&Block{Tier: TierMedium, Label: "b", Content: "medium"})
	s.Add(&Block{Tier: TierLong, Label: "c", Content: "long1"})
	s.Add(&Block{Tier: TierLong, Label: "d", Content: "long2"})

	stats, err := s.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ShortTermCount != 1 {
		t.Errorf("short: expected 1, got %d", stats.ShortTermCount)
	}
	if stats.MediumTermCount != 1 {
		t.Errorf("medium: expected 1, got %d", stats.MediumTermCount)
	}
	if stats.LongTermCount != 2 {
		t.Errorf("long: expected 2, got %d", stats.LongTermCount)
	}
	if stats.TotalCount != 4 {
		t.Errorf("total: expected 4, got %d", stats.TotalCount)
	}
}

func TestStoreBuildContext(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	s.Add(&Block{Tier: TierLong, Label: "pref", Content: "User prefers Go"})
	s.Add(&Block{Tier: TierShort, Label: "note", Content: "Working on tests"})

	cw, err := s.BuildContext()
	if err != nil {
		t.Fatal(err)
	}
	if len(cw.LongTerm) != 1 {
		t.Errorf("expected 1 long-term, got %d", len(cw.LongTerm))
	}
	if len(cw.ShortTerm) != 1 {
		t.Errorf("expected 1 short-term, got %d", len(cw.ShortTerm))
	}

	rendered := cw.Render()
	if rendered == "" {
		t.Error("expected non-empty rendered context")
	}
	if !contains(rendered, "memory_context") {
		t.Error("expected <memory_context> tag in rendered output")
	}
}

func TestManagerRememberForget(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	defer mgr.Close()

	// Remember.
	block, err := mgr.Remember("I like dark mode", "preference")
	if err != nil {
		t.Fatal(err)
	}
	if block.Tier != TierLong {
		t.Errorf("expected long tier, got %q", block.Tier)
	}

	// Search.
	results, err := mgr.Search("dark mode")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Forget by query.
	count, err := mgr.Forget("dark mode")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 deleted, got %d", count)
	}

	// Verify deleted.
	results, err = mgr.Search("dark mode")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after forget, got %d", len(results))
	}
}

func TestManagerEndSession(t *testing.T) {
	s, err := OpenStore(tempStorePath(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(s)
	mgr.AutoPromote = true
	defer mgr.Close()

	// Add short-term note.
	mgr.AddNote(TierShort, "session", "working on memory system")

	// Verify short-term.
	blocks, err := mgr.List(TierShort)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 short-term, got %d", len(blocks))
	}

	// End session.
	mgr.EndSession()

	// Short-term should be empty.
	blocks, err = mgr.List(TierShort)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Errorf("expected 0 short-term after session end, got %d", len(blocks))
	}

	// Should be promoted to medium-term.
	blocks, err = mgr.List(TierMedium)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Errorf("expected 1 medium-term after promotion, got %d", len(blocks))
	}
}

func TestDefaultStorePath(t *testing.T) {
	path := DefaultStorePath()
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".gleann", "memory", "memory.db")
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestParseTier(t *testing.T) {
	tests := []struct {
		input string
		want  Tier
		err   bool
	}{
		{"short", TierShort, false},
		{"short-term", TierShort, false},
		{"medium", TierMedium, false},
		{"medium-term", TierMedium, false},
		{"long", TierLong, false},
		{"long-term", TierLong, false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		got, err := ParseTier(tt.input)
		if tt.err && err == nil {
			t.Errorf("ParseTier(%q): expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("ParseTier(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("ParseTier(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) &&
		(s == substr || findSubstring(s, substr))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
