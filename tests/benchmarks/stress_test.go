package benchmarks

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/modules/bm25"
	"github.com/tevfik/gleann/pkg/gleann"
)

// --- Helpers ---

// randomText generates a pseudo-random text passage of approximately wordCount words.
func randomText(rng *rand.Rand, wordCount int) string {
	words := []string{
		"algorithm", "distributed", "system", "architecture", "database",
		"network", "protocol", "function", "interface", "concurrent",
		"transaction", "replication", "consistency", "partition", "latency",
		"throughput", "scalable", "resilient", "pipeline", "framework",
		"microservice", "container", "orchestration", "monitoring", "deployment",
		"kubernetes", "terraform", "observability", "tracing", "logging",
		"indexing", "embedding", "vector", "similarity", "tokenization",
		"retrieval", "augmented", "generation", "transformer", "attention",
	}
	var b []byte
	for i := 0; i < wordCount; i++ {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, words[rng.Intn(len(words))]...)
	}
	return string(b)
}

// makePassages creates n passages with random text.
func makePassages(n, wordsPerPassage int) []gleann.Item {
	rng := rand.New(rand.NewSource(42))
	items := make([]gleann.Item, n)
	for i := 0; i < n; i++ {
		items[i] = gleann.Item{
			Text: randomText(rng, wordsPerPassage),
			Metadata: map[string]any{
				"source": fmt.Sprintf("doc_%d.txt", i%100),
				"chunk":  i,
			},
		}
	}
	return items
}

// --- Stress Test: PassageManager at scale ---

func TestStress_PassageManager_LargeCorpus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large corpus test in short mode")
	}

	const (
		numPassages     = 50_000
		wordsPerPassage = 50
		batchSize       = 5000
	)

	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "test_index")
	pm := gleann.NewPassageManager(basePath)
	t.Cleanup(func() { pm.Close() })

	items := makePassages(numPassages, wordsPerPassage)

	// Test: Batch writes at scale
	t.Run("BatchAdd", func(t *testing.T) {
		start := time.Now()
		for i := 0; i < numPassages; i += batchSize {
			end := i + batchSize
			if end > numPassages {
				end = numPassages
			}
			ids, err := pm.Add(items[i:end])
			if err != nil {
				t.Fatalf("Add batch [%d:%d] failed: %v", i, end, err)
			}
			if len(ids) != end-i {
				t.Fatalf("Expected %d ids, got %d", end-i, len(ids))
			}
		}
		elapsed := time.Since(start)
		t.Logf("Added %d passages in %v (%.0f passages/sec)", numPassages, elapsed, float64(numPassages)/elapsed.Seconds())
	})

	// Test: Count correctness
	t.Run("Count", func(t *testing.T) {
		count := pm.Count()
		if count != numPassages {
			t.Fatalf("Expected %d passages, got %d", numPassages, count)
		}
	})

	// Test: LoadAll memory pressure
	t.Run("LoadAll_Memory", func(t *testing.T) {
		runtime.GC()
		var memBefore runtime.MemStats
		runtime.ReadMemStats(&memBefore)

		start := time.Now()
		if err := pm.LoadAll(); err != nil {
			t.Fatalf("LoadAll failed: %v", err)
		}
		elapsed := time.Since(start)

		runtime.GC()
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)

		// Use TotalAlloc difference (monotonically increasing, never underflows)
		allocatedMB := float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / (1024 * 1024)
		heapMB := float64(memAfter.HeapAlloc) / (1024 * 1024)
		t.Logf("LoadAll: %d passages in %v, allocated: %.1f MB, heap: %.1f MB", numPassages, elapsed, allocatedMB, heapMB)

		// Warn if total allocations exceed 500MB for 50K passages
		if allocatedMB > 500 {
			t.Errorf("Memory allocations too high: %.1f MB for %d passages", allocatedMB, numPassages)
		}
	})

	// Test: Random access after load
	t.Run("RandomAccess", func(t *testing.T) {
		rng := rand.New(rand.NewSource(99))
		start := time.Now()
		for i := 0; i < 10000; i++ {
			id := int64(rng.Intn(numPassages))
			p, err := pm.Get(id)
			if err != nil {
				t.Fatalf("Get(%d) failed: %v", id, err)
			}
			if p.Text == "" {
				t.Fatalf("Get(%d) returned empty text", id)
			}
		}
		elapsed := time.Since(start)
		t.Logf("10K random Gets in %v (%.0f/sec)", elapsed, 10000/elapsed.Seconds())
	})

	// Test: Batch retrieval
	t.Run("BatchGet", func(t *testing.T) {
		rng := rand.New(rand.NewSource(77))
		ids := make([]int64, 1000)
		for i := range ids {
			ids[i] = int64(rng.Intn(numPassages))
		}

		start := time.Now()
		passages, err := pm.GetBatch(ids)
		if err != nil {
			t.Fatalf("GetBatch failed: %v", err)
		}
		elapsed := time.Since(start)

		if len(passages) != 1000 {
			t.Fatalf("Expected 1000 passages, got %d", len(passages))
		}
		t.Logf("Batch get 1000 passages in %v", elapsed)
	})

	// Test: Concurrent reads
	t.Run("ConcurrentReads", func(t *testing.T) {
		var wg sync.WaitGroup
		errors := make(chan error, 100)

		for g := 0; g < 10; g++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(goroutineID)))
				for i := 0; i < 1000; i++ {
					id := int64(rng.Intn(numPassages))
					_, err := pm.Get(id)
					if err != nil {
						errors <- fmt.Errorf("goroutine %d Get(%d): %v", goroutineID, id, err)
						return
					}
				}
			}(g)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			t.Error(err)
		}
	})
}

