package mcp

import (
	"context"
	"testing"

	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/gleann"
)

// The MCP server caches a searcher per index for the life of the process. A
// CLI sync in another process (here a second builder over the same
// directory) removes a file and renumbers the vector slots: the next tool
// call must get a reloaded searcher that returns the right text.
func TestGetSearcher_SeesOtherProcessSync(t *testing.T) {
	t.Setenv("GLEANN_STORE_FORMAT", "2")
	dir := t.TempDir()
	emb := &testBudgetEmbedder{dim: 8}
	glCfg := gleann.DefaultConfig()
	glCfg.IndexDir = dir
	glCfg.Backend = "hnsw"

	items := []gleann.Item{
		{Text: "apple pie recipe", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "banana bread", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}},
		{Text: "cherry tart dessert", Metadata: map[string]any{"source": "c.md", "chunk_index": 0}},
	}
	b1, err := gleann.NewBuilder(glCfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b1.Build(ctx, "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}

	srv := NewServer(Config{IndexDir: dir, EmbeddingProvider: "custom", EmbeddingModel: "test-model"})
	srv.embedder = emb
	defer srv.Close()

	first, err := srv.getSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := srv.getSearcher("idx"); err != nil || again != first {
		t.Fatalf("an unchanged index must reuse the cached searcher (err=%v)", err)
	}

	b2, err := gleann.NewBuilder(glCfg, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := b2.UpdateIndex(ctx, "idx", []gleann.Item{
		{Text: "date syrup cake and more", Metadata: map[string]any{"source": "d.md", "chunk_index": 0}},
	}, []string{"a.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}

	second, err := srv.getSearcher("idx")
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("the cached searcher was not replaced after another process published the index")
	}
	for _, want := range []string{"cherry tart dessert", "banana bread", "date syrup cake and more"} {
		rs, err := second.Search(ctx, want, gleann.WithTopK(1), gleann.WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("Search(%q): %v", want, err)
		}
		if len(rs) != 1 || rs[0].Text != want {
			t.Errorf("Search(%q) = %+v; stale or mismatched passage", want, rs)
		}
	}
}
