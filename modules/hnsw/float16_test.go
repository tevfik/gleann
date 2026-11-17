package hnsw

import (
	"bytes"
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestFloat16RoundTrip_EdgeValues tests IEEE 754 binary16 conversion
// of edge values: max, min normal, subnormal, inf, NaN, -0, and all 65536 patterns.
func TestFloat16RoundTrip_EdgeValues(t *testing.T) {
	// 1. -0.0
	negZero := float32(math.Copysign(0, -1))
	hNegZero := Float32ToFloat16(negZero)
	if hNegZero != 0x8000 {
		t.Errorf("Float32ToFloat16(-0.0): got 0x%04X, want 0x8000", hNegZero)
	}
	fNegZero := Float16ToFloat32(hNegZero)
	if fNegZero != 0.0 || !math.Signbit(float64(fNegZero)) {
		t.Errorf("Float16ToFloat32(0x8000): got %v (signbit=%v), want -0.0", fNegZero, math.Signbit(float64(fNegZero)))
	}

	// 2. +0.0
	hPosZero := Float32ToFloat16(0.0)
	if hPosZero != 0x0000 {
		t.Errorf("Float32ToFloat16(+0.0): got 0x%04X, want 0x0000", hPosZero)
	}
	fPosZero := Float16ToFloat32(hPosZero)
	if fPosZero != 0.0 || math.Signbit(float64(fPosZero)) {
		t.Errorf("Float16ToFloat32(0x0000): got %v (signbit=%v), want +0.0", fPosZero, math.Signbit(float64(fPosZero)))
	}

	// 3. +Inf
	posInf := float32(math.Inf(1))
	hPosInf := Float32ToFloat16(posInf)
	if hPosInf != 0x7C00 {
		t.Errorf("Float32ToFloat16(+Inf): got 0x%04X, want 0x7C00", hPosInf)
	}
	fPosInf := Float16ToFloat32(hPosInf)
	if !math.IsInf(float64(fPosInf), 1) {
		t.Errorf("Float16ToFloat32(0x7C00): got %v, want +Inf", fPosInf)
	}

	// 4. -Inf
	negInf := float32(math.Inf(-1))
	hNegInf := Float32ToFloat16(negInf)
	if hNegInf != 0xFC00 {
		t.Errorf("Float32ToFloat16(-Inf): got 0x%04X, want 0xFC00", hNegInf)
	}
	fNegInf := Float16ToFloat32(hNegInf)
	if !math.IsInf(float64(fNegInf), -1) {
		t.Errorf("Float16ToFloat32(0xFC00): got %v, want -Inf", fNegInf)
	}

	// 5. NaN
	nan := float32(math.NaN())
	hNaN := Float32ToFloat16(nan)
	if (hNaN>>10)&0x1F != 0x1F || (hNaN&0x03FF) == 0 {
		t.Errorf("Float32ToFloat16(NaN): got 0x%04X, want exp=31 and frac!=0", hNaN)
	}
	fNaN := Float16ToFloat32(hNaN)
	if !math.IsNaN(float64(fNaN)) {
		t.Errorf("Float16ToFloat32(NaN): got %v, want NaN", fNaN)
	}

	// 6. Max normal: 65504.0
	maxNormal := float32(65504.0)
	hMaxNorm := Float32ToFloat16(maxNormal)
	if hMaxNorm != 0x7BFF {
		t.Errorf("Float32ToFloat16(65504.0): got 0x%04X, want 0x7BFF", hMaxNorm)
	}
	fMaxNorm := Float16ToFloat32(hMaxNorm)
	if fMaxNorm != 65504.0 {
		t.Errorf("Float16ToFloat32(0x7BFF): got %v, want 65504.0", fMaxNorm)
	}

	// 7. Overflow cutoff (round-to-nearest-even)
	h65519 := Float32ToFloat16(65519.0)
	if h65519 != 0x7BFF {
		t.Errorf("Float32ToFloat16(65519.0): got 0x%04X, want 0x7BFF (65504)", h65519)
	}
	h65520 := Float32ToFloat16(65520.0)
	if h65520 != 0x7C00 {
		t.Errorf("Float32ToFloat16(65520.0): got 0x%04X, want 0x7C00 (+Inf)", h65520)
	}

	// 8. Min normal: 2^(-14) = 6.103515625e-5
	minNormal := float32(math.Pow(2, -14))
	hMinNorm := Float32ToFloat16(minNormal)
	if hMinNorm != 0x0400 {
		t.Errorf("Float32ToFloat16(min normal): got 0x%04X, want 0x0400", hMinNorm)
	}
	fMinNorm := Float16ToFloat32(hMinNorm)
	if fMinNorm != minNormal {
		t.Errorf("Float16ToFloat32(0x0400): got %v, want %v", fMinNorm, minNormal)
	}

	// 9. Min subnormal: 2^(-24) ~ 5.9604645e-8
	minSub := float32(math.Pow(2, -24))
	hMinSub := Float32ToFloat16(minSub)
	if hMinSub != 0x0001 {
		t.Errorf("Float32ToFloat16(min subnormal): got 0x%04X, want 0x0001", hMinSub)
	}
	fMinSub := Float16ToFloat32(hMinSub)
	if fMinSub != minSub {
		t.Errorf("Float16ToFloat32(0x0001): got %v, want %v", fMinSub, minSub)
	}

	// 10. Exhaustive test: all 65536 half-float bit patterns must round-trip exactly
	for i := 0; i < 65536; i++ {
		h := uint16(i)
		exp := (h >> 10) & 0x1F
		frac := h & 0x03FF
		isNan := (exp == 0x1F && frac != 0)

		f := Float16ToFloat32(h)
		hBack := Float32ToFloat16(f)

		if isNan {
			expBack := (hBack >> 10) & 0x1F
			fracBack := hBack & 0x03FF
			if expBack != 0x1F || fracBack == 0 {
				t.Fatalf("NaN roundtrip failed for 0x%04X: got 0x%04X", h, hBack)
			}
		} else {
			if h != hBack {
				t.Fatalf("Bit pattern 0x%04X roundtrip mismatch: got 0x%04X (f32=%v)", h, hBack, f)
			}
		}
	}
}

// TestF32IndexWrittenByCurrentCodeStillLoads ensures backwards compatibility:
// an index serialized in CSRVersion1 (f32 only) still loads and searches properly.
func TestF32IndexWrittenByCurrentCodeStillLoads(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "legacy.csr")

	dims := 8
	numVecs := 20
	rng := rand.New(rand.NewSource(123))
	vecs := make([][]float32, numVecs)
	for i := 0; i < numVecs; i++ {
		vecs[i] = make([]float32, dims)
		for j := 0; j < dims; j++ {
			vecs[i][j] = rng.Float32()
		}
	}

	// Build a standard CSR graph
	g := NewGraph(16, 50, dims)
	for i, v := range vecs {
		g.Insert(int64(i), v)
	}
	csr := ConvertToCSR(g)
	PruneEmbeddings(g, csr, 1.0)
	csr.VectorDType = "f32" // explicitly f32

	var buf bytes.Buffer
	n, err := csr.WriteTo(&buf)
	if err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	if err := os.WriteFile(indexPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Logf("Wrote legacy v1 index: %d bytes", n)

	// Verify header version is 1
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	version := uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24
	if version != CSRVersion1 {
		t.Fatalf("expected version 1 in header, got %d", version)
	}

	// 1. Load via ReadCSR (RAM path)
	readCSR, err := ReadCSR(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("ReadCSR failed on v1 index: %v", err)
	}
	if readCSR.VectorDType != "f32" {
		t.Errorf("expected VectorDType=f32, got %q", readCSR.VectorDType)
	}
	if len(readCSR.StoredEmbeddings) != numVecs {
		t.Errorf("expected %d stored embeddings, got %d", numVecs, len(readCSR.StoredEmbeddings))
	}

	// Reconstruct graph & search
	searcher := &Searcher{config: DefaultConfig()}
	if err := searcher.Load(context.Background(), data, IndexMeta{Dimensions: dims}); err != nil {
		t.Fatalf("Searcher.Load: %v", err)
	}
	defer searcher.Close()

	query := vecs[0]
	ids, dists, err := searcher.Search(context.Background(), query, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(ids) == 0 || ids[0] != 0 {
		t.Errorf("expected top result to be id 0, got %v", ids)
	}
	if dists[0] > 1e-5 {
		t.Errorf("expected top distance ~0, got %f", dists[0])
	}

	// Test VectorDistances on RAM searcher
	scoredDists, err := searcher.VectorDistances(context.Background(), query, []int64{0, 1})
	if err != nil {
		t.Fatalf("VectorDistances: %v", err)
	}
	if len(scoredDists) != 2 || scoredDists[0] > 1e-5 {
		t.Errorf("VectorDistances: got %v", scoredDists)
	}

	// 2. Load via MmapSearcher
	mmapSearcher := &MmapSearcher{config: DefaultConfig()}
	if err := mmapSearcher.LoadFromFile(context.Background(), indexPath); err != nil {
		t.Fatalf("MmapSearcher.LoadFromFile: %v", err)
	}
	defer mmapSearcher.Close()

	mmapIDs, mmapDists, err := mmapSearcher.Search(context.Background(), query, 5)
	if err != nil {
		t.Fatalf("MmapSearcher.Search: %v", err)
	}
	if len(mmapIDs) == 0 || mmapIDs[0] != 0 {
		t.Errorf("mmap expected top result id 0, got %v", mmapIDs)
	}
	if mmapDists[0] > 1e-5 {
		t.Errorf("mmap expected top distance ~0, got %f", mmapDists[0])
	}

	// Test VectorDistances on MmapSearcher
	mmapScored, err := mmapSearcher.VectorDistances(context.Background(), query, []int64{0, 1})
	if err != nil {
		t.Fatalf("MmapSearcher.VectorDistances: %v", err)
	}
	if len(mmapScored) != 2 || mmapScored[0] > 1e-5 {
		t.Errorf("MmapSearcher.VectorDistances: got %v", mmapScored)
	}
}

// TestMmapAndRAMSearchersIdenticalResultsF16 verifies that mmap and RAM searchers
// produce identical search results and vector distances on the same f16 index file.
func TestMmapAndRAMSearchersIdenticalResultsF16(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test_f16.csr")

	dims := 32
	numVecs := 100
	rng := rand.New(rand.NewSource(99))
	embeddings := make([][]float32, numVecs)
	for i := 0; i < numVecs; i++ {
		embeddings[i] = make([]float32, dims)
		for j := 0; j < dims; j++ {
			embeddings[i][j] = rng.Float32()
		}
	}

	cfg := DefaultConfig()
	cfg.HNSWConfig.VectorDType = "f16"
	cfg.HNSWConfig.PruneEmbeddings = false // store all embeddings in f16

	builder := &Builder{config: cfg}
	indexBytes, err := builder.Build(context.Background(), embeddings)
	if err != nil {
		t.Fatalf("Build f16: %v", err)
	}
	if err := os.WriteFile(indexPath, indexBytes, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Verify header is version 2
	version := uint32(indexBytes[4]) | uint32(indexBytes[5])<<8 | uint32(indexBytes[6])<<16 | uint32(indexBytes[7])<<24
	if version != CSRVersion2 {
		t.Fatalf("expected version 2 in header, got %d", version)
	}

	// 1. RAM Searcher
	ramSearcher := &Searcher{config: cfg}
	if err := ramSearcher.Load(context.Background(), indexBytes, IndexMeta{Dimensions: dims}); err != nil {
		t.Fatalf("ramSearcher.Load: %v", err)
	}
	defer ramSearcher.Close()

	// 2. Mmap Searcher
	mmapSearcher := &MmapSearcher{config: cfg}
	if err := mmapSearcher.LoadFromFile(context.Background(), indexPath); err != nil {
		t.Fatalf("mmapSearcher.LoadFromFile: %v", err)
	}
	defer mmapSearcher.Close()

	// Test 10 queries
	for q := 0; q < 10; q++ {
		query := make([]float32, dims)
		for j := 0; j < dims; j++ {
			query[j] = rng.Float32()
		}

		ramIDs, ramDists, err := ramSearcher.Search(context.Background(), query, 10)
		if err != nil {
			t.Fatalf("query %d RAM Search: %v", q, err)
		}

		mmapIDs, mmapDists, err := mmapSearcher.Search(context.Background(), query, 10)
		if err != nil {
			t.Fatalf("query %d Mmap Search: %v", q, err)
		}

		if len(ramIDs) != len(mmapIDs) {
			t.Fatalf("query %d: len mismatch RAM=%d vs Mmap=%d", q, len(ramIDs), len(mmapIDs))
		}

		for i := range ramIDs {
			if ramIDs[i] != mmapIDs[i] {
				t.Errorf("query %d result %d ID mismatch: RAM=%d vs Mmap=%d", q, i, ramIDs[i], mmapIDs[i])
			}
			diff := math.Abs(float64(ramDists[i] - mmapDists[i]))
			if diff > 1e-5 {
				t.Errorf("query %d result %d dist mismatch: RAM=%f vs Mmap=%f (diff=%e)", q, i, ramDists[i], mmapDists[i], diff)
			}
		}

		// VectorDistances check on same IDs
		subIDs := ramIDs[:min(5, len(ramIDs))]
		ramVD, err := ramSearcher.VectorDistances(context.Background(), query, subIDs)
		if err != nil {
			t.Fatalf("query %d RAM VectorDistances: %v", q, err)
		}
		mmapVD, err := mmapSearcher.VectorDistances(context.Background(), query, subIDs)
		if err != nil {
			t.Fatalf("query %d Mmap VectorDistances: %v", q, err)
		}
		for i := range subIDs {
			diff := math.Abs(float64(ramVD[i] - mmapVD[i]))
			if diff > 1e-5 {
				t.Errorf("query %d ID %d VectorDistances mismatch: RAM=%f vs Mmap=%f", q, subIDs[i], ramVD[i], mmapVD[i])
			}
		}
	}
}

// TestIndexF16Recall_2K tests that an index built with float16 storage searches
// with recall@10 >= 0.99 compared to the float32 index on a 2000-vector set.
func TestIndexF16Recall_2K(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2K recall test in short mode")
	}

	numVecs := 2000
	dims := 64
	rng := rand.New(rand.NewSource(42))

	embeddings := make([][]float32, numVecs)
	for i := 0; i < numVecs; i++ {
		embeddings[i] = make([]float32, dims)
		var norm float32
		for j := 0; j < dims; j++ {
			embeddings[i][j] = rng.Float32() - 0.5
			norm += embeddings[i][j] * embeddings[i][j]
		}
		norm = float32(math.Sqrt(float64(norm)))
		for j := 0; j < dims; j++ {
			embeddings[i][j] /= norm
		}
	}

	// 1. Build f32 index
	cfgF32 := DefaultConfig()
	cfgF32.HNSWConfig.VectorDType = "f32"
	cfgF32.HNSWConfig.M = 32
	cfgF32.HNSWConfig.EfConstruction = 200
	cfgF32.HNSWConfig.EfSearch = 128
	cfgF32.HNSWConfig.PruneEmbeddings = false

	builderF32 := &Builder{config: cfgF32}
	dataF32, err := builderF32.Build(context.Background(), embeddings)
	if err != nil {
		t.Fatalf("Build f32: %v", err)
	}

	// 2. Build f16 index
	cfgF16 := DefaultConfig()
	cfgF16.HNSWConfig.VectorDType = "f16"
	cfgF16.HNSWConfig.M = 32
	cfgF16.HNSWConfig.EfConstruction = 200
	cfgF16.HNSWConfig.EfSearch = 128
	cfgF16.HNSWConfig.PruneEmbeddings = false

	builderF16 := &Builder{config: cfgF16}
	dataF16, err := builderF16.Build(context.Background(), embeddings)
	if err != nil {
		t.Fatalf("Build f16: %v", err)
	}

	t.Logf("Index sizes: f32 = %d bytes (%.1f KB), f16 = %d bytes (%.1f KB), ratio = %.2fx",
		len(dataF32), float64(len(dataF32))/1024,
		len(dataF16), float64(len(dataF16))/1024,
		float64(len(dataF32))/float64(len(dataF16)))

	// File size ratio should be ~1.8x - 2.0x for the stored vectors portion
	if len(dataF16) >= len(dataF32) {
		t.Errorf("f16 index (%d) should be significantly smaller than f32 index (%d)", len(dataF16), len(dataF32))
	}

	searcherF32 := &Searcher{config: cfgF32}
	if err := searcherF32.Load(context.Background(), dataF32, IndexMeta{Dimensions: dims}); err != nil {
		t.Fatalf("Load f32: %v", err)
	}
	defer searcherF32.Close()

	searcherF16 := &Searcher{config: cfgF16}
	if err := searcherF16.Load(context.Background(), dataF16, IndexMeta{Dimensions: dims}); err != nil {
		t.Fatalf("Load f16: %v", err)
	}
	defer searcherF16.Close()

	// 3. Measure recall@10 on 100 queries
	numQueries := 100
	topK := 10
	totalHits := 0

	for q := 0; q < numQueries; q++ {
		query := make([]float32, dims)
		var norm float32
		for j := 0; j < dims; j++ {
			query[j] = rng.Float32() - 0.5
			norm += query[j] * query[j]
		}
		norm = float32(math.Sqrt(float64(norm)))
		for j := 0; j < dims; j++ {
			query[j] /= norm
		}

		idsF32, _, err := searcherF32.Search(context.Background(), query, topK)
		if err != nil {
			t.Fatalf("query %d f32 search: %v", q, err)
		}

		idsF16, _, err := searcherF16.Search(context.Background(), query, topK)
		if err != nil {
			t.Fatalf("query %d f16 search: %v", q, err)
		}

		setF32 := make(map[int64]bool, len(idsF32))
		for _, id := range idsF32 {
			setF32[id] = true
		}

		hits := 0
		for _, id := range idsF16 {
			if setF32[id] {
				hits++
			}
		}
		totalHits += hits
	}

	recall := float64(totalHits) / float64(numQueries*topK)
	t.Logf("f16 vs f32 recall@10 on 2K vectors: %.4f (%d/%d)", recall, totalHits, numQueries*topK)

	if recall < 0.99 {
		t.Errorf("recall@10 = %.4f, want >= 0.99", recall)
	}
}
