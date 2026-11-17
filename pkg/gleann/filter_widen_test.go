package gleann

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// This file covers WP-2.5 (filtered search returns k): a selective metadata
// filter must still return k results when k matching passages exist, on the
// vector leg, the lexical leg and hybrid fusion, in store format 1 and 2.

// widenTestItems builds n passages sharing the query terms, with texts of
// distinct lengths (the test embedder's vectors depend on text length, so
// vector rank follows length). Every 33rd passage carries lang=go (~3%);
// the rest lang=rust. The matches are spread evenly over the rank order, so
// a single top-50 retrieval sees only ~2 of them.
func widenTestItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		lang := "rust"
		if i%33 == 0 {
			lang = "go"
		}
		items[i] = Item{
			Text: "common query term " + strings.Repeat("x", i),
			Metadata: map[string]any{
				"source": fmt.Sprintf("f%04d.md", i),
				"lang":   lang,
				"seq":    i,
			},
		}
	}
	return items
}

// newWidenSearcher builds and loads an index through NewBuilder (the CLI
// path) in the requested store format, with the default HNSW parameters.
func newWidenSearcher(t *testing.T, format2 bool, items []Item) *LeannSearcher {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initTestHNSW()
	dir := t.TempDir()
	// Set both ways: the suite also runs under GLEANN_STORE_FORMAT=2.
	t.Setenv("GLEANN_STORE_FORMAT", "")
	if format2 {
		t.Setenv("GLEANN_STORE_FORMAT", "2")
	}
	embedder := &testEmbedder{dim: 8}
	b, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	ctx := context.Background()
	if err := b.Build(ctx, "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	base := filepath.Join(dir, "idx", "idx")
	wantFormat := 1
	if format2 {
		wantFormat = 2
	}
	if got := StoreFormatOf(base); got != wantFormat {
		t.Fatalf("store format = %d, want %d", got, wantFormat)
	}
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestFilteredSearch_LowSelectivityReturnsK is the E2E case IDX-12: ~3% of
// 990 passages match the filter (30 matches), and a k=10 search must return
// exactly 10 on every retrieval leg, in both store formats. The old code
// filtered a single top-50 pool and returned ~2.
func TestFilteredSearch_LowSelectivityReturnsK(t *testing.T) {
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}
	for _, format2 := range []bool{false, true} {
		name := "format1"
		if format2 {
			name = "format2"
		}
		t.Run(name, func(t *testing.T) {
			s := newWidenSearcher(t, format2, widenTestItems(990))
			ctx := context.Background()
			for _, alpha := range []float32{1.0, 0.5, 0.0} {
				t.Run(fmt.Sprintf("alpha%.1f", alpha), func(t *testing.T) {
					rs, err := s.Search(ctx, "common query term",
						WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters))
					if err != nil {
						t.Fatalf("Search: %v", err)
					}
					if len(rs) != 10 {
						t.Fatalf("filtered search returned %d results, want exactly 10 (30 passages match)", len(rs))
					}
					for _, r := range rs {
						if r.Metadata["lang"] != "go" {
							t.Errorf("result %d has lang=%v, want go", r.ID, r.Metadata["lang"])
						}
					}
				})
			}
		})
	}
}

// widenDeepMatchItems builds n passages whose lang=go matches are the LAST m in
// rank order: text length grows with i, so the test embedder ranks shortest
// (i=0) first and the matches (i>=n-m) rank deepest. With n>filterPoolCap the
// widen loop hits the cap having seen none of the matches, forcing the exact
// format-2 pre-selection fallback.
func widenDeepMatchItems(n, m int) []Item {
	items := make([]Item, n)
	for i := range items {
		lang := "rust"
		if i >= n-m {
			lang = "go"
		}
		items[i] = Item{
			Text: "common query term " + strings.Repeat("x", i),
			Metadata: map[string]any{
				"source": fmt.Sprintf("f%05d.md", i),
				"lang":   lang,
			},
		}
	}
	return items
}

