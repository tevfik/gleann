package gleann

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/modules/diskann"
)

// This file covers filt-diskann: filtered search on the DiskANN backend (the
// default backend of the default build) must score a selective filter's few
// matches exactly, like HNSW does (filter_exact_small_test.go). The test
// factory wraps the real diskann module the way testHNSWFactory wraps hnsw;
// the adapter forwards VectorDistances only when the inner searcher supports
// it, exactly like pkg/backends/diskann.go.

var registerDiskANNOnce sync.Once

type testDiskANNFactory struct{}

func (f *testDiskANNFactory) Name() string { return "diskann" }
func (f *testDiskANNFactory) NewBuilder(cfg Config) BackendBuilder {
	factory := &diskann.Factory{}
	return factory.NewBuilder(diskann.Config{IndexDir: cfg.IndexDir, Backend: "diskann"})
}
func (f *testDiskANNFactory) NewSearcher(cfg Config) BackendSearcher {
	factory := &diskann.Factory{}
	inner := factory.NewSearcher(diskann.Config{
		IndexDir: cfg.IndexDir,
		Backend:  "diskann",
		DiskANNConfig: diskann.DiskANNConfig{
			DistanceMetric: diskann.DistanceMetric(cfg.DiskANNConfig.DistanceMetric),
			UseMmap:        cfg.MmapEnabled(),
		},
	})
	if cfg.MmapEnabled() {
		return &testDiskANNMmapAdapter{testDiskANNAdapter{inner: inner}}
	}
	return &testDiskANNAdapter{inner: inner}
}

func initTestDiskANN() {
	registerDiskANNOnce.Do(func() {
		if _, err := GetBackend("diskann"); err == nil {
			return
		}
		RegisterBackend(&testDiskANNFactory{})
	})
}

type testDiskANNAdapter struct {
	inner diskann.BackendSearcher
}

func (s *testDiskANNAdapter) Load(ctx context.Context, data []byte, meta IndexMeta) error {
	return s.inner.Load(ctx, data, diskann.IndexMeta{
		Name:        meta.Name,
		Backend:     meta.Backend,
		Dimensions:  meta.Dimensions,
		NumPassages: meta.NumPassages,
	})
}
func (s *testDiskANNAdapter) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	return s.inner.Search(ctx, query, topK)
}
func (s *testDiskANNAdapter) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	return s.inner.SearchWithRecompute(ctx, query, topK, func(ctx context.Context, ids []int64) ([][]float32, error) {
		return recompute(ctx, ids)
	})
}
func (s *testDiskANNAdapter) VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error) {
	if vd, ok := s.inner.(interface {
		VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error)
	}); ok {
		return vd.VectorDistances(ctx, query, ids)
	}
	return nil, fmt.Errorf("diskann test backend does not support VectorDistances")
}
func (s *testDiskANNAdapter) Close() error { return s.inner.Close() }

type testDiskANNMmapAdapter struct {
	testDiskANNAdapter
}

func (s *testDiskANNMmapAdapter) LoadFromFile(ctx context.Context, path string) error {
	if m, ok := s.inner.(interface {
		LoadFromFile(ctx context.Context, path string) error
	}); ok {
		return m.LoadFromFile(ctx, path)
	}
	return fmt.Errorf("diskann test backend does not support LoadFromFile")
}

// newDiskANNSearcher builds and loads a format-2 DiskANN index through
// NewBuilder (the CLI path), in RAM or mmap mode.
func newDiskANNSearcher(t *testing.T, items []Item, mmap bool) *LeannSearcher {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	initTestDiskANN()
	dir := t.TempDir()
	embedder := &testEmbedder{dim: 8}
	cfg := Config{IndexDir: dir, Backend: "diskann"}
	cfg.UseMmap = &mmap
	b, err := NewBuilder(cfg, embedder)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	ctx := context.Background()
	if err := b.Build(ctx, "idx", items); err != nil {
		t.Fatalf("Build: %v", err)
	}
	s := NewSearcher(cfg, embedder)
	if err := s.Load(ctx, "idx"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// diskANNFilterItems builds n passages sharing the query terms, with texts of
// distinct lengths (the test embedder's vectors depend on text length, so
// vector rank follows length and no two distances tie). Every 12th passage
// carries lang=go (10 of 120).
func diskANNFilterItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		lang := "rust"
		if i%12 == 0 {
			lang = "go"
		}
		items[i] = Item{
			Text: "common query term " + strings.Repeat("x", i),
			Metadata: map[string]any{
				"source": fmt.Sprintf("f%04d.md", i),
				"lang":   lang,
			},
		}
	}
	return items
}

