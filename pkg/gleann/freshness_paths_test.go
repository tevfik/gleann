package gleann

import (
	"context"
	"strings"
	"testing"
)

func TestFreshnessOfUnloadedAndCustomSearchers(t *testing.T) {
	ctx := context.Background()
	var nilSearcher *LeannSearcher
	if _, ok, err := nilSearcher.currentFreshness(ctx); ok || err != nil {
		t.Fatal("a nil searcher has no freshness")
	}
	s := NewSearcher(Config{IndexDir: t.TempDir()}, nil)
	if c, err := s.Change(ctx); c != IndexUnchanged || err != nil {
		t.Fatalf("unloaded searcher Change = %v, %v", c, err)
	}
	if stale, err := s.Stale(ctx); stale || err != nil {
		t.Fatalf("unloaded searcher Stale = %v, %v", stale, err)
	}
	if changed, err := s.mainChanged(ctx); changed || err != nil {
		t.Fatalf("unloaded searcher mainChanged = %v, %v", changed, err)
	}
	if err := s.RefreshDelta(ctx); err != errNeedsFullReload {
		t.Fatalf("RefreshDelta without a store = %v", err)
	}
	// A searcher that never loaded an index has nothing to reload.
	if did, err := s.Refresh(ctx); did || err != nil {
		t.Fatalf("Refresh of an unloaded searcher = %v, %v", did, err)
	}
}

// A searcher whose index was rebuilt elsewhere reloads it, and the chat's
// retrieval does so before searching.
func TestRefreshAfterRebuild(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	b, emb := newFormat2Builder(t, dir, 8)
	if err := b.Build(ctx, "fr", f2Items()); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s.Load(ctx, "fr"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	items := append(f2Items(), Item{Text: "durian custard", Metadata: map[string]any{"source": "d.md"}})
	if err := b.Build(ctx, "fr", items); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Change(ctx); c != IndexFullReload || err != nil {
		t.Fatalf("Change after a rebuild = %v, %v", c, err)
	}

	srv, _ := scriptedOllama(t, "the custard is durian")
	chat := newTestChat(LLMOllama, srv.URL)
	chat.SetSearcher(s)
	_, results, err := chat.AskWithResults(ctx, "durian custard")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range results {
		found = found || strings.Contains(r.Text, "durian")
	}
	if !found {
		t.Fatalf("the chat searched the stale index: %+v", results)
	}
	if c, _ := s.Change(ctx); c != IndexUnchanged {
		t.Fatalf("Change after the chat's refresh = %v", c)
	}
}
