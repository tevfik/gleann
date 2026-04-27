package diskann

import (
	"bytes"
	"context"
	"math"
	"math/rand"
	"testing"
)

func randomVectors(rng *rand.Rand, n, dims int) [][]float32 {
	vecs := make([][]float32, n)
	for i := range vecs {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()
		}
		vecs[i] = v
	}
	return vecs
}

// ──────────────────── Distance Functions ────────────────────

func TestL2Distance(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{0, 1, 0}
	d := L2DistanceSquared(a, b)
	if math.Abs(float64(d)-2.0) > 1e-6 {
		t.Errorf("L2²([1,0,0],[0,1,0]) = %f, want 2.0", d)
	}
}

func TestCosineDistance(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{1, 0}
	d := CosineDistance(a, b)
	if math.Abs(float64(d)) > 1e-6 {
		t.Errorf("cosine([1,0],[1,0]) = %f, want 0", d)
	}

	c := []float32{0, 1}
	d2 := CosineDistance(a, c)
	if math.Abs(float64(d2)-1.0) > 1e-6 {
		t.Errorf("cosine([1,0],[0,1]) = %f, want 1.0", d2)
	}
}

func TestGetDistanceFunc(t *testing.T) {
	df := GetDistanceFunc("l2")
	if df == nil {
		t.Fatal("expected non-nil")
	}
	df2 := GetDistanceFunc("cosine")
	if df2 == nil {
		t.Fatal("expected non-nil")
	}
}

// ──────────────────── PQ ────────────────────

func TestPQTrainEncode(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 500, 32)

	pq, err := TrainPQ(vecs, 8, 256)
	if err != nil {
		t.Fatalf("train PQ: %v", err)
	}

	if pq.M != 8 {
		t.Errorf("M = %d, want 8", pq.M)
	}
	if pq.SubDim != 4 {
		t.Errorf("SubDim = %d, want 4", pq.SubDim)
	}

	// Encode a vector.
	codes := pq.Encode(vecs[0])
	if len(codes) != 8 {
		t.Errorf("code length = %d, want 8", len(codes))
	}
}

func TestPQADCDistance(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 200, 16)

	pq, err := TrainPQ(vecs, 4, 64)
	if err != nil {
		t.Fatalf("train: %v", err)
	}

	query := vecs[0]
	table := pq.BuildDistanceTable(query, L2DistanceSquared)

	// ADC distance to self should be small.
	selfCodes := pq.Encode(query)
	selfDist := ADCDistance(table, selfCodes)

	// ADC distance to a random vector should be larger.
	otherCodes := pq.Encode(vecs[100])
	otherDist := ADCDistance(table, otherCodes)

	t.Logf("ADC self-dist=%f, other-dist=%f", selfDist, otherDist)
	if selfDist > otherDist {
		t.Errorf("self-ADC (%f) should be ≤ other-ADC (%f)", selfDist, otherDist)
	}
}

func TestPQTrainErrors(t *testing.T) {
	if _, err := TrainPQ(nil, 4, 256); err == nil {
		t.Error("expected error for empty vectors")
	}

	vecs := randomVectors(rand.New(rand.NewSource(1)), 10, 16)
	if _, err := TrainPQ(vecs, 3, 256); err == nil {
		t.Error("expected error: dims not divisible by m")
	}
}

// ──────────────────── Vamana Graph ────────────────────

func TestVamanaBuild(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 200, 32)

	g := NewVamanaGraph(32, 50, 32, 1.2, nil)
	g.Build(vecs)

	if g.Size() != 200 {
		t.Errorf("size = %d, want 200", g.Size())
	}

	medoid := g.Medoid()
	if medoid < 0 || medoid >= 200 {
		t.Errorf("medoid = %d, out of range", medoid)
	}

	// All nodes should have neighbors.
	for i := 0; i < 200; i++ {
		nb := g.GetNeighbors(int64(i))
		if len(nb) == 0 {
			t.Errorf("node %d has no neighbors", i)
		}
		if len(nb) > 32 {
			t.Errorf("node %d has %d neighbors (max R=32)", i, len(nb))
		}
	}
}

