package gleann

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tevfik/gleann/modules/hnsw"
)

// This file covers the searcher paths the hnsw RAM test backend never takes:
// the memory-mapped vector leg with its ad-hoc recomputation (dead slots,
// embedder failures), the recomputation server, the SQL pre-selection fusion,
// the lexical fallbacks of search, Load's alternative scorer paths,
// SearchGraphRAG and RemoveIndex's leftovers.

// recomputeBackend is a memory-mapping backend that stores no vectors: every
// distance comes from the recompute callback, which the searcher answers with
// passage texts. It asks for slots 0..probe-1, so slots without a passage
// (dead or never used) exercise the "no text, nil vector" contract.
type recomputeBackend struct {
	probe     int
	loadedArg string

	mu      sync.Mutex
	nilSeen int
	asked   int
}

func (b *recomputeBackend) Load(ctx context.Context, data []byte, meta IndexMeta) error {
	return errors.New("recomputeBackend loads from a file only")
}

func (b *recomputeBackend) LoadFromFile(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	b.loadedArg = path
	return nil
}

func (b *recomputeBackend) Search(ctx context.Context, q []float32, topK int) ([]int64, []float32, error) {
	return nil, nil, errors.New("recomputeBackend has no stored vectors")
}

func (b *recomputeBackend) SearchWithRecompute(ctx context.Context, q []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	ids := make([]int64, b.probe)
	for i := range ids {
		ids[i] = int64(i)
	}
	vecs, err := recompute(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	if len(vecs) != len(ids) {
		return nil, nil, fmt.Errorf("recompute returned %d vectors for %d ids", len(vecs), len(ids))
	}
	type hit struct {
		id int64
		d  float32
	}
	var hits []hit
	b.mu.Lock()
	b.asked += len(ids)
	for i, v := range vecs {
		if v == nil {
			b.nilSeen++
			continue
		}
		var d float32
		for j := range v {
			x := v[j] - q[j]
			d += x * x
		}
		hits = append(hits, hit{ids[i], d})
	}
	b.mu.Unlock()
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].d != hits[j].d {
			return hits[i].d < hits[j].d
		}
		return hits[i].id < hits[j].id
	})
	if len(hits) > topK {
		hits = hits[:topK]
	}
	outIDs := make([]int64, len(hits))
	outD := make([]float32, len(hits))
	for i, h := range hits {
		outIDs[i], outD[i] = h.id, h.d
	}
	return outIDs, outD, nil
}

func (b *recomputeBackend) Close() error { return nil }

// recomputeFactory builds with the real hnsw builder (a real .index file) and
// searches with a fresh recomputeBackend, which it remembers for assertions.
type recomputeFactory struct {
	mu   sync.Mutex
	last *recomputeBackend
}

func (f *recomputeFactory) Name() string { return "cov-recompute" }
func (f *recomputeFactory) NewBuilder(cfg Config) BackendBuilder {
	return (&hnsw.Factory{}).NewBuilder(hnsw.Config{IndexDir: cfg.IndexDir, Backend: "hnsw"})
}
func (f *recomputeFactory) NewSearcher(cfg Config) BackendSearcher {
	b := &recomputeBackend{probe: 16}
	f.mu.Lock()
	f.last = b
	f.mu.Unlock()
	return b
}

func (f *recomputeFactory) lastBackend() *recomputeBackend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

var (
	covRecomputeOnce    sync.Once
	covRecomputeFactory = &recomputeFactory{}
)

// covEmbedder embeds like testEmbedder (the vector depends on the text length
// only) but can be told to fail or to return a wrong number of vectors for
// batch computations, while ComputeSingle (the query) keeps working.
type covEmbedder struct {
	dim       int
	failBatch atomic.Bool
	shortBy   atomic.Int32
	batchErr  error
	wrongDims int
	batches   atomic.Int32
}

func (e *covEmbedder) vec(text string) []float32 {
	v := make([]float32, e.dim)
	for j := range v {
		v[j] = float32(len(text)+j) * 0.01
	}
	return v
}

