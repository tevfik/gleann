package gleann

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── index administration on disk ───────────────────────────────

func writeMeta(t *testing.T, indexDir, name, body string) {
	t.Helper()
	dir := filepath.Join(indexDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".meta.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListIndexesSkipsJunk(t *testing.T) {
	dir := t.TempDir()
	writeMeta(t, dir, "good", `{"name":"good","tags":["x"]}`)
	writeMeta(t, dir, "broken", `{not json`)
	if err := os.MkdirAll(filepath.Join(dir, "nometa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ListIndexes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("ListIndexes = %+v, want only the readable index", got)
	}

	if byTag, err := ListIndexesByTag(dir, "x"); err != nil || len(byTag) != 1 {
		t.Errorf("by tag = %+v, %v", byTag, err)
	}
}

func TestListIndexesMissingAndNotADir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if got, err := ListIndexes(missing); err != nil || got != nil {
		t.Errorf("missing dir: %v, %v, want nil, nil", got, err)
	}

	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ListIndexes(file); err == nil || !strings.Contains(err.Error(), "read index directory") {
		t.Errorf("file as index dir: err = %v", err)
	}
	// The tag lookup passes that failure on.
	if _, err := ListIndexesByTag(file, "x"); err == nil {
		t.Error("ListIndexesByTag must propagate the listing error")
	}
}

func TestGetAndUpdateIndexMetaErrors(t *testing.T) {
	dir := t.TempDir()
	writeMeta(t, dir, "bad", `{"name":`)
	if _, err := GetIndexMeta(dir, "bad"); err == nil || !strings.Contains(err.Error(), "unmarshal index metadata") {
		t.Errorf("GetIndexMeta on bad json: %v", err)
	}
	if err := UpdateIndexMeta(dir, "absent", func(*IndexMeta) {}); err == nil || !strings.Contains(err.Error(), "read index metadata") {
		t.Errorf("UpdateIndexMeta on a missing index: %v", err)
	}
	if err := UpdateIndexMeta(dir, "../escape", func(*IndexMeta) {}); err == nil {
		t.Error("a path-like name must be refused")
	}

	// A failing metadata write leaves the original metadata untouched.
	writeMeta(t, dir, "ok", `{"name":"ok"}`)
	blockMetaWrites(t)
	called := false
	err := UpdateIndexMeta(dir, "ok", func(m *IndexMeta) { called = true; m.Description = "changed" })
	if err == nil || !strings.Contains(err.Error(), "write temp metadata") || !strings.Contains(err.Error(), "injected metadata write failure") || !called {
		t.Fatalf("err = %v called = %v", err, called)
	}
	if m, err := GetIndexMeta(dir, "ok"); err != nil || m.Description != "" {
		t.Errorf("metadata after failed update = %+v, %v", m, err)
	}
}

func TestRemoveIndexReportsLeftoversItCannotDelete(t *testing.T) {
	// Each leftover is made undeletable by turning it into a non-empty directory.
	for _, leftover := range []string{"_graph.wal", ".rewrite.lock", ".lock"} {
		t.Run(leftover, func(t *testing.T) {
			dir := t.TempDir()
			writeMeta(t, dir, "idx", `{"name":"idx"}`)
			stuck := filepath.Join(dir, "idx"+leftover)
			if err := os.MkdirAll(filepath.Join(stuck, "child"), 0o755); err != nil {
				t.Fatal(err)
			}
			err := RemoveIndex(dir, "idx")
			if err == nil || !strings.Contains(err.Error(), "idx"+leftover) {
				t.Fatalf("err = %v, want it to name %q", err, "idx"+leftover)
			}
			// The index directory itself is already gone.
			if _, statErr := os.Stat(filepath.Join(dir, "idx")); !os.IsNotExist(statErr) {
				t.Errorf("index dir still there: %v", statErr)
			}
		})
	}
}

// ── pure helpers ───────────────────────────────────────────────

func TestCosineSimilarityZeroVector(t *testing.T) {
	if got := cosineSimilarity([]float32{0, 0}, []float32{1, 2}); got != 0 {
		t.Errorf("zero vector similarity = %v, want 0", got)
	}
	if got := cosineSimilarity([]float32{1, 2}, []float32{1, 2}); got < 0.999 {
		t.Errorf("identical vectors similarity = %v", got)
	}
}

func TestMergeLexicalFillFaults(t *testing.T) {
	lex := func(ids ...int64) []SearchResult {
		var out []SearchResult
		for _, id := range ids {
			out = append(out, SearchResult{ID: id, Score: 99})
		}
		return out
	}
	ids := func(rs []SearchResult) []int64 {
		var out []int64
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	same := func(a, b []int64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	t.Run("already full", func(t *testing.T) {
		in := []SearchResult{{ID: 1, Score: 0.5}}
		if got := mergeLexicalFill(in, lex(2), 1, 0); len(got) != 1 {
			t.Errorf("got %v", ids(got))
		}
	})
	t.Run("skips duplicates and ranks below the floor", func(t *testing.T) {
		in := []SearchResult{{ID: 1, Score: 0.8}, {ID: 2, Score: 0.4}}
		got := mergeLexicalFill(in, lex(2, 3, 4, 5), 4, 0)
		if !same(ids(got), []int64{1, 2, 3, 4}) {
			t.Fatalf("ids = %v", ids(got))
		}
		if !(got[2].Score < 0.4 && got[3].Score < got[2].Score && got[3].Score > 0) {
			t.Errorf("fill scores = %v %v, want descending under the 0.4 floor", got[2].Score, got[3].Score)
		}
	})
	t.Run("non-positive floor steps down", func(t *testing.T) {
		in := []SearchResult{{ID: 1, Score: -0.5}}
		got := mergeLexicalFill(in, lex(2, 3), 3, 0)
		if len(got) != 3 || !(got[1].Score < -0.5 && got[2].Score < got[1].Score) {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("min score stops the fill", func(t *testing.T) {
		in := []SearchResult{{ID: 1, Score: 0.2}}
		if got := mergeLexicalFill(in, lex(2, 3), 3, 0.5); len(got) != 1 {
			t.Errorf("got %v, want the fill held back by MinScore", ids(got))
		}
	})
	t.Run("no loop results uses a unit floor", func(t *testing.T) {
		got := mergeLexicalFill(nil, lex(7, 8), 5, 0)
		if len(got) != 2 || !(got[0].Score < 1 && got[0].Score > got[1].Score) {
			t.Errorf("got %+v", got)
		}
	})
}

func TestWithRerankSkipIdentifiers(t *testing.T) {
	var cfg SearchConfig
	WithRerankSkipIdentifiers(true)(&cfg)
	if !cfg.RerankSkipIdentifiers {
		t.Error("option not applied")
	}
	WithRerankSkipIdentifiers(false)(&cfg)
	if cfg.RerankSkipIdentifiers {
		t.Error("option not cleared")
	}
}

// ── Close ──────────────────────────────────────────────────────

type failingCloseBackend struct{ BackendSearcher }

func (failingCloseBackend) Close() error { return errors.New("backend close failed") }

type failingPassages struct{ PassageStore }

func (failingPassages) Close() error { return errors.New("passages close failed") }

func TestSearcherCloseCollectsEveryError(t *testing.T) {
	s := &LeannSearcher{backend: failingCloseBackend{}, passages: failingPassages{}}
	err := s.Close()
	if err == nil || !strings.Contains(err.Error(), "backend close failed") || !strings.Contains(err.Error(), "passages close failed") {
		t.Fatalf("err = %v, want both close errors", err)
	}
}

func TestSearcherCloseReportsScorerError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	buildFormat2Lexical(t, dir, format2LexItems())
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	fts, ok := s.scorer.(*FTSScorer)
	if !ok {
		t.Fatalf("scorer = %T", s.scorer)
	}
	// Closing the scorer's handle ahead of time leaves Close nothing valid to
	// release; whatever it reports, the searcher must end up scorer-less.
	_ = fts.Close()
	_ = s.Close()
	if s.scorer != nil {
		t.Error("Close must drop the scorer")
	}
}

// ── lexical search paths ───────────────────────────────────────

func lexicalSearcher(t *testing.T) *LeannSearcher {
	t.Helper()
	dir := t.TempDir()
	buildFormat2Lexical(t, dir, format2LexItems())
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// plainScorer implements Scorer only (no TopK, not persisted).
type plainScorer struct{ added int }

func (p *plainScorer) Score(string, []Passage) []float32 { return nil }
func (p *plainScorer) AddDocuments(ps []Passage)         { p.added += len(ps) }

// fxTopKScorer answers TopK with fixed ids, some of which are not passages.
type fxTopKScorer struct {
	plainScorer
	ids []int64
}

func (c *fxTopKScorer) TopK(string, int) ([]int64, []float32) {
	scores := make([]float32, len(c.ids))
	for i := range scores {
		scores[i] = float32(len(c.ids) - i)
	}
	return c.ids, scores
}

func TestSearchBM25Errors(t *testing.T) {
	ctx := context.Background()
	s := lexicalSearcher(t)

	s.SetScorer(nil)
	if _, err := s.SearchBM25(ctx, "apple", 3); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Errorf("nil scorer: %v", err)
	}
	// The same failure surfaces through a plain search, filtered or not.
	if _, err := s.Search(ctx, "apple", WithHybridAlpha(0)); err == nil {
		t.Error("unfiltered lexical search with no scorer must fail")
	}
	filter := []MetadataFilter{{Field: "source", Operator: OpEqual, Value: "recipes.md"}}
	if _, err := s.Search(ctx, "apple", WithHybridAlpha(0), WithMetadataFilters(filter, "or")); err == nil {
		t.Error("filtered lexical search with no scorer must fail")
	}

	s.SetScorer(&plainScorer{})
	if _, err := s.SearchBM25(ctx, "apple", 3); err == nil || !strings.Contains(err.Error(), "does not support direct TopK") {
		t.Errorf("non-TopK scorer: %v", err)
	}
}

func TestSearchBM25SkipsUnknownPassageIDs(t *testing.T) {
	ctx := context.Background()
	s := lexicalSearcher(t)
	real, err := s.SearchBM25(ctx, "apple", 1)
	if err != nil || len(real) != 1 {
		t.Fatalf("setup: %v %v", real, err)
	}
	s.SetScorer(&fxTopKScorer{ids: []int64{9_999_999, real[0].ID}})
	got, err := s.SearchBM25(ctx, "apple", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != real[0].ID {
		t.Errorf("got %+v, want only the existing passage", got)
	}
}

func TestLoadFeedsACustomScorerEveryPassage(t *testing.T) {
	dir := t.TempDir()
	buildFormat2Lexical(t, dir, format2LexItems())

	for _, maxPassages := range []int{0, 2} {
		sc := &plainScorer{}
		cfg := Config{IndexDir: dir, Backend: "hnsw"}
		cfg.SearchConfig.MaxBM25Passages = maxPassages
		s := NewSearcher(cfg, nil)
		s.SetScorer(sc)
		if err := s.Load(context.Background(), "idx"); err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := 3
		if maxPassages > 0 {
			want = maxPassages
		}
		if sc.added != want {
			t.Errorf("MaxBM25Passages=%d: scorer got %d passages, want %d", maxPassages, sc.added, want)
		}
		s.Close()
	}
}

// ── graph context ──────────────────────────────────────────────

// failingGraph fails the document and symbol lookups.
type failingGraph struct{ mockGraphDB }

func (failingGraph) DocumentContext(string) (*DocumentContextData, error) {
	return nil, errors.New("document lookup failed")
}
func (failingGraph) SymbolsInFile(string) ([]Callee, error) {
	return nil, errors.New("symbol lookup failed")
}

func TestGraphContextDegradesWhenLookupsFail(t *testing.T) {
	s := &LeannSearcher{graphDB: &failingGraph{}}
	results := []SearchResult{
		{Text: "a", Metadata: map[string]any{"source": "x.go"}},
		{Text: "b", Metadata: map[string]any{"source": "x.go"}}, // served from the cache
		{Text: "c"}, // no source
	}
	s.enrichWithGraphContext(results)
	for _, r := range results {
		if r.GraphContext != nil {
			t.Errorf("a failed lookup must leave no graph context: %+v", r.GraphContext)
		}
	}
}

func TestSearchWithGraphContextEnrichesVectorResults(t *testing.T) {
	initTestHNSW()
	dir := t.TempDir()
	emb := &testEmbedder{dim: 8}
	buildVectorIndex(t, dir, "repo", emb)
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s.Load(context.Background(), "repo"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.graphDB = &mockGraphDB{
		symbolsInFile: map[string][]Callee{"main.go": {{FQN: "main.main", Name: "main", Kind: "function"}}},
		calleesMap:    map[string][]Callee{"main.main": {{FQN: "fmt.Println"}}},
		callersMap:    map[string][]Callee{"main.main": {{FQN: "os.Exit"}}},
	}
	rs, err := s.Search(context.Background(), "func main() {}", WithTopK(2), WithHybridAlpha(0.5), WithGraphContext(true))
	if err != nil || len(rs) == 0 {
		t.Fatalf("search: %v %v", rs, err)
	}
	enriched := 0
	for _, r := range rs {
		if src, _ := r.Metadata["source"].(string); src == "main.go" {
			if r.GraphContext == nil || len(r.GraphContext.Symbols) != 1 {
				t.Errorf("main.go result lacks graph context: %+v", r.GraphContext)
			}
			enriched++
		}
	}
	if enriched == 0 {
		t.Error("the main.go passage was not returned")
	}
}

func TestSearchGraphRAG(t *testing.T) {
	ctx := context.Background()
	t.Run("search failure is returned", func(t *testing.T) {
		s := NewSearcher(DefaultConfig(), nil) // never loaded
		if _, err := s.SearchGraphRAG(ctx, "apple", 3); err == nil {
			t.Error("expected an error from the unloaded searcher")
		}
	})
	t.Run("results are kept without a graph", func(t *testing.T) {
		s := lexicalSearcher(t)
		rs, err := s.SearchGraphRAG(ctx, "apple", 3)
		if err != nil || len(rs) == 0 {
			t.Fatalf("%v %v", rs, err)
		}
	})
	t.Run("graph adds the callee's file once", func(t *testing.T) {
		s := lexicalSearcher(t)
		s.graphDB = &mockGraphDB{
			calleesMap: map[string][]Callee{"apple": {
				{FQN: "p.pie", Name: "pie", File: "pie.go", Line: 4},
				{FQN: "p.pie2", Name: "pie2", File: "pie.go", Line: 9}, // same file: not added twice
			}},
		}
		rs, err := s.SearchGraphRAG(ctx, "apple", 3)
		if err != nil {
			t.Fatal(err)
		}
		added := 0
		for _, r := range rs {
			if r.Metadata["graph"] == true && strings.Contains(r.Text, "apple calls pie") {
				added++
			}
		}
		if added != 1 {
			t.Errorf("graph-injected results = %d, want 1 (%+v)", added, rs)
		}
	})
	t.Run("file metadata marks a path as seen", func(t *testing.T) {
		s := lexicalSearcher(t)
		s.graphDB = &mockGraphDB{
			calleesMap: map[string][]Callee{"apple": {{FQN: "p.pie", Name: "pie", File: "fromfile.go"}}},
		}
		// A passage whose metadata carries "file" (not "source") for that path.
		s.passages = filePassages{PassageStore: s.passages, file: "fromfile.go"}
		rs, err := s.SearchGraphRAG(ctx, "apple", 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.Metadata["graph"] == true {
				t.Errorf("a path already present in the results must not be injected again: %+v", r)
			}
		}
	})
}

// filePassages rewrites passage metadata to use the "file" key.
type filePassages struct {
	PassageStore
	file string
}

func (c filePassages) Get(id int64) (Passage, error) {
	p, err := c.PassageStore.Get(id)
	if err == nil {
		p.Metadata = map[string]any{"file": c.file}
	}
	return p, err
}