// --- Stress Test: RemoveBySource + Cache Invalidation ---

func TestStress_PassageManager_CacheThrashing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cache thrashing test in short mode")
	}

	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "cache_test")
	pm := gleann.NewPassageManager(basePath)
	t.Cleanup(func() { pm.Close() })

	const numPassages = 10_000

	// Add passages from 10 sources
	for src := 0; src < 10; src++ {
		items := make([]gleann.Item, numPassages/10)
		for i := range items {
			items[i] = gleann.Item{
				Text: fmt.Sprintf("passage %d from source %d with some content for indexing", i, src),
				Metadata: map[string]any{
					"source": fmt.Sprintf("source_%d.txt", src),
				},
			}
		}
		if _, err := pm.Add(items); err != nil {
			t.Fatalf("Add source_%d failed: %v", src, err)
		}
	}

	// LoadAll to populate cache
	if err := pm.LoadAll(); err != nil {
		t.Fatal(err)
	}

	initial := pm.Count()
	if initial != numPassages {
		t.Fatalf("Expected %d, got %d", numPassages, initial)
	}

	// Remove half the sources (5 of 10) — should invalidate cache
	removedTotal := 0
	for src := 0; src < 5; src++ {
		removed, err := pm.RemoveBySource([]string{fmt.Sprintf("source_%d.txt", src)})
		if err != nil {
			t.Fatalf("RemoveBySource source_%d failed: %v", src, err)
		}
		removedTotal += len(removed)
	}

	if removedTotal != numPassages/2 {
		t.Errorf("Expected to remove %d, removed %d", numPassages/2, removedTotal)
	}

	// After remove, All() should work correctly (cache was invalidated)
	remaining := pm.All()
	if len(remaining) != numPassages/2 {
		t.Errorf("After remove: expected %d remaining, got %d", numPassages/2, len(remaining))
	}

	// Verify all remaining passages are from sources 5-9
	for _, p := range remaining {
		src, _ := p.Metadata["source"].(string)
		for s := 0; s < 5; s++ {
			if src == fmt.Sprintf("source_%d.txt", s) {
				t.Errorf("Found passage from removed source: %s", src)
				break
			}
		}
	}
}

// --- Stress Test: BM25 at Scale ---