func (e *covEmbedder) Compute(ctx context.Context, texts []string) ([][]float32, error) {
	e.batches.Add(1)
	if e.failBatch.Load() {
		if e.batchErr != nil {
			return nil, e.batchErr
		}
		return nil, errors.New("embedder is down")
	}
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		out = append(out, e.vec(t))
	}
	if n := int(e.shortBy.Load()); n > 0 && n <= len(out) {
		out = out[:len(out)-n]
	}
	return out, nil
}

func (e *covEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	if e.wrongDims > 0 {
		return make([]float32, e.wrongDims), nil
	}
	return e.vec(text), nil
}

func (e *covEmbedder) Dimensions() int   { return e.dim }
func (e *covEmbedder) ModelName() string { return "test-model" }

// buildRecomputeIndex builds a format-2 index on the cov-recompute backend and
// returns a loaded searcher.
func buildRecomputeIndex(t *testing.T, items []Item) (*LeannSearcher, *LeannBuilder, *covEmbedder, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	covRecomputeOnce.Do(func() { RegisterBackend(covRecomputeFactory) })
	dir := t.TempDir()
	emb := &covEmbedder{dim: 8}
	b, err := NewBuilder(Config{IndexDir: dir, Backend: "cov-recompute"}, emb)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	if err := b.Build(context.Background(), "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "cov-recompute"}, emb)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, b, emb, dir
}

func TestVectorSearch_MmapRecomputesPassageTexts(t *testing.T) {
	s, _, _, dir := buildRecomputeIndex(t, f2Items())
	be := covRecomputeFactory.lastBackend()
	if want := filepath.Join(dir, "idx", "idx.index"); be.loadedArg != want {
		t.Fatalf("backend was loaded from %q, want the main file %q", be.loadedArg, want)
	}
	if got := s.IndexPath(); got != filepath.Join(dir, "idx") {
		t.Errorf("IndexPath = %q", got)
	}
	ctx := context.Background()
	for _, it := range f2Items() {
		rs, err := s.Search(ctx, it.Text, WithTopK(1), WithHybridAlpha(1.0))
		if err != nil {
			t.Fatalf("search %q: %v", it.Text, err)
		}
		if len(rs) != 1 || rs[0].Text != it.Text {
			t.Fatalf("search %q = %+v, want the passage itself", it.Text, rs)
		}
	}
	// Only 4 of the 16 probed slots hold a passage: the other 12 must come
	// back as nil vectors, not as the embedding of an empty text.
	be.mu.Lock()
	nils, asked := be.nilSeen, be.asked
	be.mu.Unlock()
	if asked == 0 || nils != asked-4*(asked/16) {
		t.Errorf("nil vectors = %d of %d asked, want every slot without a passage to be nil", nils, asked)
	}
}

func TestVectorSearch_MmapDeadSlotsAfterRemoval(t *testing.T) {
	s, b, emb, dir := buildRecomputeIndex(t, f2Items())
	ctx := context.Background()
	if err := b.UpdateIndex(ctx, "idx", nil, []string{"b.md"}); err != nil {
		t.Fatalf("UpdateIndex: %v", err)
	}
	s2 := NewSearcher(Config{IndexDir: dir, Backend: "cov-recompute"}, emb)
	if err := s2.Load(ctx, "idx"); err != nil {
		t.Fatalf("reload: %v", err)
	}
	defer s2.Close()
	rs, err := s2.Search(ctx, "banana bread", WithTopK(10), WithHybridAlpha(1.0))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range rs {
		if r.Text == "banana bread" {
			t.Fatalf("a removed passage came back through its dead slot: %+v", rs)
		}
	}
	if len(rs) != 3 {
		t.Errorf("got %d results, want the 3 live passages", len(rs))
	}
	_ = s
}

func TestVectorSearch_MmapRecomputeFailures(t *testing.T) {
	s, _, emb, _ := buildRecomputeIndex(t, f2Items())
	ctx := context.Background()

	emb.failBatch.Store(true)
	_, err := s.Search(ctx, "banana bread", WithTopK(1), WithHybridAlpha(1.0))
	if err == nil || !strings.Contains(err.Error(), "embedder is down") {
		t.Fatalf("a failing recomputation must fail the search, got %v", err)
	}
	emb.failBatch.Store(false)

	emb.shortBy.Store(1)
	_, err = s.Search(ctx, "banana bread", WithTopK(1), WithHybridAlpha(1.0))
	if err == nil || !strings.Contains(err.Error(), "vectors for") {
		t.Fatalf("a short embedding batch must fail the search, got %v", err)
	}
	emb.shortBy.Store(0)

	if _, err := s.Search(ctx, "banana bread", WithTopK(1), WithHybridAlpha(1.0)); err != nil {
		t.Fatalf("search after the embedder recovered: %v", err)
	}
}

// covEmbServer is a recomputation server that embeds by slot: slot i gets
// the vector of a text of length i.
type covEmbServer struct {
	running bool
	calls   atomic.Int32
}

func (c *covEmbServer) Start(ctx context.Context) error { c.running = true; return nil }
func (c *covEmbServer) Stop() error                     { c.running = false; return nil }
func (c *covEmbServer) IsRunning() bool                 { return c.running }
func (c *covEmbServer) ComputeEmbeddings(ctx context.Context, ids []int64) ([][]float32, error) {
	c.calls.Add(1)
	out := make([][]float32, len(ids))
	for i, id := range ids {
		v := make([]float32, 8)
		for j := range v {
			v[j] = float32(int(id)+j) * 0.01
		}
		out[i] = v
	}
	return out, nil
}

func TestVectorSearch_UsesRunningEmbeddingServer(t *testing.T) {
	s, _, emb, _ := buildRecomputeIndex(t, f2Items())
	ctx := context.Background()
	srv := &covEmbServer{}
	s.SetEmbeddingServer(srv)

	// A stopped server is not used: the passages are re-embedded ad hoc.
	before := emb.batches.Load()
	if _, err := s.Search(ctx, "apple pie recipe", WithTopK(1), WithHybridAlpha(1.0)); err != nil {
		t.Fatal(err)
	}
	if srv.calls.Load() != 0 || emb.batches.Load() == before {
		t.Fatalf("stopped server: server calls=%d, embedder batches %d→%d", srv.calls.Load(), before, emb.batches.Load())
	}

	_ = srv.Start(ctx)
	before = emb.batches.Load()
	if _, err := s.Search(ctx, "apple pie recipe", WithTopK(1), WithHybridAlpha(1.0)); err != nil {
		t.Fatal(err)
	}
	if srv.calls.Load() == 0 {
		t.Fatal("a running embedding server was not asked for the vectors")
	}
	if emb.batches.Load() != before {
		t.Errorf("the embedder re-embedded passages although the server answered")
	}
}

func TestSearchPreselected_FusesLexicalScores(t *testing.T) {
	items := make([]Item, 6)
	for i := range items {
		text := fmt.Sprintf("filler passage number %d", i)
		if i == 4 {
			text = "zebra zebra zebra stripes"
		}
		items[i] = Item{Text: text, Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": map[bool]string{true: "go", false: "rust"}[i%2 == 0]}}
	}
	s := newLinearSearcher(t, items, linearIDScorer{linearBackend{n: 6}})
	ctx := context.Background()
	engine := NewMetadataFilterEngine([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}})
	q := []float32{0}
	opts := s.config.SearchConfig
	opts.HybridAlpha = 0.5

	// Hybrid: the vector leg ranks slots 0 < 2 < 4 by distance, the lexical
	// leg matches only passage 4 ("zebra"); RRF lifts passage 4.
	rs, exact, ok := s.searchPreselected(ctx, "zebra", "zebra", q, opts, 3, engine, 0.05)
	if !ok || !exact {
		t.Fatalf("tier 1 should answer exactly, got ok=%v exact=%v", ok, exact)
	}
	if len(rs) != 3 {
		t.Fatalf("got %d results, want the 3 go passages", len(rs))
	}
	if !strings.Contains(rs[0].Text, "zebra") {
		t.Errorf("the lexical match should rank first after fusion, got %q", rs[0].Text)
	}
	if rs[0].BM25Score == nil {
		t.Error("the fused lexical match lost its raw BM25 score")
	}
	for _, r := range rs {
		if r.Metadata["lang"] != "go" {
			t.Errorf("result %q does not pass the filter", r.Text)
		}
		if r.Score > 1.0 {
			t.Errorf("fused score %v exceeds 1", r.Score)
		}
	}

	// No lexical match: the vector scores stand; a zero normalizer scores 1.
	opts.HybridAlpha = 1.0
	rs, exact, ok = s.searchPreselected(ctx, "q", "q", q, opts, 3, engine, 0)
	if !ok || !exact || len(rs) != 3 {
		t.Fatalf("vector-only tier 1: ok=%v exact=%v n=%d", ok, exact, len(rs))
	}
	for _, r := range rs {
		if r.Score != 1.0 {
			t.Errorf("score with a zero normalizer = %v, want 1", r.Score)
		}
	}

	// A filter that cannot be compiled to SQL does not pre-select at all.
	ext := NewMetadataFilterEngine([]MetadataFilter{{Field: "ext", Operator: OpEqual, Value: ".md"}})
	if _, _, ok := s.searchPreselected(ctx, "q", "q", q, opts, 3, ext, 1); ok {
		t.Error("an uncompilable filter must leave the loop's result standing (ok=false)")
	}

	// A filter nothing matches is an exact, empty answer.
	none := NewMetadataFilterEngine([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "cobol"}})
	rs, exact, ok = s.searchPreselected(ctx, "q", "q", q, opts, 3, none, 1)
	if !ok || !exact || len(rs) != 0 {
		t.Errorf("no match: ok=%v exact=%v n=%d, want an exact empty result", ok, exact, len(rs))
	}
}

