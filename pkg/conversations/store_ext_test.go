package conversations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Pure function tests ---

func TestShortIDFull(t *testing.T) {
	id := "abcdef1234567890"
	got := ShortID(id)
	if got != "abcdef12" {
		t.Errorf("expected abcdef12, got %s", got)
	}
}

func TestShortIDShort(t *testing.T) {
	id := "abc"
	got := ShortID(id)
	if got != "abc" {
		t.Errorf("expected abc, got %s", got)
	}
}

func TestShortIDEmpty(t *testing.T) {
	if ShortID("") != "" {
		t.Error("expected empty")
	}
}

func TestIndexLabelMultiple(t *testing.T) {
	c := &Conversation{Indexes: []string{"docs", "code", "wiki"}}
	if c.IndexLabel() != "docs,code,wiki" {
		t.Errorf("unexpected: %s", c.IndexLabel())
	}
}

func TestIndexLabelEmpty(t *testing.T) {
	c := &Conversation{}
	if c.IndexLabel() != "" {
		t.Errorf("expected empty, got %s", c.IndexLabel())
	}
}

func TestMessageCountMixed(t *testing.T) {
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
		t.Errorf("expected 2, got %d", c.MessageCount())
	}
}

func TestMessageCountEmpty(t *testing.T) {
	c := &Conversation{}
	if c.MessageCount() != 0 {
		t.Errorf("expected 0, got %d", c.MessageCount())
	}
}

func TestAutoTitleFromFirstUser(t *testing.T) {
	c := &Conversation{
		Messages: []Message{
			{Role: "system", Content: "system prompt is long"},
			{Role: "user", Content: "What is gleann?"},
		},
	}
	title := autoTitle(c)
	if title != "What is gleann?" {
		t.Errorf("unexpected title: %s", title)
	}
}

func TestAutoTitleTruncated(t *testing.T) {
	longMsg := "This is a very long question that exceeds sixty characters and should be truncated with an ellipsis"
	c := &Conversation{
		Messages: []Message{{Role: "user", Content: longMsg}},
	}
	title := autoTitle(c)
	if len(title) > 60 {
		t.Errorf("title too long (%d chars): %s", len(title), title)
	}
	if title[len(title)-3:] != "..." {
		t.Errorf("expected ellipsis, got: %s", title)
	}
}

func TestAutoTitleNoUser(t *testing.T) {
	c := &Conversation{
		Messages: []Message{{Role: "system", Content: "sys"}},
	}
	if autoTitle(c) != "untitled" {
		t.Errorf("expected untitled, got %s", autoTitle(c))
	}
}

func TestGenerateID_Deterministic(t *testing.T) {
	// generateID includes time.Now() so IDs differ — just test it returns non-empty.
	c := &Conversation{
		Indexes:  []string{"test"},
		Title:    "Test Conv",
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
	id := generateID(c)
	if len(id) != 40 { // SHA-1 hex = 40 chars
		t.Errorf("expected 40 char SHA-1, got %d: %s", len(id), id)
	}
}

// --- Store CRUD with temp dir ---

func TestStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv := &Conversation{
		Title:   "Test Conversation",
		Indexes: []string{"docs"},
		Model:   "llama3",
		Messages: []Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}

	if err := store.Save(conv); err != nil {
		t.Fatal(err)
	}

	if conv.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if conv.Title != "Test Conversation" {
		t.Errorf("title changed: %s", conv.Title)
	}

	loaded, err := store.Load(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Test Conversation" {
		t.Errorf("loaded title: %s", loaded.Title)
	}
	if len(loaded.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(loaded.Messages))
	}
}

func TestStoreLoadByTitle(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv := &Conversation{
		Title:    "Unique Title",
		Messages: []Message{{Role: "user", Content: "test"}},
	}
	store.Save(conv)

	loaded, err := store.Load("Unique Title")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != conv.ID {
		t.Errorf("ID mismatch: %s vs %s", loaded.ID, conv.ID)
	}
}

