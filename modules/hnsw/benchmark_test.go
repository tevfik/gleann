package hnsw

import (
	"bufio"
	"context"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func getProcessRSS() uint64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseUint(fields[1], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

func getHeapAlloc() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func computeRecallAtK(results []int64, truth []int64, k int) float64 {
	truthSet := make(map[int64]bool, k)
	for i := 0; i < k && i < len(truth); i++ {
		truthSet[truth[i]] = true
	}
	hits := 0
	for i := 0; i < k && i < len(results); i++ {
		if truthSet[results[i]] {
			hits++
		}
	}
	return float64(hits) / float64(k)
}

func TestBenchmark100K_768(t *testing.T) {
	if os.Getenv("GLEANN_BENCH_HNSW_SYNTH") == "" {
		t.Skip("skipping 100K benchmark; enable with GLEANN_BENCH_HNSW_SYNTH=1")
	}

	const (
		numVectors  = 100000
		dims        = 768
		numClusters = 50
		perCluster  = 1000
		numQueries  = 100
		topK        = 10
	)

	t.Logf("=== 100K x 768 Synthetic Benchmark ===")
	t.Logf("Generating %d vectors (dims=%d): %d random unit + %d clustered (%d clusters x %d)...",
		numVectors, dims, numVectors-numClusters*perCluster, numClusters*perCluster, numClusters, perCluster)

	rng := rand.New(rand.NewSource(12345))

	normalize := func(v []float32) {
		var sum float64
		for _, x := range v {
			sum += float64(x * x)
		}
		norm := float32(math.Sqrt(sum))
		if norm > 0 {
			for j := range v {
				v[j] /= norm
			}
		}
	}

	vectors := make([][]float32, numVectors)
	// 50K random unit vectors
	for i := 0; i < 50000; i++ {
		v := make([]float32, dims)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		normalize(v)
		vectors[i] = v
	}

	// 50K clustered vectors around 50 cluster centers
	clusterCenters := make([][]float32, numClusters)
	for c := 0; c < numClusters; c++ {
		center := make([]float32, dims)
		for j := range center {
			center[j] = rng.Float32()*2 - 1
		}
		normalize(center)
		clusterCenters[c] = center
		for k := 0; k < perCluster; k++ {
			idx := 50000 + c*perCluster + k
			v := make([]float32, dims)
			for j := range v {
				v[j] = center[j] + (rng.Float32()*2-1)*0.1
			}
			normalize(v)
			vectors[idx] = v
		}
	}

	// Generate 100 test queries: 50 from clusters with small noise, 50 random unit vectors
	queries := make([][]float32, numQueries)
	for q := 0; q < 50; q++ {
		cIdx := q % numClusters
		qv := make([]float32, dims)
		for j := range qv {
			qv[j] = clusterCenters[cIdx][j] + (rng.Float32()*2-1)*0.05
		}
		normalize(qv)
		queries[q] = qv
	}
	for q := 50; q < numQueries; q++ {
		qv := make([]float32, dims)
		for j := range qv {
			qv[j] = rng.Float32()*2 - 1
		}
		normalize(qv)
		queries[q] = qv
	}

	// Brute force exact float32 search for ground truth
	t.Logf("Computing exact float32 brute force ground truth for %d queries...", numQueries)
	t0 := time.Now()
	type scorePair struct {
		id   int64
		dist float32
	}
	groundTruth := make([][]int64, numQueries)
	for q := 0; q < numQueries; q++ {
		query := queries[q]
		scores := make([]scorePair, numVectors)
		for i := 0; i < numVectors; i++ {
			// Cosine distance on normalized vectors: 1.0 - dot
			scores[i] = scorePair{
				id:   int64(i),
				dist: CosineDistance(query, vectors[i]),
			}
		}
		sort.Slice(scores, func(i, j int) bool {
			return scores[i].dist < scores[j].dist
		})
		gt := make([]int64, topK)
		for k := 0; k < topK; k++ {
			gt[k] = scores[k].id
		}
		groundTruth[q] = gt
	}
	t.Logf("Brute force ground truth computed in %v", time.Since(t0))

	// Build HNSW Graph (M=16, EfConstruction=64)
	t.Logf("Building HNSW graph (M=16, EfConstruction=64, N=%d)...", numVectors)
	tBuildStart := time.Now()
	graph := NewGraphWithOptions(16, 64, dims, CosineDistance, false)
	for i := 0; i < numVectors; i++ {
		graph.Insert(int64(i), vectors[i])
		if (i+1)%25000 == 0 {
			t.Logf("  Inserted %d / %d vectors (%v elapsed)...", i+1, numVectors, time.Since(tBuildStart))
		}
	}
	t.Logf("HNSW graph built in %v", time.Since(tBuildStart))

	tmpDir := t.TempDir()
	pathF32 := filepath.Join(tmpDir, "index_f32.bin")
	pathF16 := filepath.Join(tmpDir, "index_f16.bin")

	// Convert and serialize f32
	t.Logf("Serializing f32 CSR index to %s...", pathF32)
	csrF32 := ConvertToCSR(graph)
	csrF32.VectorDType = VectorDTypeF32
	PruneEmbeddings(graph, csrF32, 1.0)
	f32File, err := os.Create(pathF32)
	if err != nil {
		t.Fatalf("create f32 file: %v", err)
	}
	if _, err := csrF32.WriteTo(f32File); err != nil {
		t.Fatalf("write f32: %v", err)
	}
	f32File.Close()

	// Convert and serialize f16
	t.Logf("Serializing f16 CSR index to %s...", pathF16)
	csrF16 := ConvertToCSR(graph)
	csrF16.VectorDType = VectorDTypeF16
	PruneEmbeddings(graph, csrF16, 1.0)
	f16File, err := os.Create(pathF16)
	if err != nil {
		t.Fatalf("create f16 file: %v", err)
	}
	if _, err := csrF16.WriteTo(f16File); err != nil {
		t.Fatalf("write f16: %v", err)
	}
	f16File.Close()

	fi32, _ := os.Stat(pathF32)
	fi16, _ := os.Stat(pathF16)
	sizeF32Bytes := fi32.Size()
	sizeF16Bytes := fi16.Size()
	sizeF32MB := float64(sizeF32Bytes) / (1024 * 1024)
	sizeF16MB := float64(sizeF16Bytes) / (1024 * 1024)

	t.Logf("--- Index File Sizes ---")
	t.Logf("f32 index size: %d bytes (%.2f MB)", sizeF32Bytes, sizeF32MB)
	t.Logf("f16 index size: %d bytes (%.2f MB)", sizeF16Bytes, sizeF16MB)
	t.Logf("Size reduction: %.2f%% (%.2fx smaller)", (1.0-float64(sizeF16Bytes)/float64(sizeF32Bytes))*100, float64(sizeF32Bytes)/float64(sizeF16Bytes))

	// Release graph memory before search benchmarks to avoid memory pressure
	graph = nil
	csrF32 = nil
	csrF16 = nil
	vectors = nil
	runtime.GC()
	debug.FreeOSMemory()

	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.HNSWConfig.M = 16
	cfg.HNSWConfig.EfConstruction = 64
	cfg.HNSWConfig.EfSearch = 64
	cfg.HNSWConfig.DistanceMetric = DistanceCosine

	metaF32 := IndexMeta{Dimensions: dims, Backend: "hnsw", VectorDType: VectorDTypeF32}
	metaF16 := IndexMeta{Dimensions: dims, Backend: "hnsw", VectorDType: VectorDTypeF16}

	// 1. RAM Searcher: f32
	t.Logf("Measuring RAM Searcher (f32)...")
	runtime.GC()
	debug.FreeOSMemory()
	rssBeforeF32 := getProcessRSS()
	allocBeforeF32 := getHeapAlloc()

	dataF32, err := os.ReadFile(pathF32)
	if err != nil {
		t.Fatalf("read f32: %v", err)
	}
	ramSearcherF32 := &Searcher{config: cfg}
	if err := ramSearcherF32.Load(ctx, dataF32, metaF32); err != nil {
		t.Fatalf("load ram f32: %v", err)
	}
	dataF32 = nil // allow GC
	runtime.GC()
	rssAfterF32 := getProcessRSS()
	allocAfterF32 := getHeapAlloc()
	rssDeltaRAMF32MB := float64(int64(rssAfterF32)-int64(rssBeforeF32)) / (1024 * 1024)
	allocDeltaRAMF32MB := float64(int64(allocAfterF32)-int64(allocBeforeF32)) / (1024 * 1024)

	// Warmup
	ramSearcherF32.Search(ctx, queries[0], topK)
	latenciesRAMF32 := make([]time.Duration, numQueries)
	var totalRecallRAMF32 float64
	for q := 0; q < numQueries; q++ {
		tSearch := time.Now()
		res, _, err := ramSearcherF32.Search(ctx, queries[q], topK)
		lat := time.Since(tSearch)
		if err != nil {
			t.Fatalf("search ram f32: %v", err)
		}
		latenciesRAMF32[q] = lat
		totalRecallRAMF32 += computeRecallAtK(res, groundTruth[q], topK)
	}
	recallRAMF32 := totalRecallRAMF32 / float64(numQueries)
	sort.Slice(latenciesRAMF32, func(i, j int) bool { return latenciesRAMF32[i] < latenciesRAMF32[j] })
	p50RAMF32 := percentile(latenciesRAMF32, 0.50)
	p95RAMF32 := percentile(latenciesRAMF32, 0.95)
	ramSearcherF32.Close()
	ramSearcherF32 = nil

	// 2. RAM Searcher: f16
	t.Logf("Measuring RAM Searcher (f16)...")
	runtime.GC()
	debug.FreeOSMemory()
	rssBeforeF16 := getProcessRSS()
	allocBeforeF16 := getHeapAlloc()

	dataF16, err := os.ReadFile(pathF16)
	if err != nil {
		t.Fatalf("read f16: %v", err)
	}
	ramSearcherF16 := &Searcher{config: cfg}
	if err := ramSearcherF16.Load(ctx, dataF16, metaF16); err != nil {
		t.Fatalf("load ram f16: %v", err)
	}
	dataF16 = nil // allow GC
	runtime.GC()
	rssAfterF16 := getProcessRSS()
	allocAfterF16 := getHeapAlloc()
	rssDeltaRAMF16MB := float64(int64(rssAfterF16)-int64(rssBeforeF16)) / (1024 * 1024)
	allocDeltaRAMF16MB := float64(int64(allocAfterF16)-int64(allocBeforeF16)) / (1024 * 1024)

	// Warmup
	ramSearcherF16.Search(ctx, queries[0], topK)
	latenciesRAMF16 := make([]time.Duration, numQueries)
	var totalRecallRAMF16 float64
	for q := 0; q < numQueries; q++ {
		tSearch := time.Now()
		res, _, err := ramSearcherF16.Search(ctx, queries[q], topK)
		lat := time.Since(tSearch)
		if err != nil {
			t.Fatalf("search ram f16: %v", err)
		}
		latenciesRAMF16[q] = lat
		totalRecallRAMF16 += computeRecallAtK(res, groundTruth[q], topK)
	}
	recallRAMF16 := totalRecallRAMF16 / float64(numQueries)
	sort.Slice(latenciesRAMF16, func(i, j int) bool { return latenciesRAMF16[i] < latenciesRAMF16[j] })
	p50RAMF16 := percentile(latenciesRAMF16, 0.50)
	p95RAMF16 := percentile(latenciesRAMF16, 0.95)
	ramSearcherF16.Close()
	ramSearcherF16 = nil

	// 3. Mmap Searcher: f32
	t.Logf("Measuring Mmap Searcher (f32)...")
	runtime.GC()
	debug.FreeOSMemory()
	rssBeforeMmapF32 := getProcessRSS()
	allocBeforeMmapF32 := getHeapAlloc()

	mmapSearcherF32 := &MmapSearcher{config: cfg}
	if err := mmapSearcherF32.LoadFromFile(ctx, pathF32); err != nil {
		t.Fatalf("load mmap f32: %v", err)
	}
	rssAfterMmapF32 := getProcessRSS()
	allocAfterMmapF32 := getHeapAlloc()
	rssDeltaMmapF32MB := float64(int64(rssAfterMmapF32)-int64(rssBeforeMmapF32)) / (1024 * 1024)
	allocDeltaMmapF32MB := float64(int64(allocAfterMmapF32)-int64(allocBeforeMmapF32)) / (1024 * 1024)

	// Warmup
	mmapSearcherF32.Search(ctx, queries[0], topK)
	latenciesMmapF32 := make([]time.Duration, numQueries)
	var totalRecallMmapF32 float64
	for q := 0; q < numQueries; q++ {
		tSearch := time.Now()
		res, _, err := mmapSearcherF32.Search(ctx, queries[q], topK)
		lat := time.Since(tSearch)
		if err != nil {
			t.Fatalf("search mmap f32: %v", err)
		}
		latenciesMmapF32[q] = lat
		totalRecallMmapF32 += computeRecallAtK(res, groundTruth[q], topK)
	}
	recallMmapF32 := totalRecallMmapF32 / float64(numQueries)
	sort.Slice(latenciesMmapF32, func(i, j int) bool { return latenciesMmapF32[i] < latenciesMmapF32[j] })
	p50MmapF32 := percentile(latenciesMmapF32, 0.50)
	p95MmapF32 := percentile(latenciesMmapF32, 0.95)
	mmapSearcherF32.Close()
	mmapSearcherF32 = nil

	// 4. Mmap Searcher: f16
	t.Logf("Measuring Mmap Searcher (f16)...")
	runtime.GC()
	debug.FreeOSMemory()
	rssBeforeMmapF16 := getProcessRSS()
	allocBeforeMmapF16 := getHeapAlloc()

	mmapSearcherF16 := &MmapSearcher{config: cfg}
	if err := mmapSearcherF16.LoadFromFile(ctx, pathF16); err != nil {
		t.Fatalf("load mmap f16: %v", err)
	}
	rssAfterMmapF16 := getProcessRSS()
	allocAfterMmapF16 := getHeapAlloc()
	rssDeltaMmapF16MB := float64(int64(rssAfterMmapF16)-int64(rssBeforeMmapF16)) / (1024 * 1024)
	allocDeltaMmapF16MB := float64(int64(allocAfterMmapF16)-int64(allocBeforeMmapF16)) / (1024 * 1024)

	// Warmup
	mmapSearcherF16.Search(ctx, queries[0], topK)
	latenciesMmapF16 := make([]time.Duration, numQueries)
	var totalRecallMmapF16 float64
	for q := 0; q < numQueries; q++ {
		tSearch := time.Now()
		res, _, err := mmapSearcherF16.Search(ctx, queries[q], topK)
		lat := time.Since(tSearch)
		if err != nil {
			t.Fatalf("search mmap f16: %v", err)
		}
		latenciesMmapF16[q] = lat
		totalRecallMmapF16 += computeRecallAtK(res, groundTruth[q], topK)
	}
	recallMmapF16 := totalRecallMmapF16 / float64(numQueries)
	sort.Slice(latenciesMmapF16, func(i, j int) bool { return latenciesMmapF16[i] < latenciesMmapF16[j] })
	p50MmapF16 := percentile(latenciesMmapF16, 0.50)
	p95MmapF16 := percentile(latenciesMmapF16, 0.95)
	mmapSearcherF16.Close()
	mmapSearcherF16 = nil

	// Log structured report
	t.Logf("\n=================== 100K x 768 BENCHMARK RESULTS ===================")
	t.Logf("Dataset: 100,000 vectors (50K uniform unit, 50K clustered, dims=768)")
	t.Logf("Index File Size: f32 = %.2f MB | f16 = %.2f MB | reduction = %.1f%% (%.2fx smaller)",
		sizeF32MB, sizeF16MB, (1.0-float64(sizeF16Bytes)/float64(sizeF32Bytes))*100, float64(sizeF32Bytes)/float64(sizeF16Bytes))
	t.Logf("--------------------------------------------------------------------------------")
	t.Logf("%-18s | %-12s | %-12s | %-10s | %-10s | %-12s", "Engine & DType", "RSS Post-Load", "Heap Alloc", "p50 Search", "p95 Search", "Recall@10")
	t.Logf("--------------------------------------------------------------------------------")
	t.Logf("%-18s | %9.1f MB  | %9.1f MB  | %10v | %10v | %11.4f", "RAM Searcher (f32)", float64(rssAfterF32)/(1024*1024), allocDeltaRAMF32MB, p50RAMF32, p95RAMF32, recallRAMF32)
	t.Logf("%-18s | %9.1f MB  | %9.1f MB  | %10v | %10v | %11.4f", "RAM Searcher (f16)", float64(rssAfterF16)/(1024*1024), allocDeltaRAMF16MB, p50RAMF16, p95RAMF16, recallRAMF16)
	t.Logf("%-18s | %9.1f MB  | %9.1f MB  | %10v | %10v | %11.4f", "Mmap Searcher (f32)", float64(rssAfterMmapF32)/(1024*1024), allocDeltaMmapF32MB, p50MmapF32, p95MmapF32, recallMmapF32)
	t.Logf("%-18s | %9.1f MB  | %9.1f MB  | %10v | %10v | %11.4f", "Mmap Searcher (f16)", float64(rssAfterMmapF16)/(1024*1024), allocDeltaMmapF16MB, p50MmapF16, p95MmapF16, recallMmapF16)
	t.Logf("================================================================================")
	_ = rssDeltaRAMF32MB
	_ = rssDeltaRAMF16MB
	_ = rssDeltaMmapF32MB
	_ = rssDeltaMmapF16MB
	_ = allocBeforeF32
	_ = allocBeforeF16
	_ = allocBeforeMmapF32
	_ = allocBeforeMmapF16
}