// covReranker reverses the order it is given and records the pool size.
type covReranker struct {
	pool int
}

func (r *covReranker) Rerank(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error) {
	r.pool = len(results)
	out := make([]SearchResult, len(results))
	for i := range results {
		out[len(results)-1-i] = results[i]
		out[len(results)-1-i].Score = float32(i + 1)
	}
	if topN > 0 && len(out) > topN {
		out = out[:topN]
	}
	return out, nil
}

func lexicalItems() []Item {
	return []Item{
		{Text: "go channels and goroutines", Metadata: map[string]any{"source": "a.go", "lang": "go"}},
		{Text: "go channels again and again", Metadata: map[string]any{"source": "a.go", "lang": "go"}},
		{Text: "rust channels with tokio", Metadata: map[string]any{"source": "b.rs", "lang": "rust"}},
		{Text: "python channels via asyncio", Metadata: map[string]any{"source": "c.py", "lang": "python"}},
	}
}

func TestSearch_LexicalOnlyIndexOptions(t *testing.T) {
	dir := t.TempDir()
	b, emb := newFormat2Builder(t, dir, 8)
	if err := b.BuildLexicalOnly("lex", lexicalItems()); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	ctx := context.Background()
	if err := s.Load(ctx, "lex"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// One chunk per file.
	rs, err := s.Search(ctx, "channels", WithTopK(10), WithOnePerFile(true))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range rs {
		src, _ := r.Metadata["source"].(string)
		if seen[src] {
			t.Fatalf("OnePerFile returned two chunks of %s", src)
		}
		seen[src] = true
	}
	if len(seen) != 3 {
		t.Errorf("OnePerFile covered %d files, want 3", len(seen))
	}

	// Truncation to topK.
	rs, err = s.Search(ctx, "channels", WithTopK(2))
	if err != nil || len(rs) != 2 {
		t.Fatalf("topK=2 returned %d (%v)", len(rs), err)
	}

	// The reranker sees the whole first-stage pool and has the last word.
	rr := &covReranker{}
	rs, err = s.Search(ctx, "channels", WithTopK(2), WithCustomReranker(rr), WithReranker(true))
	if err != nil {
		t.Fatal(err)
	}
	if rr.pool != 4 {
		t.Errorf("reranker pool = %d, want all 4 lexical matches", rr.pool)
	}
	if len(rs) != 2 {
		t.Errorf("reranked results = %d, want 2", len(rs))
	}

	// Metadata filters go through the lexical filter path.
	rs, err = s.Search(ctx, "channels", WithTopK(10), WithMetadataFilters([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "rust"}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Metadata["lang"] != "rust" {
		t.Errorf("filtered lexical search = %+v, want the rust passage", rs)
	}
}

func TestSearch_EmbedderFallbacks(t *testing.T) {
	dir := t.TempDir()
	initTestHNSW()
	emb := &covEmbedder{dim: 8}
	b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := b.Build(ctx, "idx", lexicalItems()); err != nil {
		t.Fatal(err)
	}
	rust := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "rust"}}

	t.Run("no embedder", func(t *testing.T) {
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if s.Embedder() != nil {
			t.Fatal("Embedder() should be nil")
		}
		rs, err := s.Search(ctx, "tokio", WithTopK(3), WithHybridAlpha(0.5))
		if err != nil || len(rs) != 1 || !strings.Contains(rs[0].Text, "tokio") {
			t.Fatalf("BM25 fallback = %+v (%v)", rs, err)
		}
		rs, err = s.Search(ctx, "channels", WithTopK(3), WithHybridAlpha(0.5), WithMetadataFilters(rust))
		if err != nil || len(rs) != 1 || rs[0].Metadata["lang"] != "rust" {
			t.Fatalf("filtered BM25 fallback = %+v (%v)", rs, err)
		}
	})

	t.Run("query embedding fails", func(t *testing.T) {
		failing := &failingSingleEmbedder{covEmbedder: covEmbedder{dim: 8}}
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, failing)
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		rs, err := s.Search(ctx, "tokio", WithTopK(3), WithHybridAlpha(0.5))
		if err != nil || len(rs) != 1 {
			t.Fatalf("BM25 fallback on embedder error = %+v (%v)", rs, err)
		}
		rs, err = s.Search(ctx, "channels", WithTopK(3), WithHybridAlpha(0.5), WithMetadataFilters(rust))
		if err != nil || len(rs) != 1 || rs[0].Metadata["lang"] != "rust" {
			t.Fatalf("filtered fallback on embedder error = %+v (%v)", rs, err)
		}
		// Without a lexical leg there is nothing to fall back to.
		s.SetScorer(nil)
		if _, err := s.Search(ctx, "tokio", WithTopK(3), WithHybridAlpha(0.5)); err == nil || !strings.Contains(err.Error(), "compute query embedding") {
			t.Fatalf("want the embedding error without a scorer, got %v", err)
		}
	})

	t.Run("wrong query dimensions", func(t *testing.T) {
		bad := &covEmbedder{dim: 8, wrongDims: 3}
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, bad)
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := s.Search(ctx, "tokio", WithTopK(3), WithHybridAlpha(0.5)); err == nil || !strings.Contains(err.Error(), "3 dimensions") {
			t.Fatalf("a 3-dim query against an 8-dim index must be refused, got %v", err)
		}
	})

	t.Run("rerank pool widens retrieval", func(t *testing.T) {
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		rr := &covReranker{}
		s.SetReranker(rr)
		rs, err := s.Search(ctx, "channels", WithTopK(1), WithHybridAlpha(0.5), WithReranker(true), WithRerankPool(100), WithOnePerFile(true))
		if err != nil {
			t.Fatal(err)
		}
		if len(rs) != 1 || rr.pool == 0 {
			t.Fatalf("reranked search = %+v (pool %d)", rs, rr.pool)
		}
		if rr.pool > 3 {
			t.Errorf("OnePerFile should leave at most 3 files for the reranker, got %d", rr.pool)
		}
	})
}

