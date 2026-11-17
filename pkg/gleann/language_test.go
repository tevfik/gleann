package gleann

import (
	"context"
	"strings"
	"testing"
)

func TestGuessLanguage(t *testing.T) {
	cases := []struct {
		name string
		src  []string
		text []string
		want string
	}{
		{"turkish prose", []string{"a.md", "b.md"}, []string{"Odadaki ışıkları kapattı.", "Toplantı yarın."}, "tr"},
		{"english prose", []string{"a.md", "b.md"}, []string{"Turn the lights off.", "Meeting tomorrow."}, ""},
		{"turkish only in code", []string{"a.go", "b.md"}, []string{"// ışık", "plain text"}, ""},
		{"a fifth is enough", []string{"1.md", "2.md", "3.md", "4.md", "5.md"}, []string{"şimdi", "a", "b", "c", "d"}, "tr"},
		{"less than a fifth", []string{"1.md", "2.md", "3.md", "4.md", "5.md", "6.md"}, []string{"şimdi", "a", "b", "c", "d", "e"}, ""},
		{"converted pdf counts as prose", []string{"rapor.pdf"}, []string{"Güneş ışığı"}, "tr"},
		{"no prose", []string{"main.go"}, []string{"package main"}, ""},
	}
	for _, c := range cases {
		got := guessLanguage(len(c.src), func(i int) (string, string) { return c.src[i], c.text[i] })
		if got != c.want {
			t.Errorf("%s: guessLanguage = %q, want %q", c.name, got, c.want)
		}
	}
}

// WP2.4: an upper-case Turkish query matches lower-case Turkish prose (the
// dotless I), and an ASCII-only query matches it too, in a fresh build that
// was never told its language.
func TestTurkishCorpusMatchesUpperCaseAndASCIIQueries(t *testing.T) {
	dir := t.TempDir()
	b, _ := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	items := []Item{
		{Text: "Akşam olunca odadaki ışıkları kapattı ve uyudu.", Metadata: map[string]any{"source": "a.md", "chunk_index": 0}},
		{Text: "Toplantı yarın sabah dokuzda başlayacak.", Metadata: map[string]any{"source": "b.md", "chunk_index": 0}},
		{Text: "Bahçedeki ağaçlar çiçek açtı.", Metadata: map[string]any{"source": "c.md", "chunk_index": 0}},
	}
	if err := b.Build(ctx, "tr", items); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &testEmbedder{dim: 8})
	if err := s.Load(ctx, "tr"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{"IŞIKLARI", "ışıkları", "isiklari"} {
		rs, err := s.Search(ctx, q, WithTopK(1), WithHybridAlpha(0))
		if err != nil {
			t.Fatal(err)
		}
		if len(rs) == 0 || !strings.Contains(rs[0].Text, "ışıkları") {
			t.Errorf("query %q: top hit %v, want the passage with ışıkları", q, rs)
		}
	}
}