// TestFilteredSearch_DeepMatchesFallback is the WP-2.5 follow-up: 10 matches
// ranked beyond filterPoolCap in a 5000-passage format-2 index. The widen loop
// caps with 0 results; the exact SQL pre-selection must then score the 10 deep
// matches and return k=10 on every leg. The old preselect-first code returned 0
// (its loop stopped at the cap with seenAllowed=0 < len(allowed)=10).
func TestFilteredSearch_DeepMatchesFallback(t *testing.T) {
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}
	s := newWidenSearcher(t, true, widenDeepMatchItems(5000, 10))
	ctx := context.Background()
	for _, alpha := range []float32{1.0, 0.5, 0.0} {
		t.Run(fmt.Sprintf("alpha%.1f", alpha), func(t *testing.T) {
			rs, err := s.Search(ctx, "common query term",
				WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters))
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(rs) != 10 {
				t.Fatalf("deep-match filtered search returned %d results, want exactly 10 (10 passages match beyond the cap)", len(rs))
			}
			for _, r := range rs {
				if r.Metadata["lang"] != "go" {
					t.Errorf("result %d has lang=%v, want go", r.ID, r.Metadata["lang"])
				}
			}
		})
	}
}

// countingSearcher wraps a backend and records how often and how wide it was
// asked, so tests can prove the fast zero path and the pool cap.
type countingSearcher struct {
	inner BackendSearcher
	mu    sync.Mutex
	calls int
	maxK  int
}

func (c *countingSearcher) record(topK int) {
	c.mu.Lock()
	c.calls++
	if topK > c.maxK {
		c.maxK = topK
	}
	c.mu.Unlock()
}

func (c *countingSearcher) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	return c.inner.Load(ctx, indexData, meta)
}

func (c *countingSearcher) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	c.record(topK)
	return c.inner.Search(ctx, query, topK)
}

func (c *countingSearcher) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	c.record(topK)
	return c.inner.SearchWithRecompute(ctx, query, topK, recompute)
}

func (c *countingSearcher) Close() error { return c.inner.Close() }

// TestFilteredSearch_NoMatchReturnsZero: a filter nothing matches returns 0
// results. The widen-until-k loop runs FIRST (the vector leg retrieves, bounded
// by the cap or corpus exhaustion); the format-2 SQL pre-selection is only a
// post-cap fallback, not a pre-retrieval short-circuit. So a no-match filter
// still makes backend calls on the vector leg — unlike the old preselect-first
// code, which returned 0 without any retrieval.
func TestFilteredSearch_NoMatchReturnsZero(t *testing.T) {
	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "nope"}}
	for _, format2 := range []bool{false, true} {
		name := "format1"
		if format2 {
			name = "format2"
		}
		t.Run(name, func(t *testing.T) {
			s := newWidenSearcher(t, format2, widenTestItems(300))
			counter := &countingSearcher{inner: s.backend}
			s.backend = counter
			ctx := context.Background()
			for _, alpha := range []float32{1.0, 0.5, 0.0} {
				counter.calls, counter.maxK = 0, 0
				rs, err := s.Search(ctx, "common query term",
					WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters))
				if err != nil {
					t.Fatalf("Search: %v", err)
				}
				if len(rs) != 0 {
					t.Fatalf("no-match filter returned %d results, want 0", len(rs))
				}
				if format2 && alpha > 0 && counter.calls == 0 {
					t.Errorf("format-2 no-match filter made 0 backend calls; the widen loop must run first (pre-selection is a post-cap fallback, not a short-circuit)")
				}
				if counter.maxK > filterPoolCap {
					t.Errorf("pool grew to %d, beyond the cap %d", counter.maxK, filterPoolCap)
				}
			}
		})
	}
}

// endlessBackend always returns exactly topK hits (ids 0..topK-1), even past
// the real corpus, so the widen loop cannot stop on exhaustion and must stop
// on the cap.
type endlessBackend struct {
	calls int
	maxK  int
}

func (e *endlessBackend) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	return nil
}

func (e *endlessBackend) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	e.calls++
	if topK > e.maxK {
		e.maxK = topK
	}
	ids := make([]int64, topK)
	dists := make([]float32, topK)
	for i := range ids {
		ids[i] = int64(i)
		dists[i] = float32(i) * 0.001
	}
	return ids, dists, nil
}

func (e *endlessBackend) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	return e.Search(ctx, query, topK)
}

func (e *endlessBackend) Close() error { return nil }

type endlessFactory struct{ backend *endlessBackend }

func (f endlessFactory) Name() string                            { return "mock-endless-backend" }
func (f endlessFactory) NewBuilder(config Config) BackendBuilder { return nil }
func (f endlessFactory) NewSearcher(config Config) BackendSearcher {
	return f.backend
}

