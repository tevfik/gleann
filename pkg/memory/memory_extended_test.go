package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseTierExtended(t *testing.T) {
	tests := []struct {
		input   string
		want    Tier
		wantErr bool
	}{
		{"short", TierShort, false},
		{"short-term", TierShort, false},
		{"medium", TierMedium, false},
		{"medium-term", TierMedium, false},
		{"long", TierLong, false},
		{"long-term", TierLong, false},
		{"invalid", "", true},
		{"", "", true},
		{"SHORT", "", true}, // case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseTier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseTier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBlockIsExpired(t *testing.T) {
	t.Run("nil expires", func(t *testing.T) {
		b := &Block{ExpiresAt: nil}
		if b.IsExpired() {
			t.Error("nil ExpiresAt should not be expired")
		}
	})

	t.Run("future expires", func(t *testing.T) {
		future := time.Now().Add(24 * time.Hour)
		b := &Block{ExpiresAt: &future}
		if b.IsExpired() {
			t.Error("future ExpiresAt should not be expired")
		}
	})

	t.Run("past expires", func(t *testing.T) {
		past := time.Now().Add(-24 * time.Hour)
		b := &Block{ExpiresAt: &past}
		if !b.IsExpired() {
			t.Error("past ExpiresAt should be expired")
		}
	})
}

func TestBlockExceedsLimit(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		charLimit int
		want      bool
	}{
		{"zero limit (unlimited)", "any content", 0, false},
		{"negative limit (unlimited)", "content", -1, false},
		{"under limit", "short", 100, false},
		{"exact limit", "12345", 5, false},
		{"over limit", "too long", 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Block{Content: tt.content, CharLimit: tt.charLimit}
			if got := b.ExceedsLimit(); got != tt.want {
				t.Errorf("ExceedsLimit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBlockTruncateToLimit(t *testing.T) {
	t.Run("no truncation needed", func(t *testing.T) {
		b := &Block{Content: "short", CharLimit: 100}
		b.TruncateToLimit()
		if b.Content != "short" {
			t.Errorf("should not truncate: got %q", b.Content)
		}
	})

	t.Run("truncation applied", func(t *testing.T) {
		long := "This is a very long piece of content that exceeds the limit"
		b := &Block{Content: long, CharLimit: 30}
		b.TruncateToLimit()
		if len(b.Content) > 30 {
			t.Errorf("content length %d should be <= 30", len(b.Content))
		}
		if b.Content[len(b.Content)-len("... [truncated]"):] != "... [truncated]" {
			t.Errorf("should end with truncation marker, got %q", b.Content)
		}
	})

	t.Run("very small limit", func(t *testing.T) {
		b := &Block{Content: "long content here", CharLimit: 5}
		b.TruncateToLimit()
		// Should handle edge case gracefully
		if len(b.Content) == 0 {
			t.Error("should produce non-empty result")
		}
	})

	t.Run("unlimited", func(t *testing.T) {
		b := &Block{Content: "anything", CharLimit: 0}
		original := b.Content
		b.TruncateToLimit()
		if b.Content != original {
			t.Error("unlimited should not truncate")
		}
	})
}

func TestContextWindowRender(t *testing.T) {
	t.Run("empty window", func(t *testing.T) {
		cw := &ContextWindow{}
		if cw.Render() != "" {
			t.Error("empty window should render empty string")
		}
	})

	t.Run("long term only", func(t *testing.T) {
		cw := &ContextWindow{
			LongTerm: []Block{
				{Label: "fact", Content: "Go is great"},
			},
		}
		result := cw.Render()
		if result == "" {
			t.Fatal("should not be empty")
		}
		assertContains(t, result, "<memory_context>")
		assertContains(t, result, "</memory_context>")
		assertContains(t, result, "<long_term_memory>")
		assertContains(t, result, "[fact] Go is great")
	})

	t.Run("all tiers", func(t *testing.T) {
		cw := &ContextWindow{
			ShortTerm:  []Block{{Label: "task", Content: "fix bug"}},
			MediumTerm: []Block{{Label: "sprint", Content: "focus on perf"}},
			LongTerm:   []Block{{Label: "pref", Content: "use Go"}},
			Summaries:  []Summary{{Title: "Session 1", Content: "discussed arch", CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}},
		}
		result := cw.Render()
		assertContains(t, result, "<short_term_memory>")
		assertContains(t, result, "<medium_term_memory>")
		assertContains(t, result, "<long_term_memory>")
		assertContains(t, result, "<conversation_summaries>")
		assertContains(t, result, "[task] fix bug")
		assertContains(t, result, "[sprint] focus on perf")
		assertContains(t, result, "[pref] use Go")
		assertContains(t, result, "Session 1")
	})

	t.Run("medium only", func(t *testing.T) {
		cw := &ContextWindow{
			MediumTerm: []Block{{Label: "note", Content: "hello"}},
		}
		result := cw.Render()
		assertContains(t, result, "<medium_term_memory>")
		assertNotContains(t, result, "<long_term_memory>")
		assertNotContains(t, result, "<short_term_memory>")
	})
}

func TestContextWindowIsEmpty(t *testing.T) {
	empty := &ContextWindow{}
	if !empty.isEmpty() {
		t.Error("empty window should be empty")
	}

	nonEmpty := &ContextWindow{ShortTerm: []Block{{Content: "x"}}}
	if nonEmpty.isEmpty() {
		t.Error("window with short term should not be empty")
	}
}

func TestDefaultSleepTimeConfig(t *testing.T) {
	cfg := DefaultSleepTimeConfig()
	if cfg.Interval <= 0 {
		t.Error("interval should be positive")
	}
}

func TestStoreOpenClose(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	if store.Path() != path {
		t.Errorf("Path() = %q, want %q", store.Path(), path)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStoreAddGetDelete(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	block := &Block{
		Tier:    TierLong,
		Label:   "test",
		Content: "hello world",
		Tags:    []string{"test"},
	}

	if err := store.Add(block); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if block.ID == "" {
		t.Error("block ID should be set after Add")
	}

	got, err := store.Get(block.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "hello world" {
		t.Errorf("Content = %q, want %q", got.Content, "hello world")
	}
	if got.Label != "test" {
		t.Errorf("Label = %q, want %q", got.Label, "test")
	}

	if err := store.Delete(block.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Get(block.ID)
	if err == nil {
		t.Error("expected error getting deleted block")
	}
}

func TestStoreList(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	// Add blocks in different tiers
	store.Add(&Block{Tier: TierShort, Label: "s1", Content: "short 1"})
	store.Add(&Block{Tier: TierShort, Label: "s2", Content: "short 2"})
	store.Add(&Block{Tier: TierLong, Label: "l1", Content: "long 1"})

	shorts, err := store.List(TierShort)
	if err != nil {
		t.Fatalf("List short: %v", err)
	}
	if len(shorts) != 2 {
		t.Errorf("expected 2 short blocks, got %d", len(shorts))
	}

	longs, err := store.List(TierLong)
	if err != nil {
		t.Fatalf("List long: %v", err)
	}
	if len(longs) != 1 {
		t.Errorf("expected 1 long block, got %d", len(longs))
	}
}

func TestStoreSearchExtended(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	store.Add(&Block{Tier: TierLong, Label: "arch", Content: "hexagonal architecture with adapters", Tags: []string{"architecture"}})
	store.Add(&Block{Tier: TierLong, Label: "db", Content: "database uses PostgreSQL", Tags: []string{"database"}})

	results, err := store.Search("architecture")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 search result for 'architecture'")
	}
}

func TestStoreClearTierExtended(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	store.Add(&Block{Tier: TierShort, Content: "s1"})
	store.Add(&Block{Tier: TierShort, Content: "s2"})
	store.Add(&Block{Tier: TierLong, Content: "l1"})

	n, err := store.ClearTier(TierShort)
	if err != nil {
		t.Fatalf("ClearTier: %v", err)
	}
	if n != 2 {
		t.Errorf("cleared %d, want 2", n)
	}

	// Long should remain
	longs, _ := store.List(TierLong)
	if len(longs) != 1 {
		t.Errorf("expected 1 long block after clearing short, got %d", len(longs))
	}
}

func TestStoreClearAll(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	store.Add(&Block{Tier: TierShort, Content: "s1"})
	store.Add(&Block{Tier: TierLong, Content: "l1"})

	n, err := store.ClearAll()
	if err != nil {
		t.Fatalf("ClearAll: %v", err)
	}
	if n != 2 {
		t.Errorf("cleared %d, want 2", n)
	}
}

func TestStorePromote(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	block := &Block{Tier: TierShort, Label: "promote", Content: "to promote"}
	store.Add(block)

	if err := store.Promote(block.ID, TierLong); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	got, err := store.Get(block.ID)
	if err != nil {
		t.Fatalf("Get after promote: %v", err)
	}
	if got.Tier != TierLong {
		t.Errorf("tier after promote = %q, want %q", got.Tier, TierLong)
	}
}

func TestStorePruneExpired(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	past := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(24 * time.Hour)

	store.Add(&Block{Tier: TierShort, Content: "expired", ExpiresAt: &past})
	store.Add(&Block{Tier: TierShort, Content: "active", ExpiresAt: &future})
	store.Add(&Block{Tier: TierLong, Content: "permanent"})

	n, err := store.PruneExpired()
	if err != nil {
		t.Fatalf("PruneExpired: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
}

func TestStoreDeleteOlderThan(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	store.Add(&Block{Tier: TierShort, Content: "old"})
	time.Sleep(50 * time.Millisecond)
	store.Add(&Block{Tier: TierShort, Content: "new"})

	// Delete blocks older than 25ms
	n, err := store.DeleteOlderThan(TierShort, 25*time.Millisecond)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if n < 1 {
		t.Error("expected at least 1 block deleted")
	}
}

func TestStoreStatsExtended(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	store.Add(&Block{Tier: TierShort, Content: "s1"})
	store.Add(&Block{Tier: TierMedium, Content: "m1"})
	store.Add(&Block{Tier: TierLong, Content: "l1"})
	store.Add(&Block{Tier: TierLong, Content: "l2"})

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.ShortTermCount != 1 {
		t.Errorf("ShortTermCount = %d, want 1", stats.ShortTermCount)
	}
	if stats.MediumTermCount != 1 {
		t.Errorf("MediumTermCount = %d, want 1", stats.MediumTermCount)
	}
	if stats.LongTermCount != 2 {
		t.Errorf("LongTermCount = %d, want 2", stats.LongTermCount)
	}
	if stats.TotalCount != 4 {
		t.Errorf("TotalCount = %d, want 4", stats.TotalCount)
	}
}

func TestStoreBuildContextExtended(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	store.Add(&Block{Tier: TierShort, Label: "task", Content: "fix bug"})
	store.Add(&Block{Tier: TierLong, Label: "pref", Content: "use Go"})

	cw, err := store.BuildContext()
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if len(cw.ShortTerm) != 1 {
		t.Errorf("ShortTerm count = %d, want 1", len(cw.ShortTerm))
	}
	if len(cw.LongTerm) != 1 {
		t.Errorf("LongTerm count = %d, want 1", len(cw.LongTerm))
	}
}

func TestStoreSaveSummary(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	s := &Summary{
		ConversationID: "conv-1",
		Title:          "Test Session",
		Content:        "Discussed architecture",
		MessageCount:   10,
		CreatedAt:      time.Now(),
	}

	if err := store.SaveSummary(s); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}

	summaries, err := store.ListSummaries()
	if err != nil {
		t.Fatalf("ListSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].Title != "Test Session" {
		t.Errorf("Title = %q, want %q", summaries[0].Title, "Test Session")
	}
}

func TestManagerRememberAndSearch(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	block, err := mgr.Remember("hexagonal architecture uses ports and adapters", "arch", "design")
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if block.ID == "" {
		t.Error("expected block ID")
	}
	if block.Tier != TierLong {
		t.Errorf("default tier = %q, want long", block.Tier)
	}

	results, err := mgr.Search("architecture")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) < 1 {
		t.Error("expected at least 1 result")
	}
}

func TestManagerAddNote(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	block, err := mgr.AddNote(TierShort, "task", "implement feature X")
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	if block.Tier != TierShort {
		t.Errorf("tier = %q, want short", block.Tier)
	}
	if block.Label != "task" {
		t.Errorf("label = %q, want task", block.Label)
	}
}

func TestManagerForget(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	block, _ := mgr.Remember("temp fact to forget")

	n, err := mgr.Forget(block.ID)
	if err != nil {
		t.Fatalf("Forget by ID: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d, want 1", n)
	}
}

func TestManagerClear(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	mgr.AddNote(TierShort, "s", "1")
	mgr.AddNote(TierShort, "s", "2")
	mgr.AddNote(TierLong, "l", "1")

	n, err := mgr.Clear(TierShort)
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if n != 2 {
		t.Errorf("cleared %d, want 2", n)
	}
}

func TestManagerStats(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	mgr.AddNote(TierShort, "s", "1")
	mgr.AddNote(TierLong, "l", "1")

	stats, err := mgr.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.TotalCount != 2 {
		t.Errorf("TotalCount = %d, want 2", stats.TotalCount)
	}
}

func TestManagerBuildContext(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	mgr.AddNote(TierShort, "task", "code review")
	mgr.AddNote(TierLong, "fact", "Go is compiled")

	cw, err := mgr.BuildContext()
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	rendered := cw.Render()
	assertContains(t, rendered, "code review")
	assertContains(t, rendered, "Go is compiled")
}

func TestManagerScopedNotes(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	mgr.AddScopedNote("project-a", TierLong, "arch", "microservices")
	mgr.AddScopedNote("project-b", TierLong, "arch", "monolith")
	mgr.AddNote(TierLong, "global", "shared fact")

	// ListScoped returns scoped + global blocks
	results, err := mgr.ListScoped("project-a", TierLong)
	if err != nil {
		t.Fatalf("ListScoped: %v", err)
	}
	// Should get project-a (1) + global (1) = 2
	if len(results) != 2 {
		t.Errorf("expected 2 scoped+global results, got %d", len(results))
	}
}

func TestFilterScope(t *testing.T) {
	blocks := []Block{
		{Scope: "a", Content: "1"},
		{Scope: "b", Content: "2"},
		{Scope: "", Content: "3"},  // global, included in any scope
		{Scope: "a", Content: "4"},
	}

	// filterScope includes blocks with scope "a" + global blocks
	filtered := filterScope(blocks, "a")
	if len(filtered) != 3 { // a:1, global:3, a:4
		t.Errorf("expected 3 blocks with scope 'a' + global, got %d", len(filtered))
	}

	// Empty scope returns all
	filtered = filterScope(blocks, "")
	if len(filtered) != 4 {
		t.Errorf("expected all blocks for empty scope, got %d", len(filtered))
	}
}

func TestContainsTag(t *testing.T) {
	tests := []struct {
		tags  []string
		query string
		want  bool
	}{
		{[]string{"go", "rust", "python"}, "go", true},
		{[]string{"go", "rust", "python"}, "java", false},
		{nil, "go", false},
		{[]string{}, "go", false},
		{[]string{"architecture"}, "arch", true}, // partial match
	}

	for _, tt := range tests {
		got := containsTag(tt.tags, tt.query)
		if got != tt.want {
			t.Errorf("containsTag(%v, %q) = %v, want %v", tt.tags, tt.query, got, tt.want)
		}
	}
}

func TestDefaultStorePathNotEmpty(t *testing.T) {
	path := DefaultStorePath()
	if path == "" {
		t.Error("DefaultStorePath should not be empty")
	}
}

func TestNewSleepTimeEngine(t *testing.T) {
	store := openTestStore(t)
	mgr := NewManager(store)
	defer mgr.Close()

	cfg := DefaultSleepTimeConfig()
	engine := NewSleepTimeEngine(mgr, cfg)
	if engine == nil {
		t.Fatal("NewSleepTimeEngine returned nil")
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test_memory.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		os.Remove(path)
	})
	return store
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if len(s) == 0 || len(substr) == 0 {
		return
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Errorf("expected %q to contain %q", truncStr(s, 200), substr)
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			t.Errorf("expected %q to NOT contain %q", truncStr(s, 200), substr)
			return
		}
	}
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
