package benchmarks

import (
	"math/rand"
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