var registerEndlessOnce sync.Once

// TestFilteredSearch_CapBoundsWork: with a backend that never exhausts and a
// filter that matches nothing, the pool doubles from 50 to filterPoolCap and
// stops there — 8 retrievals, never wider than the cap.
func TestFilteredSearch_CapBoundsWork(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	backend := &endlessBackend{}
	registerEndlessOnce.Do(func() { RegisterBackend(endlessFactory{backend: backend}) })

	dir := t.TempDir()
	indexName := "captest"
	idxDir := filepath.Join(dir, indexName)
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(idxDir, indexName)
	pm := NewPassageManager(basePath)
	items := make([]Item, 20)
	for i := range items {
		items[i] = Item{
			Text:     fmt.Sprintf("common query term passage %d", i),
			Metadata: map[string]any{"source": fmt.Sprintf("f%d.md", i), "lang": "rust"},
		}
	}
	if _, err := pm.Add(items); err != nil {
		t.Fatal(err)
	}
	pm.Close()
	if err := os.WriteFile(basePath+".index", []byte("mock"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := IndexMeta{Name: indexName, Backend: "mock-endless-backend", EmbeddingModel: "mock-embedder", NumPassages: len(items)}
	metaBytes, _ := meta.MarshalJSON()
	if err := os.WriteFile(basePath+".meta.json", metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewSearcher(Config{IndexDir: dir}, &mockEmbeddingComputer{})
	if err := s.Load(context.Background(), indexName); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	filters := []MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "nope"}}
	for _, alpha := range []float32{1.0, 0.5} {
		backend.calls, backend.maxK = 0, 0
		rs, err := s.Search(context.Background(), "common query term",
			WithTopK(10), WithHybridAlpha(alpha), WithMetadataFilters(filters))
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(rs) != 0 {
			t.Fatalf("no-match filter returned %d results, want 0", len(rs))
		}
		if backend.maxK != filterPoolCap {
			t.Errorf("alpha %.1f: widest pool = %d, want the cap %d", alpha, backend.maxK, filterPoolCap)
		}
		// Nothing passes, so the pool grows 8x per round: 50, 400, 3200, 4096.
		if backend.calls != 4 {
			t.Errorf("alpha %.1f: %d retrievals, want 4 (8x growth to the cap)", alpha, backend.calls)
		}
	}
}

// fixedDistBackend returns a fixed ranked list with distinct distances so
// the unfiltered golden below is deterministic.
type fixedDistBackend struct{}

func (fixedDistBackend) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	return nil
}

func (fixedDistBackend) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	return []int64{0, 1, 2}, []float32{0.1, 0.3, 0.5}, nil
}

func (fixedDistBackend) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	return fixedDistBackend{}.Search(ctx, query, topK)
}

func (fixedDistBackend) Close() error { return nil }

type fixedDistFactory struct{}

func (fixedDistFactory) Name() string                            { return "mock-fixed-backend" }
func (fixedDistFactory) NewBuilder(config Config) BackendBuilder { return nil }
func (fixedDistFactory) NewSearcher(config Config) BackendSearcher {
	return fixedDistBackend{}
}

var registerFixedOnce sync.Once