// TestFilteredSearch_DiskANNPreselectScoresExactly: on a DiskANN index the
// exact pre-selection path (searchPreselectedLimit, the function the
// widen-until-k loop and the cap fallback call) must score the filter's
// matches by exact vector distance — tier 1, vectorOnly — on the RAM and the
// mmap path. Before the fix the DiskANN searcher has no VectorDistances, so
// tier 1 cannot run and vectorOnly reports ok=false: a selective filter on
// the default backend stays approximate (recall@10 ~0.5-0.6 at 1%
// selectivity, bench/vectordb).
func TestFilteredSearch_DiskANNPreselectScoresExactly(t *testing.T) {
	items := diskANNFilterItems(120)
	ctx := context.Background()
	for _, mmap := range []bool{false, true} {
		name := "ram"
		if mmap {
			name = "mmap"
		}
		t.Run(name, func(t *testing.T) {
			s := newDiskANNSearcher(t, items, mmap)
			emb := &testEmbedder{dim: 8}
			rq := "common query term"
			queryEmb, err := emb.ComputeSingle(ctx, s.meta.QueryPrefix+rq)
			if err != nil {
				t.Fatalf("query embedding: %v", err)
			}
			vecOf := func(i int) []float32 {
				vs, err := emb.Compute(ctx, withPrefix(s.meta.PassagePrefix, []string{items[i].Text}))
				if err != nil {
					t.Fatalf("passage embedding: %v", err)
				}
				return vs[0]
			}
			// The true distances of the matches, best first: rank follows
			// text length, so the matches order by slot (0, 12, ..., 108).
			type cand struct {
				slot int64
				dist float32
			}
			var matches []cand
			for i, it := range items {
				if it.Metadata["lang"] == "go" {
					matches = append(matches, cand{int64(i), diskann.L2DistanceSquared(queryEmb, vecOf(i))})
				}
			}
			sort.Slice(matches, func(a, b int) bool { return matches[a].dist < matches[b].dist })
			// The loop's fixed normalizer: the largest distance among the
			// first retrieveK=50 candidates (slot 49, rank follows length).
			normDist := diskann.L2DistanceSquared(queryEmb, vecOf(49))

			engine := NewMetadataFilterEngine([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}})
			rs, exact, ok := s.searchPreselectedLimit(ctx, rq, rq, queryEmb, SearchConfig{HybridAlpha: 1.0}, 3, engine, normDist, 2*filterExactMax, true, true)
			if !ok || !exact {
				t.Fatalf("pre-selection did not score the matches by vector (ok=%v exact=%v): DiskANN filtered search has no exact path", ok, exact)
			}
			// searchPreselectedLimit returns a scored pool of up to
			// max(4*topK, 50) candidates (the caller truncates to topK);
			// all 10 matches fit, in exact-score order.
			if len(rs) != len(matches) {
				t.Fatalf("got %d results, want all %d matches scored exactly", len(rs), len(matches))
			}
			for i, r := range rs {
				want := matches[i]
				if r.ID != want.slot {
					t.Errorf("result %d = slot %d (%q), want slot %d (exact-score order among the matches)", i, r.ID, r.Text, want.slot)
				}
				if r.VectorDistance == nil || *r.VectorDistance != want.dist {
					t.Errorf("slot %d: vector distance = %v, want exact %v", want.slot, r.VectorDistance, want.dist)
				}
				wantScore := 1 - want.dist/normDist
				if d := r.Score - wantScore; d > 1e-5 || d < -1e-5 {
					t.Errorf("slot %d: score %v, want %v (loop normalizer)", want.slot, r.Score, wantScore)
				}
			}
		})
	}
}

// TestFilteredSearch_DiskANNFewMatchesReturnsK is a GUARD, not a red test: it
// passes before and after the fix (on a 120-passage index the approximate
// loop already finds the matches). It pins the public behaviour every surface
// shares: a selective filter on DiskANN returns k results, all matching.
func TestFilteredSearch_DiskANNFewMatchesReturnsK(t *testing.T) {
	items := diskANNFilterItems(120)
	ctx := context.Background()
	for _, mmap := range []bool{false, true} {
		name := "ram"
		if mmap {
			name = "mmap"
		}
		t.Run(name, func(t *testing.T) {
			s := newDiskANNSearcher(t, items, mmap)
			rs, err := s.Search(ctx, "common query term", WithTopK(3), WithHybridAlpha(1.0),
				WithMetadataFilters([]MetadataFilter{{Field: "lang", Operator: OpEqual, Value: "go"}}))
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(rs) != 3 {
				t.Fatalf("got %d results, want 3", len(rs))
			}
			for _, r := range rs {
				if r.Metadata["lang"] != "go" {
					t.Errorf("result %d has lang=%v, want go", r.ID, r.Metadata["lang"])
				}
			}
		})
	}
}