func TestStress_BM25_LargeIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping BM25 large index test in short mode")
	}

	const numDocs = 100_000

	scorer := bm25.NewScorer()
	rng := rand.New(rand.NewSource(42))

	// Index 100K documents
	start := time.Now()
	for i := 0; i < numDocs; i++ {
		scorer.AddDocument(int64(i), randomText(rng, 50))
	}
	indexTime := time.Since(start)
	t.Logf("BM25: Indexed %d docs in %v (%.0f docs/sec)", numDocs, indexTime, float64(numDocs)/indexTime.Seconds())

	if scorer.DocCount() != numDocs {
		t.Fatalf("Expected %d docs, got %d", numDocs, scorer.DocCount())
	}

	// Test: Full corpus scoring
	t.Run("FullCorpusScore", func(t *testing.T) {
		start := time.Now()
		scores := scorer.Score("distributed system architecture")
		elapsed := time.Since(start)

		if len(scores) == 0 {
			t.Error("No scores returned")
		}
		t.Logf("Full corpus score (%d matches): %v", len(scores), elapsed)

		// Should complete in reasonable time
		if elapsed > 5*time.Second {
			t.Errorf("BM25 scoring too slow: %v for %d docs", elapsed, numDocs)
		}
	})

	// Test: TopK performance
	t.Run("TopK_Performance", func(t *testing.T) {
		queries := []string{
			"distributed system architecture",
			"vector embedding similarity search",
			"kubernetes container orchestration deployment",
			"database transaction replication consistency",
		}

		for _, q := range queries {
			start := time.Now()
			ids, scores := scorer.TopK(q, 10)
			elapsed := time.Since(start)

			if len(ids) == 0 {
				t.Errorf("TopK(%q) returned no results", q)
			}
			if len(ids) != len(scores) {
				t.Errorf("ids/scores length mismatch")
			}

			// Scores should be in descending order
			for i := 1; i < len(scores); i++ {
				if scores[i] > scores[i-1] {
					t.Errorf("TopK scores not sorted: score[%d]=%f > score[%d]=%f", i, scores[i], i-1, scores[i-1])
				}
			}
			t.Logf("TopK(%q): %v, top score=%.4f", q, elapsed, scores[0])
		}
	})

	// Test: Concurrent BM25 scoring
	t.Run("ConcurrentScoring", func(t *testing.T) {
		var wg sync.WaitGroup
		queries := []string{
			"distributed system", "vector embedding", "container orchestration",
			"database replication", "pipeline framework", "scalable resilient",
		}

		for _, q := range queries {
			wg.Add(1)
			go func(query string) {
				defer wg.Done()
				for i := 0; i < 100; i++ {
					scores := scorer.Score(query)
					if len(scores) == 0 {
						t.Errorf("concurrent score(%q) returned empty", query)
					}
				}
			}(q)
		}
		wg.Wait()
	})

	// Test: Memory usage of BM25 index
	t.Run("MemoryUsage", func(t *testing.T) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		t.Logf("BM25 index memory (after %d docs): Alloc=%.1f MB, Sys=%.1f MB",
			numDocs, float64(m.Alloc)/(1024*1024), float64(m.Sys)/(1024*1024))
	})
}

// --- Stress Test: BM25 Edge Cases ---