type failingSingleEmbedder struct{ covEmbedder }

func (f *failingSingleEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	return nil, errors.New("query embedding failed")
}

func TestSearch_NotLoaded(t *testing.T) {
	s := NewSearcher(Config{IndexDir: t.TempDir()}, nil)
	if _, err := s.Search(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "Load") {
		t.Fatalf("search before Load = %v, want the not-loaded error", err)
	}
}

func TestLoad_AlternativePaths(t *testing.T) {
	dir := t.TempDir()
	b, emb := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	if err := b.Build(ctx, "idx", lexicalItems()); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "idx", "idx")

	t.Run("custom in-memory scorer is fed with a limit", func(t *testing.T) {
		cfg := Config{IndexDir: dir, Backend: "hnsw"}
		cfg.SearchConfig.MaxBM25Passages = 2
		s := NewSearcher(cfg, emb)
		bm := NewBM25Adapter()
		s.SetScorer(bm)
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if got := bm.IndexedCount(); got != 2 {
			t.Errorf("custom scorer indexed %d passages, want MaxBM25Passages=2", got)
		}
		// Loading again (another index state) replaces the backend cleanly.
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatalf("second Load: %v", err)
		}
		if !s.VectorAvailable() {
			t.Error("vectors unavailable after a reload")
		}
	})

	t.Run("resolver swaps the embedder for the index model", func(t *testing.T) {
		resolved := &covEmbedder{dim: 8}
		var asked string
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &namedCovEmbedder{covEmbedder{dim: 8}, "other-model"})
		s.SetEmbedderResolver(EmbedderResolverFunc(func(ctx context.Context, model string, dims int) (EmbeddingComputer, error) {
			asked = model
			return resolved, nil
		}))
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if asked != "test-model" || s.Embedder() != EmbeddingComputer(resolved) {
			t.Errorf("resolver asked for %q, embedder %T; want the index model and the resolved embedder", asked, s.Embedder())
		}
	})

	t.Run("newer meta version still loads", func(t *testing.T) {
		if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Version = "9.9.9" }); err != nil {
			t.Fatal(err)
		}
		defer UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Version = "1.0.0" })
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatalf("a version mismatch is a warning, got %v", err)
		}
		s.Close()
	})

	t.Run("dimension mismatch is refused", func(t *testing.T) {
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, &covEmbedder{dim: 4})
		if err := s.Load(ctx, "idx"); err == nil || !strings.Contains(err.Error(), "dimension mismatch") {
			t.Fatalf("want a dimension mismatch, got %v", err)
		}
	})

	t.Run("bad names and broken meta", func(t *testing.T) {
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
		if err := s.Load(ctx, "../escape"); err == nil {
			t.Error("a path name must be refused")
		}
		data, err := os.ReadFile(base + ".meta.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(base+".meta.json", []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := s.Load(ctx, "idx"); err == nil || !strings.Contains(err.Error(), "unmarshal metadata") {
			t.Errorf("broken meta.json: %v", err)
		}
		if err := os.WriteFile(base+".meta.json", data, 0o644); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unknown backend fails the vector load", func(t *testing.T) {
		if err := UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Backend = "no-such-backend" }); err != nil {
			t.Fatal(err)
		}
		defer UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Backend = "hnsw" })
		s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
		err := s.Load(ctx, "idx")
		if err == nil || !strings.Contains(err.Error(), "get backend") {
			t.Fatalf("want a backend error, got %v", err)
		}
		// The failed Load released the store: a retry with a good meta works.
		_ = UpdateIndexMeta(dir, "idx", func(m *IndexMeta) { m.Backend = "hnsw" })
		if err := s.Load(ctx, "idx"); err != nil {
			t.Fatalf("retry after a failed Load: %v", err)
		}
		s.Close()
	})
}

