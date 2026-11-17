package hnsw

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

// smallVectors returns n deterministic vectors of the given dimension. The
// sizes in this file stay tiny on purpose: an HNSW build is quadratic-ish and
// an earlier test that was careless about size exhausted memory.
func smallVectors(n, dims int, seed int64) [][]float32 {
	rng := rand.New(rand.NewSource(seed))
	out := make([][]float32, n)
	for i := range out {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		out[i] = v
	}
	return out
}

func buildSmall(t *testing.T, cfg Config, n, dims int) ([]byte, [][]float32) {
	t.Helper()
	emb := smallVectors(n, dims, 7)
	data, err := (&Builder{config: cfg}).Build(context.Background(), emb)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return data, emb
}

func loadSearcher(t *testing.T, cfg Config, data []byte) *Searcher {
	t.Helper()
	s := &Searcher{config: cfg}
	if err := s.Load(context.Background(), data, IndexMeta{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}

// A config that leaves M and EfConstruction at zero gets the documented
// defaults (32 and 200) rather than a degenerate graph, and the index is
// stored as float32 unless a dtype says otherwise.
func TestBuildAppliesDefaultsToZeroConfig(t *testing.T) {
	data, emb := buildSmall(t, Config{}, 12, 6)
	csr, err := ReadCSR(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if csr.VectorDType != VectorDTypeF32 {
		t.Fatalf("dtype = %q, want %q by default", csr.VectorDType, VectorDTypeF32)
	}
	s := loadSearcher(t, Config{}, data)
	ids, _, err := s.Search(context.Background(), emb[3], 1)
	if err != nil || len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("an indexed vector must be its own nearest neighbour: %v, %v", ids, err)
	}
}

func TestBuildNormalisesDType(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 20
	cfg.HNSWConfig.VectorDType = "  F16 "
	data, _ := buildSmall(t, cfg, 8, 4)
	csr, err := ReadCSR(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if csr.VectorDType != VectorDTypeF16 {
		t.Fatalf("dtype = %q, want %q", csr.VectorDType, VectorDTypeF16)
	}
}

// A canceled context stops Build and AddVectors before the graph is done and
// returns the context's error.
func TestBuildAndAddVectorsHonourCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 20
	data, _ := buildSmall(t, cfg, 6, 4)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &Builder{config: cfg}
	if _, err := b.Build(ctx, smallVectors(4, 4, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Build err = %v, want context.Canceled", err)
	}
	if _, err := b.AddVectors(ctx, data, smallVectors(2, 4, 2), 6); !errors.Is(err, context.Canceled) {
		t.Fatalf("AddVectors err = %v, want context.Canceled", err)
	}
}

func TestAddVectorsRejectsCorruptIndex(t *testing.T) {
	b := &Builder{config: DefaultConfig()}
	_, err := b.AddVectors(context.Background(), []byte("not an index"), smallVectors(1, 4, 1), 0)
	if err == nil || !strings.Contains(err.Error(), "load index") {
		t.Fatalf("err = %v, want a load failure", err)
	}
}

// AddVectors keeps the stored dtype unless the config names another, honours
// the prune setting, and the vectors it adds are searchable.
func TestAddVectorsDTypeAndPruneOptions(t *testing.T) {
	base := DefaultConfig()
	base.HNSWConfig.M = 4
	base.HNSWConfig.EfConstruction = 20
	data, _ := buildSmall(t, base, 8, 4)
	ctx := context.Background()

	t.Run("keeps the stored dtype", func(t *testing.T) {
		out, err := (&Builder{config: base}).AddVectors(ctx, data, smallVectors(2, 4, 3), 8)
		if err != nil {
			t.Fatal(err)
		}
		csr, err := ReadCSR(strings.NewReader(string(out)))
		if err != nil {
			t.Fatal(err)
		}
		if csr.VectorDType != VectorDTypeF32 {
			t.Fatalf("dtype = %q", csr.VectorDType)
		}
	})
	t.Run("converts to the configured dtype", func(t *testing.T) {
		cfg := base
		cfg.HNSWConfig.VectorDType = "f16"
		out, err := (&Builder{config: cfg}).AddVectors(ctx, data, smallVectors(2, 4, 3), 8)
		if err != nil {
			t.Fatal(err)
		}
		csr, err := ReadCSR(strings.NewReader(string(out)))
		if err != nil {
			t.Fatal(err)
		}
		if csr.VectorDType != VectorDTypeF16 {
			t.Fatalf("dtype = %q", csr.VectorDType)
		}
	})
	t.Run("an index written without a dtype is treated as float32", func(t *testing.T) {
		csr, err := ReadCSR(strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		csr.VectorDType = ""
		var sb strings.Builder
		if _, err := csr.WriteTo(&sb); err != nil {
			t.Fatal(err)
		}
		out, err := (&Builder{config: base}).AddVectors(ctx, []byte(sb.String()), smallVectors(1, 4, 3), 8)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadCSR(strings.NewReader(string(out)))
		if err != nil {
			t.Fatal(err)
		}
		if got.VectorDType != VectorDTypeF32 {
			t.Fatalf("dtype = %q, want %q", got.VectorDType, VectorDTypeF32)
		}
	})
	t.Run("pruning the grown index", func(t *testing.T) {
		cfg := base
		cfg.HNSWConfig.PruneEmbeddings = true
		cfg.HNSWConfig.PruneKeepFraction = 0
		out, err := (&Builder{config: cfg}).AddVectors(ctx, data, smallVectors(2, 4, 3), 8)
		if err != nil {
			t.Fatal(err)
		}
		// A pruned result can be searched with recomputation but no longer
		// edited.
		if _, err := (&Builder{config: base}).AddVectors(ctx, out, smallVectors(1, 4, 4), 10); err == nil || !strings.Contains(err.Error(), "pruned") {
			t.Fatalf("editing the pruned result: err = %v, want a pruned-embeddings refusal", err)
		}
	})
}

func TestSearcherLoadRejectsCorruptIndex(t *testing.T) {
	s := &Searcher{config: DefaultConfig()}
	if err := s.Load(context.Background(), []byte("garbage"), IndexMeta{}); err == nil || !strings.Contains(err.Error(), "load index") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := s.Search(context.Background(), []float32{1}, 1); err == nil {
		t.Fatal("a searcher whose load failed must still report no index")
	}
}

// A zero EfSearch falls back to 128, and an ef below topK is raised to topK,
// so asking for more results than the default ef still returns them all.
func TestSearchEfDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 20
	data, emb := buildSmall(t, cfg, 30, 4)

	zero := cfg
	zero.HNSWConfig.EfSearch = 0
	s := loadSearcher(t, zero, data)
	ctx := context.Background()
	ids, dists, err := s.Search(ctx, emb[0], 30)
	if err != nil || len(ids) != 30 || len(dists) != 30 {
		t.Fatalf("Search = %d ids, %d distances, %v; want all 30", len(ids), len(dists), err)
	}

	small := cfg
	small.HNSWConfig.EfSearch = 2
	s = loadSearcher(t, small, data)
	if ids, _, err := s.Search(ctx, emb[0], 10); err != nil || len(ids) != 10 {
		t.Fatalf("EfSearch below topK: %d ids, %v; want 10", len(ids), err)
	}

	recompute := func(ctx context.Context, ids []int64) ([][]float32, error) {
		out := make([][]float32, len(ids))
		for i, id := range ids {
			out[i] = emb[id]
		}
		return out, nil
	}
	for _, c := range []Config{zero, small} {
		s = loadSearcher(t, c, data)
		if ids, _, err := s.SearchWithRecompute(ctx, emb[1], 10, recompute); err != nil || len(ids) != 10 {
			t.Fatalf("SearchWithRecompute (ef %d): %d ids, %v; want 10", c.HNSWConfig.EfSearch, len(ids), err)
		}
	}
}

// A recompute callback that fails does not fail the search: the traversal
// proceeds with what the graph holds.
func TestSearchWithRecomputeFailingCallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 20
	cfg.HNSWConfig.PruneEmbeddings = true
	cfg.HNSWConfig.PruneKeepFraction = 0
	data, emb := buildSmall(t, cfg, 12, 4)
	s := loadSearcher(t, cfg, data)
	calls := 0
	_, _, err := s.SearchWithRecompute(context.Background(), emb[0], 3, func(context.Context, []int64) ([][]float32, error) {
		calls++
		return nil, errors.New("embedder down")
	})
	if err != nil {
		t.Fatalf("err = %v, want the search to degrade, not fail", err)
	}
	if calls == 0 {
		t.Fatal("the recompute callback was never asked for vectors")
	}
}

// Every accessor of an unloaded or closed searcher says there is no index.
func TestSearcherAccessorsWithoutIndex(t *testing.T) {
	ctx := context.Background()
	s := &Searcher{config: DefaultConfig()}
	check := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "no index loaded") {
			t.Errorf("%s: err = %v, want \"no index loaded\"", name, err)
		}
	}
	_, _, err := s.SearchWithRecompute(ctx, []float32{1}, 1, nil)
	check("SearchWithRecompute", err)
	_, err = s.VectorDistances(ctx, []float32{1}, []int64{0})
	check("VectorDistances", err)
	_, err = s.ExtractVectors(ctx, []int64{0})
	check("ExtractVectors", err)

	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 20
	data, _ := buildSmall(t, cfg, 6, 4)
	s = loadSearcher(t, cfg, data)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.Search(ctx, []float32{1, 0, 0, 0}, 1)
	check("Search after Close", err)
}