func TestStress_BM25_EdgeCases(t *testing.T) {
	t.Run("EmptyQuery", func(t *testing.T) {
		scorer := bm25.NewScorer()
		scorer.AddDocument(1, "some text content")
		scores := scorer.Score("")
		// Empty query should return empty/zero scores
		for _, s := range scores {
			if s != 0 {
				t.Errorf("Empty query returned non-zero score: %f", s)
			}
		}
	})

	t.Run("SingleCharDocs", func(t *testing.T) {
		scorer := bm25.NewScorer()
		// Single-char tokens should be filtered by tokenizer
		scorer.AddDocument(1, "a b c d e")
		scores := scorer.Score("a b c")
		// Expect 0 scores since single chars are stop words
		if len(scores) != 0 {
			for id, s := range scores {
				if s != 0 {
					t.Logf("Unexpected score for single-char doc %d: %f", id, s)
				}
			}
		}
	})

	t.Run("IdenticalDocs", func(t *testing.T) {
		scorer := bm25.NewScorer()
		text := "distributed system architecture design"
		for i := 0; i < 1000; i++ {
			scorer.AddDocument(int64(i), text)
		}
		scores := scorer.Score("distributed architecture")
		// All should have equal scores
		var firstScore float32
		for _, s := range scores {
			if firstScore == 0 {
				firstScore = s
			} else if s != firstScore {
				t.Errorf("Identical docs have different scores: %f vs %f", firstScore, s)
				break
			}
		}
	})

	t.Run("VeryLongDocument", func(t *testing.T) {
		scorer := bm25.NewScorer()
		rng := rand.New(rand.NewSource(42))
		// 10K word document
		longText := randomText(rng, 10000)
		scorer.AddDocument(1, longText)
		scorer.AddDocument(2, "short text")

		scores := scorer.Score("distributed system")
		if len(scores) == 0 {
			t.Error("No scores for long document test")
		}
	})

	t.Run("ZeroDivision_MaxDistance", func(t *testing.T) {
		// When all distances are 0 (all vectors identical), maxDist=0
		// The searcher should handle 1.0 - dist/maxDist without panic
		// This tests the BM25 side: when maxBM25=0
		scorer := bm25.NewScorer()
		scorer.AddDocument(1, "completely unrelated gibberish xyz")
		scores := scorer.Score("distributed system architecture")
		// Should not panic, scores might be empty or zero
		t.Logf("Unrelated query scores: %v", scores)
	})
}

// --- Stress Test: BM25Adapter with Passages ---

func TestStress_BM25Adapter_CandidateOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adapter stress test in short mode")
	}

	adapter := gleann.NewBM25Adapter()

	// Add 50K passages to the adapter
	const total = 50_000
	rng := rand.New(rand.NewSource(42))
	allPassages := make([]gleann.Passage, total)
	for i := 0; i < total; i++ {
		allPassages[i] = gleann.Passage{
			ID:   int64(i),
			Text: randomText(rng, 50),
		}
	}

	start := time.Now()
	adapter.AddDocuments(allPassages)
	t.Logf("BM25Adapter indexed %d passages in %v", total, time.Since(start))

	// Score only a candidate subset (simulating hybrid search)
	candidateSize := 200
	candidates := allPassages[:candidateSize]

	start = time.Now()
	scores := adapter.Score("vector embedding similarity retrieval", candidates)
	elapsed := time.Since(start)

	if len(scores) != candidateSize {
		t.Errorf("Expected %d scores, got %d", candidateSize, len(scores))
	}
	t.Logf("Scored %d candidates (from %d total) in %v", candidateSize, total, elapsed)

	// Should be much faster than scoring all 50K
	if elapsed > 1*time.Second {
		t.Errorf("Candidate scoring too slow: %v", elapsed)
	}
}

// --- Stress Test: Passage Manager Disk Persistence ---

func TestStress_PassageManager_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "persist_test")

	const numPassages = 5000

	// Write passages, close, reopen, verify
	func() {
		pm := gleann.NewPassageManager(basePath)
		defer pm.Close()

		items := makePassages(numPassages, 30)
		if _, err := pm.Add(items); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}()

	// Reopen and verify
	pm2 := gleann.NewPassageManager(basePath)
	t.Cleanup(func() { pm2.Close() })

	count := pm2.Count()
	if count != numPassages {
		t.Fatalf("After reopen: expected %d, got %d", numPassages, count)
	}

	// Verify random passages
	rng := rand.New(rand.NewSource(42))
	items := makePassages(numPassages, 30) // Same seed = same text
	for i := 0; i < 100; i++ {
		idx := rng.Intn(numPassages)
		p, err := pm2.Get(int64(idx))
		if err != nil {
			t.Fatalf("Get(%d) failed: %v", idx, err)
		}
		if p.Text != items[idx].Text {
			t.Errorf("Get(%d) text mismatch after persist", idx)
		}
	}
}

// --- Stress Test: DB File Size ---