type namedCovEmbedder struct {
	covEmbedder
	name string
}

func (n *namedCovEmbedder) ModelName() string { return n.name }

func TestSearchGraphRAG_InjectsGraphNeighbours(t *testing.T) {
	dir := t.TempDir()
	b, emb := newFormat2Builder(t, dir, 8)
	ctx := context.Background()
	items := []Item{
		{Text: "func HandleRequest parses the body", Metadata: map[string]any{"source": "handler.go"}},
		{Text: "unrelated text about cooking", Metadata: map[string]any{"source": "cook.md"}},
	}
	if err := b.Build(ctx, "idx", items); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, emb)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Without a graph the hybrid results come back as they are.
	base, err := s.SearchGraphRAG(ctx, "HandleRequest", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range base {
		if r.ID == -1 {
			t.Fatal("graph results injected without a graph")
		}
	}

	s.graphDB = &mockGraphDB{
		calleesMap: map[string][]Callee{
			"HandleRequest": {
				{FQN: "pkg.ParseJSON", Name: "ParseJSON", File: "json.go", Line: 10},
				{FQN: "pkg.Local", Name: "Local", File: "handler.go", Line: 3}, // already a result
			},
		},
		callersMap: map[string][]Callee{
			"HandleRequest": {{FQN: "main.main", Name: "main", File: "main.go", Line: 7}},
		},
	}
	rs, err := s.SearchGraphRAG(ctx, "HandleRequest is (it)", 10)
	if err != nil {
		t.Fatal(err)
	}
	var calls, calledBy int
	for _, r := range rs {
		if r.ID != -1 {
			continue
		}
		switch {
		case strings.Contains(r.Text, "calls ParseJSON") && r.Metadata["source"] == "json.go":
			calls++
		case strings.Contains(r.Text, "called by main") && r.Metadata["source"] == "main.go":
			calledBy++
		default:
			t.Errorf("unexpected graph result %q", r.Text)
		}
	}
	if calls != 1 || calledBy != 1 {
		t.Errorf("graph results: %d callee, %d caller; want 1 each (handler.go already present)", calls, calledBy)
	}

	rs, err = s.SearchGraphRAG(ctx, "HandleRequest", 1)
	if err != nil || len(rs) != 1 {
		t.Errorf("topK=1 returned %d results (%v)", len(rs), err)
	}
	s.graphDB = nil
}

