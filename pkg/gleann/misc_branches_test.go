package gleann

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ── MultiSearcher ──────────────────────────────────────────────

func TestMultiSearcherTrimsToTopKAndClosesAll(t *testing.T) {
	ctx := context.Background()
	a, b := lexicalSearcher(t), lexicalSearcher(t)
	ms := NewMultiSearcher(map[string]*LeannSearcher{"a": a, "b": b})

	got, err := ms.Search(ctx, "recipe", WithHybridAlpha(0), WithTopK(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want the merged list cut to top-k 1", len(got))
	}
	if idx, _ := got[0].Metadata["_index"].(string); idx != "a" && idx != "b" {
		t.Errorf("result lacks its index tag: %+v", got[0].Metadata)
	}

	if err := ms.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestMultiSearcherCloseJoinsErrors(t *testing.T) {
	ms := NewMultiSearcher(map[string]*LeannSearcher{
		"one": {backend: failingCloseBackend{}},
		"two": {passages: failingPassages{}},
	})
	err := ms.Close()
	if err == nil || !strings.Contains(err.Error(), `close "one"`) || !strings.Contains(err.Error(), `close "two"`) {
		t.Fatalf("err = %v, want both searchers named", err)
	}
}

// ── SearchMultiple ─────────────────────────────────────────────

func TestSearchMultipleListingFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{IndexDir: file, Backend: "hnsw"}
	if _, err := SearchMultiple(context.Background(), cfg, nil, nil, "q"); err == nil || !strings.Contains(err.Error(), "list indexes") {
		t.Errorf("err = %v, want the listing failure", err)
	}
	// A tag that cannot be expanded is skipped rather than failing the search.
	got, err := SearchMultiple(context.Background(), cfg, nil, []string{"@docs"}, "q")
	if err != nil || got != nil {
		t.Errorf("unexpandable tag: %v, %v", got, err)
	}
}

// wrongDimsEmbedder reports the index's dimensions but embeds queries with
// another size, so a hybrid search fails after a successful load.
type wrongDimsEmbedder struct{ testEmbedder }

func (e *wrongDimsEmbedder) ComputeSingle(context.Context, string) ([]float32, error) {
	return make([]float32, e.dim+3), nil
}

func TestSearchMultipleReportsSearchFailure(t *testing.T) {
	initTestHNSW()
	dir := t.TempDir()
	buildVectorIndex(t, dir, "repo", &testEmbedder{dim: 8})
	emb := &wrongDimsEmbedder{testEmbedder{dim: 8}}
	_, err := SearchMultiple(context.Background(), Config{IndexDir: dir, Backend: "hnsw"}, emb, []string{"repo"}, "main", WithHybridAlpha(0.5))
	if err == nil || !strings.Contains(err.Error(), `search index "repo"`) {
		t.Fatalf("err = %v", err)
	}
}

// ── metadata field aliases ─────────────────────────────────────

func TestResolveMetadataFieldAliases(t *testing.T) {
	cases := []struct {
		name  string
		field string
		meta  map[string]any
		want  any
		found bool
	}{
		{"file falls back to source", "file", map[string]any{"source": "a.go"}, "a.go", true},
		{"source falls back to file", "source", map[string]any{"file": "b.go"}, "b.go", true},
		{"ext reads an ext value", "ext", map[string]any{"ext": ".go"}, ".go", true},
		{"ext reads the extension alias", "ext", map[string]any{"extension": ".py"}, ".py", true},
		{"extension reads the ext alias", "extension", map[string]any{"ext": ".rs"}, ".rs", true},
		{"extension derives from a path", "extension", map[string]any{"file": "dir/x.md"}, ".md", true},
		{"nothing to resolve", "ext", map[string]any{"other": 1}, nil, false},
		{"unknown field", "zzz", map[string]any{"a": 1}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := resolveMetadataField(c.field, c.meta)
			if ok != c.found || got != c.want {
				t.Errorf("resolveMetadataField(%q) = %v, %v; want %v, %v", c.field, got, ok, c.want, c.found)
			}
		})
	}
}

// ── context field ──────────────────────────────────────────────

func TestContextFieldEdges(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := recencyScore(now.Add(time.Hour), now); got <= 0 {
		t.Errorf("a timestamp in the future counts as just accessed, got %v", got)
	}

	cases := []struct {
		active, cand string
		want         float64
	}{
		{"main.go", "pkg/util.go", 0.2}, // active file has no directory
		{"a/b/c/x.go", "a/y.go", 0.5},   // candidate sits higher in the tree
		{"a/b/x.go", "a/b/y.go", 0.8},   // same directory
		{"", "a/b.go", 0},               // nothing to compare
		{"a/b/x.go", "c/d/y.go", 0.2},   // unrelated: same project only
	}
	for _, c := range cases {
		got := ComputeProximity(c.active, c.cand)
		switch {
		case c.want == 0 && got != 0:
			t.Errorf("ComputeProximity(%q,%q) = %v, want 0", c.active, c.cand, got)
		case c.want > 0 && (got <= 0 || got > 0.8):
			t.Errorf("ComputeProximity(%q,%q) = %v, want a positive score <= 0.8", c.active, c.cand, got)
		}
	}
	if got := ComputeProximity("a/b/x.go", "a/b/y.go"); got != 0.8 {
		t.Errorf("same directory = %v", got)
	}
}

func TestEnrichSearchResultsNilMetadata(t *testing.T) {
	cft := DefaultContextField()
	in := []SearchResult{{ID: 1, Text: "a", Score: 0.9}, {ID: 2, Text: "b", Score: 0.1, Metadata: map[string]any{"source": "s.go"}}}
	out := cft.EnrichSearchResults(in, map[string]ContextSignal{"s.go": {StructureScore: 1, ProximityScore: 1}})
	if len(out) != 2 {
		t.Fatalf("got %d results", len(out))
	}
	for _, r := range out {
		if r.Metadata == nil {
			t.Errorf("result %d has no metadata map after enrichment", r.ID)
		}
	}
}

