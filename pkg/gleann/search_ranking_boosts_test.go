package gleann

import (
	"context"
	"testing"
)

// rankingFixture indexes the same text under sources that the ranking treats
// differently, so the order of the results is decided only by the boosts and
// demotions (equal base scores).
func rankingFixture(t *testing.T) *LeannSearcher {
	t.Helper()
	dir := t.TempDir()
	b, emb := newFormat2Builder(t, dir, 8)
	const text = "parse the configuration file and validate settings"
	items := []Item{
		{Text: text, Metadata: map[string]any{"source": "internal/config.go", "name": "ParseConfig", "fqn": "config.ParseConfig"}},
		{Text: text, Metadata: map[string]any{"source": "internal/other.go", "name": "Other", "fqn": "config.Other"}},
		{Text: text, Metadata: map[string]any{"source": "vendor/x/config.go"}},
		{Text: text, Metadata: map[string]any{"source": "lib/flags.go", "is_vendor": true}},
		{Text: text, Metadata: map[string]any{"source": "internal/config_test.go"}},
		{Text: text, Metadata: map[string]any{"source": "lib/check.go", "is_test": true}},
		{Text: text, Metadata: map[string]any{"source": "docs/config.md"}},
		{Text: text, Metadata: map[string]any{"source": "notes/a", "ext": ".txt"}},
		{Text: text, Metadata: map[string]any{"source": "notes/b", "file": "guide.rst"}},
		{Text: text, Metadata: map[string]any{"source": "notes/c", "kind": "documentation"}},
	}
	if err := b.Build(context.Background(), "rank", items); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s.Load(context.Background(), "rank"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sourcesOf(rs []SearchResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i], _ = r.Metadata["source"].(string)
	}
	return out
}

func rankOf(rs []SearchResult, src string) int {
	for i, r := range rs {
		if r.Metadata["source"] == src {
			return i
		}
	}
	return -1
}

func TestSearchRankingBoostsAndDemotions(t *testing.T) {
	s := rankingFixture(t)
	ctx := context.Background()
	q := "parse the configuration file and validate settings"

	rs, err := s.Search(ctx, q, WithTopK(20), WithHybridAlpha(0.7))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 10 {
		t.Fatalf("%d results: %v", len(rs), sourcesOf(rs))
	}
	plain := rankOf(rs, "internal/config.go")
	for _, demoted := range []string{"vendor/x/config.go", "lib/flags.go", "internal/config_test.go", "lib/check.go"} {
		if r := rankOf(rs, demoted); r < plain {
			t.Errorf("%s (rank %d) ranks above first-party code (rank %d): %v", demoted, r, plain, sourcesOf(rs))
		}
	}
	// Vendor code (x0.3) ranks below tests (x0.5).
	if rankOf(rs, "vendor/x/config.go") < rankOf(rs, "internal/config_test.go") {
		t.Errorf("vendor above tests: %v", sourcesOf(rs))
	}
	// A query about tests does not demote them.
	rs, _ = s.Search(ctx, q+" test", WithTopK(20), WithHybridAlpha(0.7))
	if rankOf(rs, "internal/config_test.go") > rankOf(rs, "vendor/x/config.go") {
		t.Errorf("tests demoted although the query asks for them: %v", sourcesOf(rs))
	}

	// The exact symbol name and the FQN suffix boost their passage to the top.
	for _, q := range []string{"ParseConfig", "config.ParseConfig", "parseconfig"} {
		rs, err := s.Search(ctx, q, WithTopK(3), WithHybridAlpha(0.7))
		if err != nil || len(rs) == 0 || rs[0].Metadata["source"] != "internal/config.go" {
			t.Errorf("query %q: top = %v, %v", q, sourcesOf(rs), err)
		}
	}
	rs, _ = s.Search(ctx, "where does the Other helper live", WithTopK(3), WithHybridAlpha(0.7))
	if len(rs) == 0 || rs[0].Metadata["source"] != "internal/other.go" {
		t.Errorf("a query naming a symbol must boost it: %v", sourcesOf(rs))
	}

	// Kind filters split code from documentation (source suffix, ext, file
	// and kind metadata all mark docs).
	docs, err := s.Search(ctx, q, WithTopK(20), WithHybridAlpha(0.7), WithKind("docs"))
	if err != nil || len(docs) != 4 {
		t.Fatalf("docs = %v, %v", sourcesOf(docs), err)
	}
	code, _ := s.Search(ctx, q, WithTopK(20), WithHybridAlpha(0.7), WithKind("code"))
	if len(code) != 6 {
		t.Fatalf("code = %v", sourcesOf(code))
	}
	all, _ := s.Search(ctx, q, WithTopK(20), WithHybridAlpha(0.7), WithKind("all"))
	if len(all) != 10 {
		t.Fatalf("all = %v", sourcesOf(all))
	}
}

// A symbol with callers in the code graph ranks above an otherwise equal one
// without (graph-assisted ranking), capped so it cannot beat an exact match.
func TestSearchGraphCallerBoost(t *testing.T) {
	s := rankingFixture(t)
	g := newMockGraphDB()
	g.callersMap = map[string][]Callee{"config.Other": make([]Callee, 50)}
	s.graphDB = g
	defer func() { s.graphDB = nil }()
	rs, err := s.Search(context.Background(), "parse the configuration file and validate settings", WithTopK(3), WithHybridAlpha(0.7))
	if err != nil || len(rs) == 0 || rs[0].Metadata["source"] != "internal/other.go" {
		t.Fatalf("top = %v, %v; want the symbol with callers", sourcesOf(rs), err)
	}
	if b := rs[0].Score - rs[1].Score; b > 0.15+1e-4 {
		t.Fatalf("caller boost %v exceeds its 0.15 cap", b)
	}
}
