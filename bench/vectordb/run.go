package vectordb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// Config parameters for a benchmark run.
type Config struct {
	Sizes   []int    // vector counts
	Dims    int      // vector dimensions (default 384)
	Queries int      // number of queries (default 1000)
	K       int      // top-k (default 10)
	Metric  string   // distance metric (default "cosine")
	Stores  []string // registered stores to benchmark
	WorkDir string   // scratch directory; "" = os.TempDir
	Log     func(format string, args ...any)
}

func (c *Config) defaults() {
	if len(c.Sizes) == 0 {
		c.Sizes = []int{10000}
	}
	if c.Dims <= 0 {
		c.Dims = 384
	}
	if c.Queries <= 0 {
		c.Queries = 1000
	}
	if c.K <= 0 {
		c.K = 10
	}
	if c.Metric == "" {
		c.Metric = "cosine"
	}
	if len(c.Stores) == 0 {
		c.Stores = Names()
	}
	if c.Log == nil {
		c.Log = func(string, ...any) {}
	}
}

// Run executes the complete benchmark across sizes and stores.
func Run(ctx context.Context, cfg Config) (*Report, error) {
	cfg.defaults()

	for _, name := range cfg.Stores {
		if _, err := New(name); err != nil {
			return nil, fmt.Errorf("verifying store %s: %w", name, err)
		}
	}

	workDir := cfg.WorkDir
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), fmt.Sprintf("vectordb-bench-%d", time.Now().UnixNano()))
		defer os.RemoveAll(workDir)
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("creating workdir %s: %w", workDir, err)
	}

	rep := &Report{
		Time:    time.Now().Format("2006-01-02 15:04:05"),
		Go:      runtime.Version(),
		Platf:   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		CPUs:    runtime.NumCPU(),
		Dims:    cfg.Dims,
		K:       cfg.K,
		Queries: cfg.Queries,
	}

	for _, size := range cfg.Sizes {
		cfg.Log("=== Benchmark size %d vectors, %d dims, %d queries ===", size, cfg.Dims, cfg.Queries)

		// 1. Generate synthetic dataset
		cfg.Log("Generating dataset (size=%d, dims=%d, queries=%d)...", size, cfg.Dims, cfg.Queries)
		ds := GenerateDataset(size, cfg.Dims, cfg.Queries, 42)

		// 2. Load or compute exact ground truth
		cfg.Log("Computing/loading exact ground truth...")
		truth, err := LoadOrComputeGroundTruth(workDir, ds, cfg.Metric)
		if err != nil {
			return nil, fmt.Errorf("ground truth generation: %w", err)
		}

		sizeRun := SizeRun{
			Size:    size,
			Dims:    cfg.Dims,
			Queries: cfg.Queries,
			K:       cfg.K,
		}

		for _, name := range cfg.Stores {
			cfg.Log("Running store %s...", name)
			res := runStore(ctx, name, ds, truth, cfg, workDir)
			sizeRun.Results = append(sizeRun.Results, res)
		}

		rep.Sizes = append(rep.Sizes, sizeRun)
	}

	return rep, nil
}

