package gleann

import (
	"context"
	"strings"
	"testing"
)

func TestLoadMultiSearcher(t *testing.T) {
	cfg, emb := buildMultiFixture(t)
	ctx := context.Background()

	// A missing index fails the whole load and names the index.
	if _, err := LoadMultiSearcher(ctx, cfg, emb, []string{"fruit", "nope"}); err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("missing index: %v", err)
	}

	ms, err := LoadMultiSearcher(ctx, cfg, emb, []string{"tools", "fruit", "cake"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ms.Names(), ",") != "cake,fruit,tools" {
		t.Fatalf("Names = %v", ms.Names())
	}
	if ms.Searcher("fruit") == nil || ms.Searcher("nope") != nil {
		t.Fatal("Searcher lookup")
	}

	// TopK 0 falls back to 10; every result names its index.
	rs, err := ms.Search(ctx, "apple", WithTopK(0), WithHybridAlpha(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) == 0 || len(rs) > 10 {
		t.Fatalf("%d results", len(rs))
	}
	seen := map[any]bool{}
	for _, r := range rs {
		seen[r.Metadata["_index"]] = true
	}
	if !seen["fruit"] || !seen["cake"] {
		t.Fatalf("apple hits come from fruit and cake, got %v", seen)
	}

	// A cancelled context fails the fan-out.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ms.Search(cctx, "apple"); err == nil {
		t.Fatal("a cancelled search succeeded")
	}

	if err := ms.Close(); err != nil {
		t.Fatal(err)
	}
	// A second close reports the searchers that are already closed, or
	// nothing; it must not panic.
	_ = ms.Close()

}
