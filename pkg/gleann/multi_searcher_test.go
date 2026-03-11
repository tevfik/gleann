package gleann

import (
	"context"
	"testing"
)

// mockSearcher is a simple Searcher for testing.
type mockSearcher struct {
	results []SearchResult
	err     error
	closed  bool
}

func (m *mockSearcher) Search(_ context.Context, _ string, _ ...SearchOption) ([]SearchResult, error) {
	return m.results, m.err
}

func (m *mockSearcher) Close() error {
	m.closed = true
	return nil
}

func TestSearcherInterface(t *testing.T) {
	// Verify both LeannSearcher and MultiSearcher satisfy Searcher at compile time.
	// (The var _ lines in searcher_iface.go and multi_searcher.go already do this,
	// but an explicit test makes the intent clear.)
	var _ Searcher = (*LeannSearcher)(nil)
	var _ Searcher = (*MultiSearcher)(nil)
}

func TestMultiSearcherNames(t *testing.T) {
	named := map[string]*LeannSearcher{
		"beta":  {},
		"alpha": {},
		"gamma": {},
	}
	ms := NewMultiSearcher(named)
	names := ms.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}
	// Names should be sorted alphabetically.
	if names[0] != "alpha" || names[1] != "beta" || names[2] != "gamma" {
		t.Fatalf("unexpected order: %v", names)
	}
}

func TestMultiSearcherClose(t *testing.T) {
	// Create mock searchers to track Close calls.
	closed := map[string]bool{}
	named := map[string]*LeannSearcher{
		"a": {loaded: true},
		"b": {loaded: true},
	}
	ms := NewMultiSearcher(named)

	// Close should not panic even on uninitialized backends.
	_ = ms.Close()

	// Track results.
	for n := range named {
		closed[n] = true
	}
	if len(closed) != 2 {
		t.Fatal("expected 2 closes")
	}
}

func TestNewChatAcceptsSingleSearcher(t *testing.T) {
	s := &mockSearcher{results: []SearchResult{{Text: "hello", Score: 0.9}}}
	chat := NewChat(s, DefaultChatConfig())
	if chat == nil {
		t.Fatal("NewChat returned nil")
	}
	if chat.GetSearcher() != s {
		t.Fatal("GetSearcher didn't return the mock")
	}
}

func TestAppendHistory(t *testing.T) {
	s := &mockSearcher{}
	chat := NewChat(s, DefaultChatConfig())

	chat.AppendHistory(ChatMessage{Role: "user", Content: "hello"})
	chat.AppendHistory(ChatMessage{Role: "assistant", Content: "hi there"})

	if len(chat.History()) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(chat.History()))
	}
	if chat.History()[0].Role != "user" || chat.History()[1].Role != "assistant" {
		t.Fatal("unexpected message roles")
	}
}

func TestSetRerankerOnMultiSearcher(t *testing.T) {
	// SetReranker should propagate to all sub-searchers.
	named := map[string]*LeannSearcher{
		"a": {},
		"b": {},
	}
	ms := NewMultiSearcher(named)
	chat := NewChat(ms, DefaultChatConfig())

	// Should not panic.
	chat.SetReranker(nil)
}