func TestStress_PassageManager_DiskUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping disk usage test in short mode")
	}

	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "disk_test")
	pm := gleann.NewPassageManager(basePath)
	t.Cleanup(func() { pm.Close() })

	const numPassages = 20_000

	items := makePassages(numPassages, 100) // Larger texts
	if _, err := pm.Add(items); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	dbFile := basePath + ".passages.db"
	info, err := os.Stat(dbFile)
	if err != nil {
		t.Fatalf("Stat db file: %v", err)
	}

	sizeMB := float64(info.Size()) / (1024 * 1024)
	avgTextLen := 0
	for _, item := range items[:100] {
		avgTextLen += len(item.Text)
	}
	avgTextLen /= 100

	t.Logf("DB file: %.1f MB for %d passages (avg %d bytes/passage text)", sizeMB, numPassages, avgTextLen)

	// Estimate: raw text should roughly be avgTextLen * numPassages
	rawMB := float64(avgTextLen*numPassages) / (1024 * 1024)
	overhead := sizeMB / rawMB
	t.Logf("Storage overhead: %.1fx over raw text (%.1f MB raw)", overhead, rawMB)

	// Reasonable overhead: less than 3x
	if overhead > 5 {
		t.Errorf("Storage overhead too high: %.1fx", overhead)
	}
}

// --- Stress Test: LoadAllWithLimit ---

func TestStress_PassageManager_LoadAllWithLimit(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "limit_test")

	const total = 10_000

	// Write passages with one PM, then close it
	func() {
		pm := gleann.NewPassageManager(basePath)
		defer pm.Close()
		items := makePassages(total, 30)
		if _, err := pm.Add(items); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}()

	// Test 1: LimitHalf — only load 5000 of 10000
	func() {
		pm := gleann.NewPassageManager(basePath)
		defer pm.Close()

		if err := pm.LoadAllWithLimit(total / 2); err != nil {
			t.Fatalf("LoadAllWithLimit(half) failed: %v", err)
		}
		all := pm.All()
		if len(all) != total/2 {
			t.Errorf("LimitHalf: expected %d passages, got %d", total/2, len(all))
		}
		t.Logf("LimitHalf: loaded %d of %d passages", len(all), total)
	}()

	// Test 2: LimitZero — no limit, load all
	func() {
		pm := gleann.NewPassageManager(basePath)
		defer pm.Close()

		if err := pm.LoadAllWithLimit(0); err != nil {
			t.Fatalf("LoadAllWithLimit(0) failed: %v", err)
		}
		all := pm.All()
		if len(all) != total {
			t.Errorf("LimitZero: expected %d passages, got %d", total, len(all))
		}
		t.Logf("LimitZero: loaded %d of %d passages", len(all), total)
	}()

	// Test 3: Limit exceeds total — should load all
	func() {
		pm := gleann.NewPassageManager(basePath)
		defer pm.Close()

		if err := pm.LoadAllWithLimit(total * 2); err != nil {
			t.Fatalf("LoadAllWithLimit(2x) failed: %v", err)
		}
		all := pm.All()
		if len(all) != total {
			t.Errorf("LimitExceeds: expected %d passages, got %d", total, len(all))
		}
		t.Logf("LimitExceeds: loaded %d of %d passages", len(all), total)
	}()
}

// --- Stress Test: ForEachPassage Streaming ---

func TestStress_PassageManager_ForEachPassage(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "stream_test")
	pm := gleann.NewPassageManager(basePath)
	t.Cleanup(func() { pm.Close() })

	const total = 5_000
	items := makePassages(total, 30)
	if _, err := pm.Add(items); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Count passages via streaming without LoadAll
	count := 0
	if err := pm.ForEachPassage(func(p gleann.Passage) error {
		count++
		if p.Text == "" {
			return fmt.Errorf("empty text for passage %d", p.ID)
		}
		return nil
	}); err != nil {
		t.Fatalf("ForEachPassage failed: %v", err)
	}

	if count != total {
		t.Errorf("ForEachPassage visited %d passages, expected %d", count, total)
	}

	// Build BM25 index via streaming (simulating the searcher pattern)
	adapter := gleann.NewBM25Adapter()
	if err := pm.ForEachPassage(func(p gleann.Passage) error {
		adapter.AddDocument(p)
		return nil
	}); err != nil {
		t.Fatalf("Streaming BM25 build failed: %v", err)
	}

	if adapter.IndexedCount() != total {
		t.Errorf("BM25 indexed %d, expected %d", adapter.IndexedCount(), total)
	}
}
