package backends

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tevfik/gleann/modules/diskann"
	"github.com/tevfik/gleann/modules/hnsw"
	"github.com/tevfik/gleann/pkg/gleann"
)

var errFake = errors.New("fake failure")

// bareHNSW satisfies hnsw.BackendSearcher only (no optional capabilities).
type bareHNSW struct{ recompute hnsw.EmbeddingRecomputer }

func (b *bareHNSW) Load(context.Context, []byte, hnsw.IndexMeta) error { return nil }
func (b *bareHNSW) Search(context.Context, []float32, int) ([]int64, []float32, error) {
	return nil, nil, nil
}
func (b *bareHNSW) SearchWithRecompute(ctx context.Context, _ []float32, _ int, r hnsw.EmbeddingRecomputer) ([]int64, []float32, error) {
	b.recompute = r
	return []int64{1}, []float32{0.5}, nil
}
func (b *bareHNSW) Close() error { return nil }

// capHNSW adds every optional capability and records its calls.
type capHNSW struct {
	bareHNSW
	loadedPath string
	distIDs    []int64
}

func (c *capHNSW) LoadFromFile(_ context.Context, p string) error { c.loadedPath = p; return errFake }
func (c *capHNSW) VectorDistances(_ context.Context, _ []float32, ids []int64) ([]float32, error) {
	c.distIDs = ids
	return []float32{9}, nil
}
func (c *capHNSW) ExtractVectors(_ context.Context, ids []int64) ([][]float32, error) {
	return [][]float32{{float32(ids[0])}}, nil
}

func TestHNSWAdapterOptionalCapabilitiesMissing(t *testing.T) {
	ctx := context.Background()
	s := &searcherAdapter{inner: &bareHNSW{}}
	if _, err := s.VectorDistances(ctx, nil, []int64{1}); err == nil || !strings.Contains(err.Error(), "VectorDistances") {
		t.Errorf("VectorDistances err = %v", err)
	}
	if _, err := s.ExtractVectors(ctx, []int64{1}); err == nil || !strings.Contains(err.Error(), "ExtractVectors") {
		t.Errorf("ExtractVectors err = %v", err)
	}
	m := &mmapSearcherAdapter{searcherAdapter{inner: &bareHNSW{}}}
	if err := m.LoadFromFile(ctx, "/x"); err == nil || !strings.Contains(err.Error(), "LoadFromFile") {
		t.Errorf("LoadFromFile err = %v", err)
	}
}

func TestHNSWAdapterForwardsToCapableInner(t *testing.T) {
	ctx := context.Background()
	inner := &capHNSW{}
	s := &searcherAdapter{inner: inner}

	d, err := s.VectorDistances(ctx, []float32{1}, []int64{4, 5})
	if err != nil || len(d) != 1 || d[0] != 9 || len(inner.distIDs) != 2 {
		t.Errorf("VectorDistances = %v, %v (ids %v)", d, err, inner.distIDs)
	}
	v, err := s.ExtractVectors(ctx, []int64{7})
	if err != nil || len(v) != 1 || v[0][0] != 7 {
		t.Errorf("ExtractVectors = %v, %v", v, err)
	}

	m := &mmapSearcherAdapter{searcherAdapter{inner: inner}}
	if err := m.LoadFromFile(ctx, "/some/path"); !errors.Is(err, errFake) || inner.loadedPath != "/some/path" {
		t.Errorf("LoadFromFile err=%v path=%q", err, inner.loadedPath)
	}
}

func TestHNSWAdapterSearchWithRecomputeForwardsCallback(t *testing.T) {
	inner := &bareHNSW{}
	s := &searcherAdapter{inner: inner}
	called := false
	ids, dist, err := s.SearchWithRecompute(context.Background(), []float32{1}, 1,
		func(_ context.Context, ids []int64) ([][]float32, error) {
			called = true
			return [][]float32{{float32(ids[0])}}, nil
		})
	if err != nil || len(ids) != 1 || ids[0] != 1 || dist[0] != 0.5 {
		t.Fatalf("got %v %v %v", ids, dist, err)
	}
	if inner.recompute == nil {
		t.Fatal("recompute callback not passed to inner searcher")
	}
	out, err := inner.recompute(context.Background(), []int64{3})
	if err != nil || !called || out[0][0] != 3 {
		t.Errorf("wrapped callback: %v %v called=%v", out, err, called)
	}
}