func TestVamanaSearch(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 500, 32)

	g := NewVamanaGraph(32, 50, 32, 1.2, nil)
	g.Build(vecs)

	// Search for an exact vector.
	results := g.Search(vecs[0], 5, 50)
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if results[0].ID != 0 {
		t.Logf("expected ID 0 as top result, got %d (dist=%f)", results[0].ID, results[0].Distance)
	}
	if results[0].Distance > 1e-4 {
		t.Errorf("self-search distance %f, expected near-zero", results[0].Distance)
	}
}

func TestVamanaSearchRecall(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n := 1000
	dims := 32
	vecs := randomVectors(rng, n, dims)

	g := NewVamanaGraph(64, 100, dims, 1.2, nil)
	g.Build(vecs)

	// Measure recall@10 by searching for 50 known vectors.
	hits := 0
	queries := 50
	for q := 0; q < queries; q++ {
		results := g.Search(vecs[q], 10, 100)
		for _, r := range results {
			if r.ID == int64(q) {
				hits++
				break
			}
		}
	}

	recall := float64(hits) / float64(queries)
	t.Logf("Vamana recall@10 (self-search): %.1f%% (%d/%d)", recall*100, hits, queries)
	if recall < 0.90 {
		t.Errorf("recall %.2f too low (expected ≥0.90)", recall)
	}
}

func TestVamanaEmpty(t *testing.T) {
	g := NewVamanaGraph(32, 50, 16, 1.2, nil)
	g.Build(nil)
	if g.Size() != 0 {
		t.Errorf("expected empty graph")
	}
}

// ──────────────────── DiskIndex Build + Serialize ────────────────────

func TestDiskIndexBuild(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 300, 32)

	cfg := DiskANNConfig{R: 32, L: 50, Alpha: 1.2}
	idx, err := BuildDiskIndex(vecs, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if idx.NumNodes != 300 {
		t.Errorf("numNodes = %d, want 300", idx.NumNodes)
	}
	if idx.Dims != 32 {
		t.Errorf("dims = %d, want 32", idx.Dims)
	}
	if idx.Codebook == nil {
		t.Fatal("codebook is nil")
	}
	if len(idx.PQCodes) != 300 {
		t.Errorf("PQ codes count = %d, want 300", len(idx.PQCodes))
	}

	ramUsage := idx.RAMUsage()
	diskUsage := idx.DiskUsage()
	t.Logf("300 vectors × 32d: RAM=%dKB, Disk=%dKB, ratio=%.1fx savings",
		ramUsage/1024, diskUsage/1024, float64(diskUsage)/float64(ramUsage))

	if ramUsage >= diskUsage {
		t.Errorf("RAM (%d) should be less than disk (%d)", ramUsage, diskUsage)
	}
}

func TestDiskIndexSerializeRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 100, 16)

	cfg := DiskANNConfig{R: 16, L: 30, Alpha: 1.2}
	idx, err := BuildDiskIndex(vecs, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Serialize.
	var buf bytes.Buffer
	written, err := idx.WriteTo(&buf)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if written == 0 {
		t.Fatal("wrote 0 bytes")
	}

	// Deserialize.
	idx2, err := ReadDiskIndex(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Verify header.
	if idx2.NumNodes != idx.NumNodes {
		t.Errorf("numNodes: %d vs %d", idx2.NumNodes, idx.NumNodes)
	}
	if idx2.Dims != idx.Dims {
		t.Errorf("dims: %d vs %d", idx2.Dims, idx.Dims)
	}
	if idx2.Medoid != idx.Medoid {
		t.Errorf("medoid: %d vs %d", idx2.Medoid, idx.Medoid)
	}
	if idx2.PQM != idx.PQM {
		t.Errorf("pqM: %d vs %d", idx2.PQM, idx.PQM)
	}

	// Verify neighbors roundtrip.
	for i := int64(0); i < idx.NumNodes; i++ {
		nb1 := idx.GetNeighbors(i)
		nb2 := idx2.GetNeighbors(i)
		if len(nb1) != len(nb2) {
			t.Errorf("node %d: neighbor count %d vs %d", i, len(nb1), len(nb2))
			continue
		}
		for j := range nb1 {
			if nb1[j] != nb2[j] {
				t.Errorf("node %d neighbor %d: %d vs %d", i, j, nb1[j], nb2[j])
			}
		}
	}

	// Verify embeddings roundtrip.
	for i := int64(0); i < idx.NumNodes; i++ {
		for d := 0; d < idx.Dims; d++ {
			if idx.Embeddings[i][d] != idx2.Embeddings[i][d] {
				t.Errorf("embedding[%d][%d]: %f vs %f", i, d, idx.Embeddings[i][d], idx2.Embeddings[i][d])
			}
		}
	}
}

func TestDiskIndexBadMagic(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00} // bad magic
	_, err := ReadDiskIndex(bytes.NewReader(data))
	if err == nil {
		t.Error("expected error for bad magic")
	}
}

// ──────────────────── Search ────────────────────

func TestSearchExactSelfRecall(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n := 500
	dims := 32
	vecs := randomVectors(rng, n, dims)

	cfg := DiskANNConfig{R: 64, L: 100, Alpha: 1.2}
	idx, err := BuildDiskIndex(vecs, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// Self-recall: search for known vectors.
	hits := 0
	queries := 50
	for q := 0; q < queries; q++ {
		results := SearchExact(idx, vecs[q], 10, 100, L2DistanceSquared)
		for _, r := range results {
			if r.ID == int64(q) {
				hits++
				break
			}
		}
	}

	recall := float64(hits) / float64(queries)
	t.Logf("SearchExact recall@10: %.1f%% (%d/%d)", recall*100, hits, queries)
	if recall < 0.80 {
		t.Errorf("recall %.2f too low (expected ≥0.80)", recall)
	}
}

func TestSearchPQPrefilter(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n := 500
	dims := 32
	vecs := randomVectors(rng, n, dims)

	cfg := DiskANNConfig{R: 64, L: 100, Alpha: 1.2}
	idx, err := BuildDiskIndex(vecs, cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	// PQ-prefiltered search with reranking callback.
	query := vecs[0]
	getVector := func(id int64) []float32 {
		if id >= 0 && id < int64(n) {
			return vecs[id]
		}
		return nil
	}

	results := SearchDiskIndex(idx, query, 5, 100, 20, L2DistanceSquared, getVector)
	if len(results) == 0 {
		t.Fatal("no results")
	}

	// Top result should be the query itself.
	if results[0].Distance > 1e-4 {
		t.Errorf("self-search distance %f, expected near-zero", results[0].Distance)
	}
}

// ──────────────────── Backend API ────────────────────

func TestBackendBuildSearch(t *testing.T) {
	config := DefaultConfig()
	config.DiskANNConfig.R = 32
	config.DiskANNConfig.L = 50

	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 200, 32)

	ctx := context.Background()
	data, err := builder.Build(ctx, vecs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty index data")
	}

	searcher := &Searcher{config: config}
	meta := IndexMeta{Dimensions: 32, Backend: "diskann"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	ids, dists, err := searcher.Search(ctx, vecs[0], 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("no results")
	}
	t.Logf("top result: id=%d, dist=%f", ids[0], dists[0])
}

func TestBackendSearchWithRecompute(t *testing.T) {
	config := DefaultConfig()
	config.DiskANNConfig.R = 32
	config.DiskANNConfig.L = 50

	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 200, 32)

	ctx := context.Background()
	data, err := builder.Build(ctx, vecs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	searcher := &Searcher{config: config}
	meta := IndexMeta{Dimensions: 32, Backend: "diskann"}
	if err := searcher.Load(ctx, data, meta); err != nil {
		t.Fatalf("load: %v", err)
	}
	defer searcher.Close()

	recompute := func(ctx context.Context, ids []int64) ([][]float32, error) {
		result := make([][]float32, len(ids))
		for i, id := range ids {
			if id >= 0 && id < int64(len(vecs)) {
				result[i] = vecs[id]
			}
		}
		return result, nil
	}

	ids, dists, err := searcher.SearchWithRecompute(ctx, vecs[0], 5, recompute)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("no results")
	}
	if dists[0] > 1e-4 {
		t.Errorf("self-search distance %f, expected near-zero", dists[0])
	}
}

func TestBackendNotLoaded(t *testing.T) {
	s := &Searcher{config: DefaultConfig()}
	_, _, err := s.Search(context.Background(), []float32{1, 0}, 5)
	if err == nil {
		t.Error("expected error when not loaded")
	}
}

func TestBackendAddVectors(t *testing.T) {
	config := DefaultConfig()
	config.DiskANNConfig.R = 16
	config.DiskANNConfig.L = 30

	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	initial := randomVectors(rng, 100, 16)
	additional := randomVectors(rng, 50, 16)

	ctx := context.Background()
	data, err := builder.Build(ctx, initial)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	data2, err := builder.AddVectors(ctx, data, additional, 100)
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Verify the new index has more data.
	idx, err := ReadDiskIndex(bytes.NewReader(data2))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if idx.NumNodes != 150 {
		t.Errorf("expected 150 nodes, got %d", idx.NumNodes)
	}
}

func TestBackendRemoveVectors(t *testing.T) {
	config := DefaultConfig()
	config.DiskANNConfig.R = 16
	config.DiskANNConfig.L = 30

	builder := &Builder{config: config}

	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 100, 16)

	ctx := context.Background()
	data, err := builder.Build(ctx, vecs)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	data2, err := builder.RemoveVectors(ctx, data, []int64{0, 1, 2})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}

	idx, err := ReadDiskIndex(bytes.NewReader(data2))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if idx.NumNodes != 97 {
		t.Errorf("expected 97 nodes, got %d", idx.NumNodes)
	}
}