// ── shell compression ──────────────────────────────────────────

func TestShellCompressorFallbacks(t *testing.T) {
	sc := NewShellCompressor()

	// A pattern registered for a tool without a sub-command applies to any
	// sub-command of that tool.
	sc.AddPattern(ShellPattern{Name: "mytool_any", Tool: "mytool", Custom: func(string) string { return "custom" }})
	if got := sc.Compress("mytool unknown-sub --flag", "lots\nof\noutput"); got.PatternUsed != "mytool_any" || got.Output != "custom" {
		t.Errorf("tool-only pattern: %+v", got)
	}

	// cargo test output with nothing recognisable falls back to generic compression.
	raw := "warning: something\n\n\n\nnote: other"
	got := sc.Compress("cargo test", raw)
	if got.PatternUsed != "cargo_test" || strings.Contains(got.Output, "\n\n\n") || !strings.Contains(got.Output, "note: other") {
		t.Errorf("cargo test fallback: %+v", got)
	}
	// With recognisable lines only those are kept.
	got = sc.Compress("cargo test", "running 2 tests\ntest a ... ok\ntest result: ok. 2 passed")
	if got.Output != "test result: ok. 2 passed" {
		t.Errorf("cargo test summary: %q", got.Output)
	}
}

func TestCollapseLinesFlushesTrailingRun(t *testing.T) {
	re := regexp.MustCompile(`^x`)
	got := collapseLines("keep\nx1\nx2\nx3", re, "%d x-lines")
	if got != "keep\n  [3 x-lines]" {
		t.Errorf("got %q", got)
	}
	got = collapseLines("x1\nkeep\nx2", re, "%d x-lines")
	if got != "  [1 x-lines]\nkeep\n  [1 x-lines]" {
		t.Errorf("got %q", got)
	}
}

// ── explain ────────────────────────────────────────────────────

type termScorer struct{ plainScorer }

func (termScorer) ExplainTerms(string, int64) []TermExplanation {
	return []TermExplanation{{Term: "apple"}}
}

func TestExplainEdges(t *testing.T) {
	if got := maskSegment("a\nb\nc", 3, 2); got != "a\nb\nc" {
		t.Errorf("inverted range must leave the text alone, got %q", got)
	}
	if got := maskSegment("a\nb\nc", 0, 99); got != "" {
		t.Errorf("clamped range = %q", got)
	}

	ctx := context.Background()
	if _, err := (&LeannSearcher{}).ExplainResult(ctx, "q", SearchResult{Text: "x"}, ExplainOptions{}); err == nil {
		t.Error("explain without an embedder must fail")
	}

	// An empty passage has nothing to occlude: only the BM25 terms come back.
	s := &LeannSearcher{embedder: &testEmbedder{dim: 8}, scorer: &termScorer{}}
	exp, err := s.ExplainResult(ctx, "apple", SearchResult{ID: 7}, ExplainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if exp.ResultID != 7 || len(exp.Terms) != 1 || len(exp.Segments) != 0 {
		t.Errorf("explanation = %+v", exp)
	}
}

// ── react tool ─────────────────────────────────────────────────

func TestSearchToolErrorsAndEmpty(t *testing.T) {
	ctx := context.Background()
	tool := NewSearchTool(NewSearcher(DefaultConfig(), nil)) // never loaded
	if _, err := tool.Execute(ctx, "anything"); err == nil {
		t.Error("an unloaded searcher must surface its error")
	}

	loaded := NewSearchTool(lexicalSearcher(t))
	out, err := loaded.Execute(ctx, "zzzzqqqq")
	if err != nil || out != "No results found." {
		t.Errorf("no match: %q, %v", out, err)
	}
	out, err = loaded.Execute(ctx, "apple")
	if err != nil || !strings.Contains(out, "[1] [recipes.md]") {
		t.Errorf("match: %q, %v", out, err)
	}
}

// ── citations, summariser, rerank stage, pacer ─────────────────

func TestAttributeRejectsShortEmbedderAnswer(t *testing.T) {
	emb := &covEmbedder{dim: 4}
	emb.shortBy.Store(1)
	_, err := Attribute(context.Background(), emb, "The cache is flushed nightly. It is rebuilt on demand.", []SearchResult{{Text: "cache notes"}}, 0)
	if err == nil || !strings.Contains(err.Error(), "embedder returned") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractSummaryWithoutContentWords(t *testing.T) {
	// A long enough sentence made only of stop words and short words has no
	// word to rank by.
	if got := ExtractSummary("The and with for the and with for the and with for the and."); got != "" {
		t.Errorf("summary = %q, want none", got)
	}
}

func TestRerankStageNoResults(t *testing.T) {
	rr := &countingReranker{}
	if got := rerankStage(context.Background(), rr, "q", nil, 3, SearchConfig{}); len(got) != 0 || rr.calls != 0 {
		t.Errorf("got %v with %d reranker calls", got, rr.calls)
	}
}

type countingReranker struct{ calls int }

func (c *countingReranker) Rerank(_ context.Context, _ string, r []SearchResult, _ int) ([]SearchResult, error) {
	c.calls++
	return r, errors.New("unused")
}

func TestPacerClampsNegativeProgress(t *testing.T) {
	var buf bytes.Buffer
	p := NewPacer(SurfacePlain, &buf)
	// A counter that ran backwards must not panic the bar renderer.
	p.Tick("scan", 0, 0, -50, 100, true)
	if buf.Len() == 0 {
		t.Error("forced tick wrote nothing")
	}
}
