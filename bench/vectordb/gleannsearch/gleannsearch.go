// Package gleannsearch runs the benchmark dataset through gleann's REAL
// build and search path (gleann.NewBuilder, LeannSearcher.Search with
// metadata filters) instead of a backend adapter: a format-2 index with the
// production defaults. A lookup embedder serves the dataset's vectors by
// text, so no model is needed: passage i is "p<i>", query i is "q<i>".
//
// Metadata of passage i: source, kind, and the tag buckets t1 / t10 / t50
// ("true" for tag < 1, 10, 50), i.e. filters of 1 %, 10 % and 50 %
// selectivity matching GroundTruth.Filtered1Pct / 10Pct / 50Pct.
package gleannsearch

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/tevfik/gleann/bench/vectordb"
	_ "github.com/tevfik/gleann/pkg/backends" // registers the hnsw and diskann backends
	"github.com/tevfik/gleann/pkg/gleann"
)

// IndexName is the name of the index Open builds inside its directory.
const IndexName = "idx"

// LookupEmbedder returns precomputed vectors by text.
type LookupEmbedder struct {
	dims int
	vecs map[string][]float32
}

// Compute implements gleann.EmbeddingComputer.
func (e *LookupEmbedder) Compute(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, ok := e.vecs[t]
		if !ok {
			return nil, fmt.Errorf("no vector for %q", t)
		}
		out[i] = v
	}
	return out, nil
}

// ComputeSingle implements gleann.EmbeddingComputer.
func (e *LookupEmbedder) ComputeSingle(ctx context.Context, text string) ([]float32, error) {
	v, err := e.Compute(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("computing query embedding: %w", err)
	}
	return v[0], nil
}

// Dimensions implements gleann.EmbeddingComputer.
func (e *LookupEmbedder) Dimensions() int { return e.dims }

// ModelName implements gleann.EmbeddingComputer.
func (e *LookupEmbedder) ModelName() string { return "lookup" }

// Options adjust the index configuration; the zero value is gleann's default.
type Options struct {
	Backend  string // "hnsw" (default) or "diskann"
	EfSearch int    // search width: HNSW EfSearch or DiskANN SearchL; 0 = default
	NoMmap   bool   // load the RAM searcher instead of the mmap one
	BuildL   int    // DiskANN build list size; 0 = default
}

// Open builds the dataset into a gleann index under dir (or reuses a
// format-2 index already there) and returns a loaded searcher and whether it
// was built now. The caller closes the searcher.
func Open(ctx context.Context, dir string, ds *vectordb.Dataset, o Options) (*gleann.LeannSearcher, bool, error) {
	emb := &LookupEmbedder{dims: ds.Dims, vecs: make(map[string][]float32, len(ds.Vectors)+len(ds.Queries))}
	items := make([]gleann.Item, len(ds.Vectors))
	for i := range items {
		t := "p" + strconv.Itoa(i)
		emb.vecs[t] = ds.Vectors[i]
		m := ds.Metas[i]
		items[i] = gleann.Item{Text: t, Metadata: map[string]any{
			"source": m.Source, "kind": m.Kind,
			"t1": strconv.FormatBool(m.Tag < 1), "t10": strconv.FormatBool(m.Tag < 10), "t50": strconv.FormatBool(m.Tag < 50),
		}}
	}
	for i, q := range ds.Queries {
		emb.vecs["q"+strconv.Itoa(i)] = q
	}

	// The production defaults (M=32, efConstruction=200, heuristic neighbour
	// selection, mmap).
	cfg := gleann.DefaultConfig()
	cfg.IndexDir, cfg.Backend = dir, "hnsw"
	if o.Backend != "" {
		cfg.Backend = o.Backend
	}
	if o.EfSearch > 0 {
		cfg.HNSWConfig.EfSearch = o.EfSearch
		cfg.DiskANNConfig.SearchL = o.EfSearch
	}
	if o.BuildL > 0 {
		cfg.DiskANNConfig.L = o.BuildL
	}
	if o.NoMmap {
		off := false
		cfg.UseMmap = &off
	}
	built := false
	if gleann.StoreFormatOf(dir+"/"+IndexName+"/"+IndexName) != 2 {
		if err := os.RemoveAll(dir); err != nil {
			return nil, false, fmt.Errorf("clearing %s: %w", dir, err)
		}
		b, err := gleann.NewBuilder(cfg, emb)
		if err != nil {
			return nil, false, fmt.Errorf("creating builder: %w", err)
		}
		if err := b.Build(ctx, IndexName, items); err != nil {
			return nil, false, fmt.Errorf("building index: %w", err)
		}
		built = true
	}
	s := gleann.NewSearcher(cfg, emb)
	if err := s.Load(ctx, IndexName); err != nil {
		return nil, false, fmt.Errorf("loading index: %w", err)
	}
	return s, built, nil
}

// Query runs query i of the dataset and returns the passage numbers of the
// results. field is the tag bucket to filter on ("t1", "t10", "t50"), or ""
// for an unfiltered search. alpha is the hybrid weight (1 = vector only).
func Query(ctx context.Context, s *gleann.LeannSearcher, i, k int, alpha float32, field string) ([]int64, error) {
	opts := []gleann.SearchOption{gleann.WithTopK(k), gleann.WithHybridAlpha(alpha)}
	if field != "" {
		opts = append(opts, gleann.WithMetadataFilters([]gleann.MetadataFilter{{Field: field, Operator: gleann.OpEqual, Value: "true"}}))
	}
	rs, err := s.Search(ctx, "q"+strconv.Itoa(i), opts...)
	if err != nil {
		return nil, fmt.Errorf("searching query %d: %w", i, err)
	}
	ids := make([]int64, 0, len(rs))
	for _, r := range rs {
		n, err := strconv.Atoi(r.Text[1:])
		if err != nil {
			return nil, fmt.Errorf("query %d: unexpected passage text %q", i, r.Text)
		}
		ids = append(ids, int64(n))
	}
	return ids, nil
}