func TestFactoryName(t *testing.T) {
	f := &Factory{}
	if f.Name() != "diskann" {
		t.Errorf("expected 'diskann', got %q", f.Name())
	}
}

// ──────────────────── Benchmark ────────────────────

func BenchmarkVamanaBuild(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 1000, 64)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g := NewVamanaGraph(32, 50, 64, 1.2, nil)
		g.Build(vecs)
	}
}

func BenchmarkPQTrain(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 1000, 64)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		TrainPQ(vecs, 16, 256)
	}
}

func BenchmarkSearchExact(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 5000, 64)

	cfg := DiskANNConfig{R: 64, L: 100, Alpha: 1.2}
	idx, _ := BuildDiskIndex(vecs, cfg)

	query := make([]float32, 64)
	for j := range query {
		query[j] = rng.Float32()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SearchExact(idx, query, 10, 100, L2DistanceSquared)
	}
}

func BenchmarkSearchPQPrefilter(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	vecs := randomVectors(rng, 5000, 64)

	cfg := DiskANNConfig{R: 64, L: 100, Alpha: 1.2}
	idx, _ := BuildDiskIndex(vecs, cfg)

	query := make([]float32, 64)
	for j := range query {
		query[j] = rng.Float32()
	}

	getVector := func(id int64) []float32 {
		if id >= 0 && id < int64(len(vecs)) {
			return vecs[id]
		}
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SearchDiskIndex(idx, query, 10, 100, 20, L2DistanceSquared, getVector)
	}
}
