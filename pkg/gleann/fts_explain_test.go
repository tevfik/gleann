package gleann

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
)

// `search --explain` on a format-2 index with the FTS5 lexical leg breaks the
// score down by term (it used to print no term table: FTSScorer had no
// ExplainTerms).
func TestFTSScorerExplainTerms(t *testing.T) {
	dir := t.TempDir()
	b, embedder := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	// Filler passages keep "apple" below the document-frequency cut (0.2).
	items := f2Items()
	for i := 0; i < 8; i++ {
		items = append(items, Item{Text: fmt.Sprintf("unrelated filler passage %d%s", i, strings.Repeat(" z", i)),
			Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "chunk_index": 0}})
	}
	if err := b.Build(ctx, "idx", items); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	te, ok := s.scorer.(TermExplainer)
	if !ok {
		t.Fatalf("scorer %T does not explain terms", s.scorer)
	}
	res, err := s.Search(ctx, "apple cinnamon", WithTopK(4))
	if err != nil || len(res) == 0 {
		t.Fatalf("Search: %v (%d results)", err, len(res))
	}
	var hit SearchResult
	for _, r := range res {
		if r.Text == "apple pie recipe with cinnamon sugar" {
			hit = r
		}
	}
	if hit.Text == "" {
		t.Fatal("expected passage not found")
	}
	terms := te.ExplainTerms("apple cinnamon", hit.ID)
	if len(terms) != 2 {
		t.Fatalf("terms = %+v, want apple and cinnamon", terms)
	}
	var sum float64
	for _, tm := range terms {
		if tm.TF != 1 || tm.Score <= 0 {
			t.Errorf("term %+v: want tf 1 and a positive score", tm)
		}
		sum += tm.Score
	}
	if terms[0].Score < terms[1].Score {
		t.Errorf("terms not sorted best first: %+v", terms)
	}
	// The contributions add up to the scorer's own score for the passage.
	total := s.scorer.Score("apple cinnamon", []Passage{{ID: hit.ID}})[0]
	if math.Abs(sum-float64(total)) > 1e-3 {
		t.Errorf("sum of terms %.4f != passage score %.4f", sum, total)
	}

	exp, err := s.ExplainResult(ctx, "apple cinnamon", hit, ExplainOptions{MaxSegments: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Terms) != 2 {
		t.Errorf("ExplainResult terms = %+v, want 2", exp.Terms)
	}
}