func TestStoreLoadByPrefix(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv := &Conversation{
		Title:    "Prefix Test",
		Messages: []Message{{Role: "user", Content: "test"}},
	}
	store.Save(conv)

	prefix := conv.ID[:6]
	loaded, err := store.Load(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != conv.ID {
		t.Errorf("ID mismatch")
	}
}

func TestStoreLoadNotFound(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStoreList(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Save 3 conversations.
	for i := 0; i < 3; i++ {
		conv := &Conversation{
			Messages: []Message{{Role: "user", Content: "q"}},
		}
		store.Save(conv)
		time.Sleep(10 * time.Millisecond) // ensure different UpdatedAt
	}

	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Errorf("expected 3, got %d", len(list))
	}
	// Should be sorted newest first.
	if list[0].UpdatedAt.Before(list[1].UpdatedAt) {
		t.Error("expected newest first")
	}
}

func TestStoreListEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty, got %d", len(list))
	}
}

func TestStoreListNonExistentDir(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "does", "not", "exist"))
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if list != nil {
		t.Error("expected nil for nonexistent dir")
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv := &Conversation{
		Messages: []Message{{Role: "user", Content: "delete me"}},
	}
	store.Save(conv)

	if err := store.Delete(conv.ID); err != nil {
		t.Fatal(err)
	}

	_, err := store.Load(conv.ID)
	if err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestStoreDeleteByTitle(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv := &Conversation{
		Title:    "Delete Me",
		Messages: []Message{{Role: "user", Content: "q"}},
	}
	store.Save(conv)

	if err := store.Delete("Delete Me"); err != nil {
		t.Fatal(err)
	}
}

func TestStoreDeleteOlderThan(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Save a conversation.
	conv := &Conversation{
		Messages: []Message{{Role: "user", Content: "old conv"}},
	}
	store.Save(conv)

	// Manually rewrite the file with old timestamp.
	conv.UpdatedAt = time.Now().Add(-48 * time.Hour)
	data, _ := json.MarshalIndent(conv, "", "  ")
	os.WriteFile(store.path(conv.ID), data, 0o644)

	count, err := store.DeleteOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 deleted, got %d", count)
	}
}

func TestStoreLatestConv(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv1 := &Conversation{Title: "First", Messages: []Message{{Role: "user", Content: "a"}}}
	store.Save(conv1)
	time.Sleep(10 * time.Millisecond)

	conv2 := &Conversation{Title: "Second", Messages: []Message{{Role: "user", Content: "b"}}}
	store.Save(conv2)

	latest, err := store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest.Title != "Second" {
		t.Errorf("expected Second, got %s", latest.Title)
	}
}

func TestStoreLatestEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	latest, err := store.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest != nil {
		t.Error("expected nil for empty store")
	}
}

func TestStoreSaveAutoTitle(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	conv := &Conversation{
		Messages: []Message{{Role: "user", Content: "How to build an index?"}},
	}
	store.Save(conv)

	if conv.Title != "How to build an index?" {
		t.Errorf("expected auto title, got: %s", conv.Title)
	}
}

func TestStoreSaveTimestamps(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	before := time.Now()
	conv := &Conversation{
		Messages: []Message{{Role: "user", Content: "test"}},
	}
	store.Save(conv)

	if conv.CreatedAt.Before(before) {
		t.Error("CreatedAt should be after test start")
	}
	if conv.UpdatedAt.Before(before) {
		t.Error("UpdatedAt should be after test start")
	}
}

func TestStoreListIgnoresNonJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not json"), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("invalid{json"), 0o644)

	store := NewStore(dir)
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 valid conversations, got %d", len(list))
	}
}

func TestStorePath(t *testing.T) {
	store := NewStore("/tmp/convs")
	p := store.path("abc123")
	if p != "/tmp/convs/abc123.json" {
		t.Errorf("unexpected path: %s", p)
	}
}

func TestDefaultStoreDir(t *testing.T) {
	store := DefaultStore()
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}
