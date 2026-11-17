package gleann

import (
	"context"
	"sort"
	"testing"
)

// buildMultiFixture builds three real format-2 indexes in one directory:
//
//	fruit  tags [food]        apple/banana passages
//	cake   tags [food, sweet] cake passages, hidden from MCP
//	tools  tags [hardware]    hammer passage
func buildMultiFixture(t *testing.T) (Config, *testEmbedder) {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	b, emb := newFormat2Builder(t, dir, 8)
	fixtures := map[string][]Item{
		"fruit": {
			{Text: "apple orchard harvest", Metadata: map[string]any{"source": "apple.md"}},
			{Text: "banana smoothie", Metadata: map[string]any{"source": "banana.md"}},
		},
		"cake": {
			{Text: "apple cake with cream", Metadata: map[string]any{"source": "cake.md"}},
		},
		"tools": {
			{Text: "hammer and nails", Metadata: map[string]any{"source": "hammer.md"}},
		},
	}
	for name, items := range fixtures {
		if err := b.Build(ctx, name, items); err != nil {
			t.Fatalf("build %s: %v", name, err)
		}
	}
	hidden := false
	set := map[string]func(*IndexMeta){
		"fruit": func(m *IndexMeta) { m.Tags = []string{"food"} },
		"cake": func(m *IndexMeta) {
			m.Tags = []string{"food", "sweet"}
			m.MCPExposed = &hidden
		},
		"tools": func(m *IndexMeta) { m.Tags = []string{"hardware"} },
	}
	for name, fn := range set {
		if err := UpdateIndexMeta(dir, name, fn); err != nil {
			t.Fatalf("update meta %s: %v", name, err)
		}
	}
	cfg := DefaultConfig()
	cfg.IndexDir = dir
	cfg.Backend = "hnsw"
	return cfg, emb
}

func multiIndexes(rs []MultiSearchResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		if !seen[r.Index] {
			seen[r.Index] = true
			out = append(out, r.Index)
		}
	}
	sort.Strings(out)
	return out
}

func TestSearchMultiple_Selection(t *testing.T) {
	cfg, emb := buildMultiFixture(t)
	ctx := context.Background()

	cases := []struct {
		name    string
		tagsEnv string
		names   []string
		want    []string
	}{
		// nil names: every MCP-exposed index; the hidden cake index is skipped.
		{name: "all exposed", names: nil, want: []string{"fruit", "tools"}},
		// GLEANN_TAGS restricts the implicit set.
		{name: "env tag filter", tagsEnv: " food , ", names: nil, want: []string{"fruit"}},
		// @tag expands to the exposed indexes carrying the tag; duplicates
		// (explicit name plus tag expansion) are searched once.
		{name: "tag expansion", names: []string{"@food", "fruit", " tools "}, want: []string{"fruit", "tools"}},
		// An explicitly named hidden index is not searched.
		{name: "explicit hidden", names: []string{"cake", "tools"}, want: []string{"tools"}},
		// GLEANN_TAGS also filters explicit names and tag expansions.
		{name: "env filters explicit", tagsEnv: "hardware", names: []string{"fruit", "tools", "@food"}, want: []string{"tools"}},
		// A tag nobody carries yields nothing at all.
		{name: "unknown tag", names: []string{"@nope"}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GLEANN_TAGS", tc.tagsEnv)
			rs, err := SearchMultiple(ctx, cfg, emb, tc.names, "apple", WithTopK(20))
			if err != nil {
				t.Fatalf("SearchMultiple: %v", err)
			}
			got := multiIndexes(rs)
			if len(got) != len(tc.want) {
				t.Fatalf("searched indexes = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("searched indexes = %v, want %v", got, tc.want)
				}
			}
			for i := 1; i < len(rs); i++ {
				if rs[i-1].Score < rs[i].Score {
					t.Fatalf("results not sorted by score: %v then %v", rs[i-1].Score, rs[i].Score)
				}
			}
		})
	}
}

func TestSearchMultiple_TopKTruncates(t *testing.T) {
	cfg, emb := buildMultiFixture(t)
	t.Setenv("GLEANN_TAGS", "")
	rs, err := SearchMultiple(context.Background(), cfg, emb, []string{"fruit", "tools"}, "apple", WithTopK(1))
	if err != nil {
		t.Fatalf("SearchMultiple: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d results, want 1 (topK)", len(rs))
	}
	if rs[0].Index != "fruit" || rs[0].Text != "apple orchard harvest" {
		t.Errorf("best result = %s/%q, want the fruit apple passage", rs[0].Index, rs[0].Text)
	}

	// TopK <= 0 falls back to 10: all three passages of the two indexes come back.
	cfg.SearchConfig.TopK = 0
	rs, err = SearchMultiple(context.Background(), cfg, emb, []string{"fruit", "tools"}, "apple nails banana", WithTopK(0), WithMinScore(-1e9))
	if err != nil {
		t.Fatalf("SearchMultiple: %v", err)
	}
	if len(rs) == 0 || len(rs) > 10 {
		t.Errorf("got %d results with the default topK", len(rs))
	}
}

func TestSearchMultiple_LoadErrorNamesIndex(t *testing.T) {
	cfg, emb := buildMultiFixture(t)
	t.Setenv("GLEANN_TAGS", "")
	_, err := SearchMultiple(context.Background(), cfg, emb, []string{"fruit", "missing"}, "apple")
	if err == nil {
		t.Fatal("expected an error for a missing index")
	}
	if want := `load index "missing"`; !contains(err.Error(), want) {
		t.Errorf("error %q does not name the failing index (%s)", err, want)
	}
}