func TestRemoveIndex_LeftoversAndErrors(t *testing.T) {
	dir := t.TempDir()
	name := "gone"
	for _, p := range []string{
		filepath.Join(dir, name, name+".db"),
		filepath.Join(dir, name+"_graph", "data"),
		filepath.Join(dir, name+".sync-bak", "x"),
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	leftovers := []string{"_graph.wal", "_graph.shadow", "_graph_file_hashes.db", ".rewrite.lock", ".lock", ".lex.lock", ".sync.json"}
	for _, suf := range leftovers {
		if err := os.WriteFile(filepath.Join(dir, name+suf), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(dir, "other.lock")
	if err := os.WriteFile(keep, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveIndex(dir, name); err != nil {
		t.Fatalf("RemoveIndex: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "other.lock" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("after RemoveIndex the directory holds %v, want only other.lock", names)
	}

	if err := RemoveIndex(dir, ".."); err == nil {
		t.Error("RemoveIndex must refuse a path name")
	}

	// A leftover that cannot be removed (a non-empty directory where a file
	// is expected) is reported with its name.
	for _, suf := range leftovers {
		t.Run(suf, func(t *testing.T) {
			blocker := filepath.Join(dir, name+suf, "child")
			if err := os.MkdirAll(blocker, 0o755); err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(filepath.Join(dir, name+suf))
			err := RemoveIndex(dir, name)
			if err == nil {
				t.Fatalf("RemoveIndex succeeded with an unremovable %s", suf)
			}
		})
	}
}