func runStore(ctx context.Context, name string, ds *Dataset, truth *GroundTruth, cfg Config, parentDir string) *StoreResult {
	storeDir := filepath.Join(parentDir, fmt.Sprintf("%s-%d", name, len(ds.IDs)))
	_ = os.RemoveAll(storeDir)
	_ = os.MkdirAll(storeDir, 0755)
	defer os.RemoveAll(storeDir)

	res := &StoreResult{
		Store:         name,
		RecallMatched: make(map[string]RecallMatchedRow),
		Filtered:      make(map[string]FilteredRow),
	}

	store, err := New(name)
	if err != nil {
		res.Error = fmt.Sprintf("instantiating store: %v", err)
		return res
	}
	res.Caveats = store.Caveats()

	params := Params{
		Dims:           cfg.Dims,
		Metric:         cfg.Metric,
		M:              32,
		EfConstruction: 200,
		EfSearch:       128,
	}

	// ── Phase 1: Load (Bulk Insert + Flush) ──
	// Return the previous store's freed heap to the OS first: otherwise the
	// baseline includes it, this store reuses it without growing RSS, and
	// its delta comes out far too small (diskann after hnsw at 100K x 768:
	// 157 MiB; alone: 1,369 MiB).
	debug.FreeOSMemory()
	baselineRSS := currentRSSKB()
	resetPeakRSS()

	t0 := time.Now()
	if err := store.Open(ctx, storeDir, params); err != nil {
		res.Error = fmt.Sprintf("open: %v", err)
		return res
	}
	if err := store.Insert(ctx, ds.IDs, ds.Vectors, ds.Metas); err != nil {
		res.Error = fmt.Sprintf("insert: %v", err)
		_ = store.Close()
		return res
	}
	if err := store.Flush(ctx); err != nil {
		res.Error = fmt.Sprintf("flush: %v", err)
		_ = store.Close()
		return res
	}
	res.LoadMS = float64(time.Since(t0).Microseconds()) / 1000.0
	res.PeakRSSDeltaKB = max(0, peakRSSKB()-baselineRSS)

	stats, _ := store.Stats(ctx)
	if stats.DiskBytes > 0 {
		res.DiskBytes = stats.DiskBytes
	} else {
		res.DiskBytes = dirSize(storeDir)
	}
	res.ServerRSSKB = stats.ServerRSS >> 10

	// ── Phase 2: Cold Start ──
	if err := store.Close(); err != nil {
		res.Error = fmt.Sprintf("close before cold start: %v", err)
		return res
	}

	tCold := time.Now()
	if err := store.Open(ctx, storeDir, params); err != nil {
		res.Error = fmt.Sprintf("open cold start: %v", err)
		return res
	}
	if len(ds.Queries) > 0 {
		if _, err := store.Search(ctx, ds.Queries[0], cfg.K, nil); err != nil {
			res.Error = fmt.Sprintf("cold search: %v", err)
			_ = store.Close()
			return res
		}
	}
	res.ColdStartMS = float64(time.Since(tCold).Microseconds()) / 1000.0

	// ── Phase 3: Search with ef sweep (concurrency 1 and 8) ──
	efs := []int{16, 32, 64, 128, 256}
	for _, ef := range efs {
		if ef < cfg.K {
			continue
		}
		if setter, ok := store.(EfSearchSetter); ok {
			setter.SetEfSearch(ef)
		} else {
			_ = store.Close()
			params.EfSearch = ef
			if err := store.Open(ctx, storeDir, params); err != nil {
				res.Error = fmt.Sprintf("reopen for ef=%d: %v", ef, err)
				return res
			}
		}

		// Concurrency 1 search
		c1Latencies := make([]int64, len(ds.Queries))
		c1Results := make([][]int64, len(ds.Queries))
		tC1Start := time.Now()
		for i, q := range ds.Queries {
			qStart := time.Now()
			ids, sErr := store.Search(ctx, q, cfg.K, nil)
			c1Latencies[i] = time.Since(qStart).Microseconds()
			if sErr != nil {
				res.Error = fmt.Sprintf("search ef=%d query %d: %v", ef, i, sErr)
				_ = store.Close()
				return res
			}
			c1Results[i] = ids
		}
		c1DurationSec := time.Since(tC1Start).Seconds()
		qpsC1 := float64(len(ds.Queries)) / c1DurationSec
		p50, p95, p99 := percentiles(c1Latencies)
		recall := AverageRecallAtK(truth.ExactTop100, c1Results, cfg.K)

		// Concurrency 8 search
		qpsC8 := runConcurrentSearch(ctx, store, ds.Queries, cfg.K, 8)

		sp := SweepPoint{
			EfSearch: ef,
			Recall:   recall,
			P50us:    p50,
			P95us:    p95,
			P99us:    p99,
			QPSC1:    qpsC1,
			QPSC8:    qpsC8,
		}
		res.Sweep = append(res.Sweep, sp)
	}

	// Match recall targets (>=0.90, >=0.95, >=0.99) with cheapest ef
	targets := []struct {
		name string
		val  float64
	}{
		{"0.90", 0.90},
		{"0.95", 0.95},
		{"0.99", 0.99},
	}
	for _, tg := range targets {
		for _, sp := range res.Sweep {
			if sp.Recall >= tg.val {
				res.RecallMatched[tg.name] = RecallMatchedRow{
					TargetRecall:   tg.val,
					AchievedRecall: sp.Recall,
					EfSearch:       sp.EfSearch,
					P50us:          sp.P50us,
					P95us:          sp.P95us,
					P99us:          sp.P99us,
					QPSC1:          sp.QPSC1,
					QPSC8:          sp.QPSC8,
					Reached:        true,
				}
				break
			}
		}
	}

	// ── Phase 4: Filtered Search (1%, 10%, 50% selectivity) ──
	filterTests := []struct {
		name        string
		filter      *Filter
		groundTruth [][]int64
	}{
		{"1%", &Filter{TagMax: 1}, truth.Filtered1Pct},
		{"10%", &Filter{TagMax: 10}, truth.Filtered10Pct},
		{"50%", &Filter{TagMax: 50}, truth.Filtered50Pct},
	}

	for _, ft := range filterTests {
		latencies := make([]int64, len(ds.Queries))
		results := make([][]int64, len(ds.Queries))
		for i, q := range ds.Queries {
			qStart := time.Now()
			ids, fErr := store.Search(ctx, q, cfg.K, ft.filter)
			latencies[i] = time.Since(qStart).Microseconds()
			if fErr != nil {
				res.Error = fmt.Sprintf("filtered search %s: %v", ft.name, fErr)
				_ = store.Close()
				return res
			}
			results[i] = ids
		}
		p50, p95, _ := percentiles(latencies)
		rec := AverageRecallAtK(ft.groundTruth, results, cfg.K)
		res.Filtered[ft.name] = FilteredRow{
			Selectivity: ft.name,
			Recall:      rec,
			P50us:       p50,
			P95us:       p95,
		}
	}

	// ── Phase 5: File update (~24 passages) ──
	// Collect IDs belonging to file_000000.go
	var fileIDs []int64
	var fileMetas []Meta
	for i, m := range ds.Metas {
		if m.Source == "file_000000.go" {
			fileIDs = append(fileIDs, ds.IDs[i])
			fileMetas = append(fileMetas, m)
		}
	}
	if len(fileIDs) == 0 && len(ds.IDs) > 0 {
		n := min(24, len(ds.IDs))
		fileIDs = ds.IDs[:n]
		fileMetas = ds.Metas[:n]
	}

	// Create updated vectors
	updatedVecs := make([][]float32, len(fileIDs))
	for i := range fileIDs {
		vec := make([]float32, cfg.Dims)
		for d := 0; d < cfg.Dims; d++ {
			vec[d] = ds.Vectors[fileIDs[i]][d] * 0.95
		}
		unitNormalize(vec)
		updatedVecs[i] = vec
	}

	tUp := time.Now()
	if err := store.Upsert(ctx, fileIDs, updatedVecs, fileMetas); err != nil {
		res.Error = fmt.Sprintf("upsert file: %v", err)
		_ = store.Close()
		return res
	}
	if err := store.Flush(ctx); err != nil {
		res.Error = fmt.Sprintf("flush after upsert: %v", err)
		_ = store.Close()
		return res
	}
	res.FileUpdateMS = float64(time.Since(tUp).Microseconds()) / 1000.0

	// ── Phase 6: Footprint & Close ──
	if err := store.Close(); err != nil {
		res.Error = fmt.Sprintf("close final: %v", err)
		return res
	}
	res.DiskBytes = dirSize(storeDir)

	return res
}

func runConcurrentSearch(ctx context.Context, store Store, queries [][]float32, k, concurrency int) float64 {
	type job struct {
		idx int
		q   []float32
	}

	jobs := make(chan job, len(queries))
	for i, q := range queries {
		jobs <- job{idx: i, q: q}
	}
	close(jobs)

	var wg sync.WaitGroup
	t0 := time.Now()
	for c := 0; c < concurrency; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				_, _ = store.Search(ctx, j.q, k, nil)
			}
		}()
	}
	wg.Wait()
	sec := time.Since(t0).Seconds()
	if sec <= 0 {
		return 0
	}
	return float64(len(queries)) / sec
}

func dirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

func percentiles(us []int64) (p50, p95, p99 int64) {
	if len(us) == 0 {
		return 0, 0, 0
	}
	sorted := make([]int64, len(us))
	copy(sorted, us)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	p50 = sorted[len(sorted)*50/100]
	p95 = sorted[len(sorted)*95/100]
	p99 = sorted[len(sorted)*99/100]
	return
}
