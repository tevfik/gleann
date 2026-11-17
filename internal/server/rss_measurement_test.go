package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/modules/diskann"
	"github.com/tevfik/gleann/pkg/gleann"
)

func getRSSKiB() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				return kb
			}
		}
	}
	return 0
}

func createBenchIndex(t *testing.T, indexDir, name string, numVectors, dims int) {
	t.Helper()
	dir := filepath.Join(indexDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	basePath := filepath.Join(dir, name)

	rng := rand.New(rand.NewSource(42))
	embeddings := make([][]float32, numVectors)
	for i := 0; i < numVectors; i++ {
		vec := make([]float32, dims)
		for d := 0; d < dims; d++ {
			vec[d] = rng.Float32()
		}
		embeddings[i] = vec
	}

	cfg := diskann.DiskANNConfig{
		R:              32,
		L:              64,
		Alpha:          1.2,
		PQDim:          dims / 4,
		PQCentroids:    64,
		SearchL:        64,
		DistanceMetric: diskann.DistanceL2,
		UseMmap:        true,
	}

	idx, err := diskann.BuildDiskIndex(embeddings, cfg)
	if err != nil {
		t.Fatalf("build disk index: %v", err)
	}

	var buf bytes.Buffer
	if _, err := idx.WriteTo(&buf); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(basePath+".index", buf.Bytes(), 0644); err != nil {
		t.Fatalf("write index file: %v", err)
	}

	meta := gleann.IndexMeta{
		Name:           name,
		Backend:        "diskann",
		EmbeddingModel: "bge-m3",
		Dimensions:     dims,
		NumPassages:    numVectors,
		CreatedAt:      time.Now(),
		Version:        "1.0.0",
		StoreFormat:    1,
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(basePath+".meta.json", metaBytes, 0644); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	ps, err := gleann.OpenPassageStore(basePath, false)
	if err != nil {
		t.Fatalf("open passage store: %v", err)
	}
	items := make([]gleann.Item, numVectors)
	for i := 0; i < numVectors; i++ {
		items[i] = gleann.Item{
			Text:     fmt.Sprintf("document %d content for %s with enough sample text words for testing", i, name),
			Metadata: map[string]any{"file": fmt.Sprintf("doc_%d.txt", i)},
		}
	}
	if _, err := ps.Add(items); err != nil {
		ps.Close()
		t.Fatalf("add passages: %v", err)
	}
	if err := ps.Close(); err != nil {
		t.Fatalf("close passage store: %v", err)
	}
}

func TestMeasureServerRSS(t *testing.T) {
	if os.Getenv("GLEANN_RSS_MEASURE") == "" {
		t.Skip("measurement, not a check: run with GLEANN_RSS_MEASURE=1")
	}
	tmpDir := t.TempDir()
	numVectors := 8000
	dims := 256

	t.Logf("=== Gleann Serve Searcher Cache RSS Measurement ===")
	t.Logf("Generating 3 test indexes (%d vectors, dim=%d each) in %s...", numVectors, dims, tmpDir)
	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("bench_idx_%d", i)
		createBenchIndex(t, tmpDir, name, numVectors, dims)
	}

	// 1. RAM Mode (No Mmap, No Budget)
	{
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		rssStart := getRSSKiB()

		cfg := gleann.DefaultConfig()
		cfg.IndexDir = tmpDir
		cfg.Backend = "diskann"
		f := false
		cfg.UseMmap = &f
		cfg.DiskANNConfig.UseMmap = false
		cfg.SearcherCacheBudget = 0

		srv := NewServer(cfg, "127.0.0.1:0", "dev")

		var releases []func()
		for i := 1; i <= 3; i++ {
			name := fmt.Sprintf("bench_idx_%d", i)
			s, release, err := srv.acquireSearcher(context.Background(), name)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			releases = append(releases, release)
			_, _, _ = s.Backend().Search(context.Background(), make([]float32, dims), 5)
		}
		for _, rel := range releases {
			rel()
		}

		runtime.GC()
		rssLoaded := getRSSKiB()
		srv.invalidateAllSearchers()

		delta := rssLoaded - rssStart
		fmt.Printf("\n[1. BEFORE: RAM Mode (No Mmap, No Memory Budget)]\n")
		fmt.Printf("  RSS Baseline   : %d KiB (%.2f MiB)\n", rssStart, float64(rssStart)/1024)
		fmt.Printf("  RSS with 3 Idxs: %d KiB (%.2f MiB)\n", rssLoaded, float64(rssLoaded)/1024)
		fmt.Printf("  Net Delta      : +%d KiB (+%.2f MiB)\n", delta, float64(delta)/1024)
	}

	// 2. Mmap Mode (Zero-Copy Mmap, No Budget)
	{
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		rssStart := getRSSKiB()

		cfg := gleann.DefaultConfig()
		cfg.IndexDir = tmpDir
		cfg.Backend = "diskann"
		tr := true
		cfg.UseMmap = &tr
		cfg.DiskANNConfig.UseMmap = true
		cfg.SearcherCacheBudget = 0

		srv := NewServer(cfg, "127.0.0.1:0", "dev")

		var releases []func()
		for i := 1; i <= 3; i++ {
			name := fmt.Sprintf("bench_idx_%d", i)
			s, release, err := srv.acquireSearcher(context.Background(), name)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			releases = append(releases, release)
			_, _, _ = s.Backend().Search(context.Background(), make([]float32, dims), 5)
		}
		for _, rel := range releases {
			rel()
		}

		runtime.GC()
		rssLoaded := getRSSKiB()
		srv.invalidateAllSearchers()

		delta := rssLoaded - rssStart
		fmt.Printf("\n[2. AFTER: Mmap Mode (Zero-Copy Mmap, No Budget)]\n")
		fmt.Printf("  RSS Baseline   : %d KiB (%.2f MiB)\n", rssStart, float64(rssStart)/1024)
		fmt.Printf("  RSS with 3 Idxs: %d KiB (%.2f MiB)\n", rssLoaded, float64(rssLoaded)/1024)
		fmt.Printf("  Net Delta      : +%d KiB (+%.2f MiB)\n", delta, float64(delta)/1024)
	}

	// 3. Mmap Mode + Size-Aware Eviction Budget (12 MiB cap)
	{
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		rssStart := getRSSKiB()

		cfg := gleann.DefaultConfig()
		cfg.IndexDir = tmpDir
		cfg.Backend = "diskann"
		tr := true
		cfg.UseMmap = &tr
		cfg.DiskANNConfig.UseMmap = true
		cfg.SearcherCacheBudget = 12 * 1024 * 1024 // 12 MiB budget (~1 index fits)

		srv := NewServer(cfg, "127.0.0.1:0", "dev")

		for i := 1; i <= 3; i++ {
			name := fmt.Sprintf("bench_idx_%d", i)
			s, release, err := srv.acquireSearcher(context.Background(), name)
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}
			_, _, _ = s.Backend().Search(context.Background(), make([]float32, dims), 5)
			release()
		}

		runtime.GC()
		rssLoaded := getRSSKiB()
		cachedCount := srv.cachedSearcherCount()
		srv.invalidateAllSearchers()

		delta := rssLoaded - rssStart
		fmt.Printf("\n[3. AFTER: Mmap Mode + Size-Aware Eviction (12 MiB cap)]\n")
		fmt.Printf("  Cached Searchers: %d in cache (older searchers evicted by LRU)\n", cachedCount)
		fmt.Printf("  RSS Baseline   : %d KiB (%.2f MiB)\n", rssStart, float64(rssStart)/1024)
		fmt.Printf("  RSS with Cache : %d KiB (%.2f MiB)\n", rssLoaded, float64(rssLoaded)/1024)
		fmt.Printf("  Net Delta      : +%d KiB (+%.2f MiB)\n", delta, float64(delta)/1024)
	}
}