// TestUnfilteredSearch_Unchanged proves the fix leaves unfiltered search
// byte-identical: exact IDs, order, scores and raw distance/BM25 fields on
// all three legs. It is a golden test and passes on the old code too — that
// is the point: no change without filters.
func TestUnfilteredSearch_Unchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	registerFixedOnce.Do(func() { RegisterBackend(fixedDistFactory{}) })
	dir := t.TempDir()
	indexName := "golden"
	idxDir := filepath.Join(dir, indexName)
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(idxDir, indexName)
	pm := NewPassageManager(basePath)
	items := []Item{
		{Text: "beta", Metadata: map[string]any{"source": "a.go", "name": "Aaa"}},
		{Text: "beta gamma", Metadata: map[string]any{"source": "b.go", "name": "Bbb"}},
		{Text: "beta gamma delta", Metadata: map[string]any{"source": "c.go", "name": "Ccc"}},
	}
	if _, err := pm.Add(items); err != nil {
		t.Fatal(err)
	}
	pm.Close()
	if err := os.WriteFile(basePath+".index", []byte("mock"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := IndexMeta{Name: indexName, Backend: "mock-fixed-backend", EmbeddingModel: "mock-embedder", NumPassages: len(items)}
	metaBytes, _ := meta.MarshalJSON()
	if err := os.WriteFile(basePath+".meta.json", metaBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSearcher(Config{IndexDir: dir}, &mockEmbeddingComputer{})
	if err := s.Load(context.Background(), indexName); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()
	// Vector-only: scores 1 - d/maxDist, in the same float32 arithmetic the
	// searcher uses (untyped constant math would round differently).
	rs, err := s.Search(ctx, "beta", WithTopK(3), WithHybridAlpha(1.0))
	if err != nil {
		t.Fatal(err)
	}
	wantDists := []float32{0.1, 0.3, 0.5}
	maxDist := float32(0.5)
	wantScores := make([]float32, 3)
	for i, d := range wantDists {
		wantScores[i] = 1.0 - d/maxDist
	}
	if len(rs) != 3 {
		t.Fatalf("vector leg returned %d results, want 3", len(rs))
	}
	for i, r := range rs {
		if r.ID != int64(i) || r.Score != wantScores[i] {
			t.Errorf("vector leg result %d: ID=%d score=%v, want ID=%d score=%v", i, r.ID, r.Score, i, wantScores[i])
		}
		if r.VectorDistance == nil || *r.VectorDistance != wantDists[i] {
			t.Errorf("vector leg result %d: VectorDistance=%v, want %v", i, r.VectorDistance, wantDists[i])
		}
		if r.BM25Score != nil {
			t.Errorf("vector leg result %d: BM25Score=%v, want nil", i, *r.BM25Score)
		}
	}

	// Hybrid: both legs rank [0,1,2]; RRF (k=60, alpha=0.5) normalized by
	// the rank-1 maximum, in the searcher's float32 arithmetic.
	rs, err = s.Search(ctx, "beta", WithTopK(3), WithHybridAlpha(0.5))
	if err != nil {
		t.Fatal(err)
	}
	rrf := func(rank float32) float32 {
		return 0.5*(1.0/(60.0+rank)) + 0.5*(1.0/(60.0+rank))
	}
	wantRRF := []float32{rrf(1) / rrf(1), rrf(2) / rrf(1), rrf(3) / rrf(1)}
	if len(rs) != 3 {
		t.Fatalf("hybrid returned %d results, want 3", len(rs))
	}
	for i, r := range rs {
		if r.ID != int64(i) || r.Score != wantRRF[i] {
			t.Errorf("hybrid result %d: ID=%d score=%v, want ID=%d score=%v", i, r.ID, r.Score, i, wantRRF[i])
		}
		if r.VectorDistance == nil || *r.VectorDistance != wantDists[i] {
			t.Errorf("hybrid result %d: VectorDistance=%v, want %v", i, r.VectorDistance, wantDists[i])
		}
		if r.BM25Score == nil {
			t.Errorf("hybrid result %d: BM25Score missing", i)
		}
	}

	// Lexical-only: BM25 order (shorter doc first), raw BM25 scores kept.
	rs, err = s.Search(ctx, "beta", WithTopK(3), WithHybridAlpha(0.0))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("lexical leg returned %d results, want 3", len(rs))
	}
	for i, r := range rs {
		if r.ID != int64(i) {
			t.Errorf("lexical leg result %d: ID=%d, want %d", i, r.ID, i)
		}
		if r.BM25Score == nil || *r.BM25Score != r.Score {
			t.Errorf("lexical leg result %d: BM25Score=%v score=%v, want equal", i, r.BM25Score, r.Score)
		}
		if r.VectorDistance != nil {
			t.Errorf("lexical leg result %d: VectorDistance=%v, want nil", i, *r.VectorDistance)
		}
	}
	if !(rs[0].Score > rs[1].Score && rs[1].Score > rs[2].Score) {
		t.Errorf("lexical leg scores not strictly descending: %v %v %v", rs[0].Score, rs[1].Score, rs[2].Score)
	}
}

// TestPreselectFilterIDs_MatchesEngine checks the SQL pre-selection against
// the authoritative engine on a corpus with awkward metadata: aliases
// (source/file, type/kind), numeric and boolean values, JSON null, nested
// objects and arrays, unicode, LIKE/GLOB metacharacters, quotes and missing
// fields. Where the compiler answers, its verified set must equal the
// engine's set exactly; the raw SQL set must be a superset.
func TestPreselectFilterIDs_MatchesEngine(t *testing.T) {
	metas := []map[string]any{
		{"lang": "go", "seq": 1, "flag": true, "tag": "a.b", "nested": map[string]any{"x": 1}, "arr": []any{1, 2}, "nullv": nil, "empty": ""},
		{"lang": "rust", "seq": 2.5, "flag": false, "file": "alt.md"},
		{"kind": "function", "type": "other"},
		{"type": "function"},
		{"source": "x.go", "file": "y.md"},
		{"file": "z.md"},
		{"lang": "5", "seq": "7"},
		{"lang": "éü", "uni": "日本語"},
		{"lang": "a%b_c", "glob": "a*b?c"},
		{"lang": `q"uote`, "bs": `back\slash`},
		{"lang": ""},
		{},
	}
	items := make([]Item, len(metas))
	for i, md := range metas {
		items[i] = Item{Text: fmt.Sprintf("preselect doc %d", i), Metadata: md}
	}
	s := newWidenSearcher(t, true, items)
	sp, ok := s.passages.(*storePassages)
	if !ok {
		t.Fatalf("expected a format-2 store, got %T", s.passages)
	}
	all := s.passages.All()

	fields := []string{"lang", "seq", "flag", "tag", "nested", "arr", "nullv", "empty", "source", "file", "type", "kind", "missing", "uni", "glob", "bs", "chunk_index", "ext"}
	values := []any{"go", "", "a", "5", 5, true, nil, "<nil>", "é", "%", "*", []any{"go", "rust"}, []string{"go"}, []any{}, "function", ".md"}
	ops := []FilterOperator{OpEqual, OpNotEqual, OpContains, OpStartsWith, OpEndsWith, OpIn, OpNotIn, OpExists}

	compilable := 0
	for _, field := range fields {
		for _, op := range ops {
			for _, value := range values {
				for _, logic := range []string{"and", "or"} {
					filters := []MetadataFilter{
						{Field: field, Operator: op, Value: value},
						{Field: "lang", Operator: OpEqual, Value: "go"},
					}
					engine := NewMetadataFilterEngine(filters)
					engine.Logic = logic
					want := make(map[int64]bool)
					for _, p := range all {
						if engine.Match(p.Metadata) {
							want[p.ID] = true
						}
					}
					got, ran := s.preselectFilterIDs(context.Background(), engine)
					if !ran {
						continue
					}
					compilable++
					if len(got) != len(want) {
						t.Fatalf("field=%s op=%s value=%v logic=%s: preselect %d matches, engine %d", field, op, value, logic, len(got), len(want))
					}
					for id := range want {
						rowid, ok := got[id]
						if !ok {
							t.Fatalf("field=%s op=%s value=%v logic=%s: preselect missed passage %d", field, op, value, logic, id)
						}
						if rowid <= 0 {
							t.Fatalf("field=%s op=%s value=%v logic=%s: passage %d mapped to invalid rowid %d", field, op, value, logic, id, rowid)
						}
					}
					// The raw SQL set must be a superset of the engine's matches.
					where, args, ok := compileFilterSQL(filters, logic)
					if !ok {
						t.Fatalf("preselect ran but compileFilterSQL reports not compilable")
					}
					rows, err := sp.st.SelectByPredicate(context.Background(), where, args, 0)
					if err != nil {
						t.Fatalf("SelectByPredicate: %v", err)
					}
					raw := make(map[int64]bool, len(rows))
					for _, p := range rows {
						if p.Text != "" {
							t.Fatalf("SelectByPredicate must not read passage text (got %q)", p.Text)
						}
						raw[storePassageToGleann(p).ID] = true
					}
					for id := range want {
						if !raw[id] {
							t.Fatalf("field=%s op=%s value=%v logic=%s: SQL superset missed passage %d", field, op, value, logic, id)
						}
					}
				}
			}
		}
	}
	if compilable == 0 {
		t.Fatal("no filter combination was compilable; the compiler is dead code")
	}
	t.Logf("%d filter combinations compiled and verified against the engine", compilable)

	// Numeric operators stay on the loop fallback.
	if _, _, ok := compileFilterSQL([]MetadataFilter{{Field: "seq", Operator: OpGreaterThan, Value: 1}}, "and"); ok {
		t.Error("numeric gt must not be compiled (loop fallback)")
	}
}

// widenBenchItems is the cost fixture: shared query terms, text length cycling
// over 500 values (distinct-ish vectors), and three selectivity flags so one
// build serves the 50% (lang2), 3% (lang) and 0.01% (lang10000) filters.
func widenBenchItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		lang, lang2, lang10000 := "rust", "rust", "rust"
		if i%33 == 0 {
			lang = "go"
		}
		if i%2 == 0 {
			lang2 = "go"
		}
		if i%10000 == 0 {
			lang10000 = "go"
		}
		items[i] = Item{
			Text: fmt.Sprintf("common query term doc %06d ", i) + strings.Repeat("p", i%500),
			Metadata: map[string]any{
				"source":    fmt.Sprintf("f%05d.md", i/20),
				"lang":      lang,
				"lang2":     lang2,
				"lang10000": lang10000,
			},
		}
	}
	return items
}