// VectorDistances and ExtractVectors refuse an id the graph does not hold and
// an id whose vector was pruned, and otherwise return what is stored.
func TestVectorAccessorsMissingAndPrunedIDs(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 20

	data, emb := buildSmall(t, cfg, 6, 4)
	s := loadSearcher(t, cfg, data)
	if _, err := s.VectorDistances(ctx, emb[0], []int64{0, 99}); err == nil || !strings.Contains(err.Error(), "not in graph") {
		t.Fatalf("VectorDistances missing id: %v", err)
	}
	if _, err := s.ExtractVectors(ctx, []int64{99}); err == nil || !strings.Contains(err.Error(), "not in graph") {
		t.Fatalf("ExtractVectors missing id: %v", err)
	}
	vecs, err := s.ExtractVectors(ctx, []int64{2})
	if err != nil || len(vecs) != 1 || len(vecs[0]) != 4 {
		t.Fatalf("ExtractVectors = %v, %v", vecs, err)
	}
	vecs[0][0] += 1 // the caller's copy must not alias the graph
	again, _ := s.ExtractVectors(ctx, []int64{2})
	if again[0][0] == vecs[0][0] {
		t.Fatal("ExtractVectors returned a slice aliasing the stored vector")
	}

	pruned := cfg
	pruned.HNSWConfig.PruneEmbeddings = true
	pruned.HNSWConfig.PruneKeepFraction = 0
	pdata, _ := buildSmall(t, pruned, 6, 4)
	p := loadSearcher(t, pruned, pdata)
	if _, err := p.VectorDistances(ctx, emb[0], []int64{1}); err == nil || !strings.Contains(err.Error(), "pruned") {
		t.Fatalf("VectorDistances over a pruned vector: %v", err)
	}
	if _, err := p.ExtractVectors(ctx, []int64{1}); err == nil || !strings.Contains(err.Error(), "pruned") {
		t.Fatalf("ExtractVectors over a pruned vector: %v", err)
	}
}

// Vectors stored as float16 are widened back to float32 by ExtractVectors.
func TestExtractVectorsFromFloat16Index(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 4
	cfg.HNSWConfig.EfConstruction = 20
	cfg.HNSWConfig.VectorDType = VectorDTypeF16
	data, emb := buildSmall(t, cfg, 6, 4)
	s := loadSearcher(t, cfg, data)
	got, err := s.ExtractVectors(context.Background(), []int64{4})
	if err != nil || len(got) != 1 || len(got[0]) != 4 {
		t.Fatalf("ExtractVectors = %v, %v", got, err)
	}
	for i, v := range got[0] {
		if d := v - emb[4][i]; d > 1e-2 || d < -1e-2 {
			t.Fatalf("component %d = %v, want ~%v (float16 precision)", i, v, emb[4][i])
		}
	}
}
