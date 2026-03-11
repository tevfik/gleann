package conversations

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestConversationIndexes(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv := &Conversation{
		Indexes: []string{"docs", "code", "api"},
		Model:   "test-model",
		Messages: []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}

	if err := store.Save(conv); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := store.Load(conv.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(loaded.Indexes) != 3 {
		t.Fatalf("expected 3 indexes, got %d", len(loaded.Indexes))
	}
	if loaded.Indexes[0] != "docs" || loaded.Indexes[1] != "code" || loaded.Indexes[2] != "api" {
		t.Fatalf("unexpected indexes: %v", loaded.Indexes)
	}
}

func TestConversationIndexLabel(t *testing.T) {
	tests := []struct {
		indexes []string
		want    string
	}{
		{nil, ""},
		{[]string{"docs"}, "docs"},
		{[]string{"docs", "code"}, "docs,code"},
		{[]string{"a", "b", "c"}, "a,b,c"},
	}
	for _, tt := range tests {
		c := Conversation{Indexes: tt.indexes}
		got := c.IndexLabel()
		if got != tt.want {
			t.Errorf("IndexLabel(%v) = %q, want %q", tt.indexes, got, tt.want)
		}
	}
}

func TestStoreListAndDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Save two conversations.
	c1 := &Conversation{Indexes: []string{"docs"}, Model: "m1", Messages: []Message{{Role: "user", Content: "first"}}}
	c2 := &Conversation{Indexes: []string{"code"}, Model: "m2", Messages: []Message{{Role: "user", Content: "second"}}}

	if err := store.Save(c1); err != nil {
		t.Fatal(err)
	}
	// Small delay so updated_at differs.
	time.Sleep(10 * time.Millisecond)
	if err := store.Save(c2); err != nil {
		t.Fatal(err)
	}

	convs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 2 {
		t.Fatalf("expected 2, got %d", len(convs))
	}

	// Most recent first.
	if convs[0].ID != c2.ID {
		t.Error("expected c2 first")
	}

	// Delete by ID.
	if err := store.Delete(c1.ID); err != nil {
		t.Fatal(err)
	}
	remaining, _ := store.List()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 after delete, got %d", len(remaining))
	}
}

func TestStoreLatest(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Empty store.
	latest, err := store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Fatal("expected nil for empty store")
	}

	c := &Conversation{Indexes: []string{"docs"}, Messages: []Message{{Role: "user", Content: "hi"}}}
	store.Save(c)

	latest, err = store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != c.ID {
		t.Fatal("latest should return the saved conversation")
	}
}

func TestStorePrefixMatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	c := &Conversation{Indexes: []string{"docs"}, Messages: []Message{{Role: "user", Content: "test"}}}
	store.Save(c)

	// Prefix match.
	short := ShortID(c.ID)
	loaded, err := store.Load(short)
	if err != nil {
		t.Fatalf("prefix match failed: %v", err)
	}
	if loaded.ID != c.ID {
		t.Fatal("prefix match returned wrong conversation")
	}
}

func TestStoreTitleMatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	c := &Conversation{
		Title:   "My Test Chat",
		Indexes: []string{"docs"},
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
	}
	store.Save(c)

	// Case-insensitive title match.
	loaded, err := store.Load("my test chat")
	if err != nil {
		t.Fatalf("title match failed: %v", err)
	}
	if loaded.ID != c.ID {
		t.Fatal("title match returned wrong conversation")
	}
}

func TestDeleteOlderThan(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Save normally first to create the file and ID.
	c := &Conversation{Indexes: []string{"old"}, Messages: []Message{{Role: "user", Content: "ancient"}}}
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}

	// Now manually set the UpdatedAt to 48 hours ago and re-save
	// by writing the file directly (Save always sets UpdatedAt=now).
	c.UpdatedAt = time.Now().Add(-48 * time.Hour)
	data, _ := json.MarshalIndent(c, "", "  ")
	os.WriteFile(store.path(c.ID), data, 0o644)

	deleted, err := store.DeleteOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
}

func TestShortID(t *testing.T) {
	if got := ShortID("abcdef1234567890"); got != "abcdef12" {
		t.Errorf("ShortID = %q, want %q", got, "abcdef12")
	}
	if got := ShortID("abc"); got != "abc" {
		t.Errorf("ShortID = %q, want %q", got, "abc")
	}
}

func TestAutoTitle(t *testing.T) {
	c := &Conversation{Messages: []Message{{Role: "assistant", Content: "nope"}, {Role: "user", Content: "This is my question"}}}
	title := autoTitle(c)
	if title != "This is my question" {
		t.Errorf("autoTitle = %q", title)
	}
}

func TestAutoTitleTruncate(t *testing.T) {
	long := "This is a very long user message that should be truncated to sixty characters or thereabouts"
	c := &Conversation{Messages: []Message{{Role: "user", Content: long}}}
	title := autoTitle(c)
	if len(title) > 60 {
		t.Errorf("autoTitle too long: %d chars", len(title))
	}
}

func TestStoreEmptyDir(t *testing.T) {
	store := NewStore("/nonexistent/path/unlikely/to/exist")
	convs, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 0 {
		t.Fatal("expected empty list")
	}
}

func TestMessageCount(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "q1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "q2"},
			{Role: "assistant", Content: "a2"},
		},
	}
	if c.MessageCount() != 2 {
		t.Errorf("MessageCount = %d, want 2", c.MessageCount())
	}
}

func TestDefaultStore(t *testing.T) {
	s := DefaultStore()
	if s == nil {
		t.Fatal("DefaultStore returned nil")
	}
	home, _ := os.UserHomeDir()
	if s.dir == "" || home == "" {
		t.Skip("no home dir")
	}
}