// The real RAM HNSW searcher supports VectorDistances: closer ids score lower.
func TestHNSWRealSearcherVectorDistances(t *testing.T) {
	ctx := context.Background()
	cfg := gleann.DefaultConfig()
	cfg.Backend = "hnsw"
	cfg.IndexDir = t.TempDir()
	no := false
	cfg.UseMmap = &no
	f, err := gleann.GetBackend("hnsw")
	if err != nil {
		t.Fatal(err)
	}
	vecs := make([][]float32, 30)
	for i := range vecs {
		vecs[i] = []float32{float32(i), 1, float32(i % 5), 2}
	}
	data, err := f.NewBuilder(cfg).Build(ctx, vecs)
	if err != nil {
		t.Fatal(err)
	}
	s := f.NewSearcher(cfg)
	defer s.Close()
	if err := s.Load(ctx, data, gleann.IndexMeta{Name: "x", Backend: "hnsw", Dimensions: 4, NumPassages: 30}); err != nil {
		t.Fatal(err)
	}
	vs, ok := s.(gleann.VectorIDScorer)
	if !ok {
		t.Fatal("hnsw searcher is no VectorIDScorer")
	}
	d, err := vs.VectorDistances(ctx, vecs[2], []int64{2, 25})
	if err != nil || len(d) != 2 {
		t.Fatalf("VectorDistances = %v, %v", d, err)
	}
	if !(d[0] < d[1]) {
		t.Errorf("own vector should be nearer than a distant one: %v", d)
	}
	ids, _, err := s.SearchWithRecompute(ctx, vecs[2], 3, func(_ context.Context, ids []int64) ([][]float32, error) {
		out := make([][]float32, len(ids))
		for i, id := range ids {
			out[i] = vecs[id]
		}
		return out, nil
	})
	if err != nil || len(ids) == 0 {
		t.Fatalf("SearchWithRecompute = %v, %v", ids, err)
	}
	if ids[0] != 2 {
		t.Errorf("nearest to vecs[2] = %d, want 2", ids[0])
	}
}

// bareDiskANN satisfies diskann.BackendSearcher only.
type bareDiskANN struct{ recompute diskann.EmbeddingRecomputer }

func (b *bareDiskANN) Load(context.Context, []byte, diskann.IndexMeta) error { return nil }
func (b *bareDiskANN) Search(context.Context, []float32, int) ([]int64, []float32, error) {
	return nil, nil, nil
}
func (b *bareDiskANN) SearchWithRecompute(_ context.Context, _ []float32, _ int, r diskann.EmbeddingRecomputer) ([]int64, []float32, error) {
	b.recompute = r
	return []int64{2}, []float32{0.25}, nil
}
func (b *bareDiskANN) Close() error { return nil }

type capDiskANN struct {
	bareDiskANN
	path string
}

func (c *capDiskANN) LoadFromFile(_ context.Context, p string) error { c.path = p; return errFake }
func (c *capDiskANN) ExtractVectors(_ context.Context, ids []int64) ([][]float32, error) {
	return [][]float32{{float32(ids[0])}}, nil
}

func TestDiskANNAdapterOptionalCapabilities(t *testing.T) {
	ctx := context.Background()
	bare := &diskannMmapSearcherAdapter{diskannSearcherAdapter{inner: &bareDiskANN{}}}
	if err := bare.LoadFromFile(ctx, "/x"); err == nil || !strings.Contains(err.Error(), "LoadFromFile") {
		t.Errorf("LoadFromFile err = %v", err)
	}
	if _, err := bare.ExtractVectors(ctx, []int64{1}); err == nil || !strings.Contains(err.Error(), "ExtractVectors") {
		t.Errorf("ExtractVectors err = %v", err)
	}

	inner := &capDiskANN{}
	capable := &diskannMmapSearcherAdapter{diskannSearcherAdapter{inner: inner}}
	if err := capable.LoadFromFile(ctx, "/p"); !errors.Is(err, errFake) || inner.path != "/p" {
		t.Errorf("LoadFromFile err=%v path=%q", err, inner.path)
	}
	v, err := capable.ExtractVectors(ctx, []int64{6})
	if err != nil || v[0][0] != 6 {
		t.Errorf("ExtractVectors = %v, %v", v, err)
	}
}

func TestDiskANNAdapterSearchWithRecomputeForwardsCallback(t *testing.T) {
	inner := &bareDiskANN{}
	s := &diskannSearcherAdapter{inner: inner}
	ids, dist, err := s.SearchWithRecompute(context.Background(), nil, 1,
		func(_ context.Context, ids []int64) ([][]float32, error) {
			return [][]float32{{float32(ids[0]) * 2}}, nil
		})
	if err != nil || ids[0] != 2 || dist[0] != 0.25 {
		t.Fatalf("got %v %v %v", ids, dist, err)
	}
	out, err := inner.recompute(context.Background(), []int64{4})
	if err != nil || out[0][0] != 8 {
		t.Errorf("wrapped callback = %v, %v", out, err)
	}
}