// BenchmarkFilteredSearch measures the WP-2.5 follow-up on 20K and 200K
// synthetic format-2 indexes (mock embedder, GLEANN_STORE_FORMAT=2): the
// unfiltered baseline against filtered search at 50%, 3% and 0.01% selectivity
// (k=10). The 0.01% filter ranks its matches deep, exercising the post-cap SQL
// pre-selection fallback; 50% and 3% are answered by the widen loop alone.
func BenchmarkFilteredSearch(b *testing.B) {
	sel := []struct {
		name  string
		field string
		mod   int
	}{
		{"50pct", "lang2", 2},
		{"3pct", "lang", 33},
		{"0.01pct", "lang10000", 10000},
	}
	for _, n := range []int{20000, 200000} {
		b.Run(fmt.Sprintf("%d", n), func(b *testing.B) {
			home := b.TempDir()
			b.Setenv("HOME", home)
			b.Setenv("USERPROFILE", home)
			b.Setenv("GLEANN_STORE_FORMAT", "2")
			initTestHNSW()
			dir := b.TempDir()
			embedder := &testEmbedder{dim: 8}
			builder, err := NewBuilder(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
			if err != nil {
				b.Fatalf("NewBuilder: %v", err)
			}
			ctx := context.Background()
			if err := builder.Build(ctx, "idx", widenBenchItems(n)); err != nil {
				b.Fatalf("Build: %v", err)
			}
			s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, embedder)
			if err := s.Load(ctx, "idx"); err != nil {
				b.Fatalf("Load: %v", err)
			}
			defer s.Close()
			b.Run("unfiltered", func(b *testing.B) {
				for b.Loop() {
					if _, err := s.Search(ctx, "common query term", WithTopK(10)); err != nil {
						b.Fatal(err)
					}
				}
			})
			for _, sc := range sel {
				filters := []MetadataFilter{{Field: sc.field, Operator: OpEqual, Value: "go"}}
				want := n / sc.mod
				if want > 10 {
					want = 10
				}
				if rs, err := s.Search(ctx, "common query term", WithTopK(10), WithMetadataFilters(filters)); err != nil || len(rs) != want {
					b.Fatalf("%s sanity: %d results (%v), want %d", sc.name, len(rs), err, want)
				}
				b.Run("filtered_"+sc.name, func(b *testing.B) {
					for b.Loop() {
						if _, err := s.Search(ctx, "common query term", WithTopK(10), WithMetadataFilters(filters)); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func TestNextFilterPool(t *testing.T) {
	cases := []struct{ pool, passed, examined, k, want int }{
		{40, 1, 40, 10, 600}, // 2.5% pass rate: 10/0.025*1.5
		{40, 0, 40, 10, 320}, // nothing passed yet: 8x
		{40, 20, 40, 10, 80}, // estimate below 2x: at least doubles
		{3000, 1, 3000, 10, filterPoolCap},
	}
	for _, c := range cases {
		if got := nextFilterPool(c.pool, c.passed, c.examined, c.k); got != c.want {
			t.Errorf("nextFilterPool(%d,%d,%d,%d) = %d, want %d", c.pool, c.passed, c.examined, c.k, got, c.want)
		}
	}
}
