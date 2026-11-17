package store

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/analysis"
)

// benchCombo represents one lexical configuration to benchmark.
type benchCombo struct {
	name string
	opts LexQueryOptions
}

var testCombos = []benchCombo{
	{name: "OR (today)", opts: LexQueryOptions{ANDFirst: false, DocFreqCut: 0}},
	{name: "AND-first", opts: LexQueryOptions{ANDFirst: true, DocFreqCut: 0}},
	{name: "AND-first + df cut 0.05", opts: LexQueryOptions{ANDFirst: true, DocFreqCut: 0.05}},
	{name: "AND-first + df cut 0.1", opts: LexQueryOptions{ANDFirst: true, DocFreqCut: 0.1}},
	{name: "AND-first + df cut 0.2", opts: LexQueryOptions{ANDFirst: true, DocFreqCut: 0.2}},
}

// buildSyntheticCorpus returns the cached path to a benchmark database with n passages.
func getOrBuildSyntheticStore(tb testing.TB, n int) (*Store, func()) {
	tb.Helper()
	dbPath := filepath.Join(os.TempDir(), fmt.Sprintf("gleann-lexbench-%d.db", n))

	// Check if cached database exists and has expected rows.
	if fi, err := os.Stat(dbPath); err == nil && fi.Size() > 0 {
		s, err := Open(dbPath, Options{})
		if err == nil {
			ctx := context.Background()
			cnt, err := s.Count(ctx)
			if err == nil && cnt == n {
				stats, err := s.Stats(ctx)
				if err == nil && stats.Rows == n {
					tb.Logf("Using cached synthetic benchmark DB at %s (N=%d)", dbPath, n)
					return s, func() { s.Close() }
				}
			}
			s.Close()
		}
	}

	tb.Logf("Building synthetic benchmark DB for N=%d at %s (this may take a moment)...", n, dbPath)
	tmpPath := dbPath + ".tmp"
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(tmpPath + suffix)
		_ = os.Remove(dbPath + suffix)
	}

	s, err := Open(tmpPath, Options{})
	if err != nil {
		tb.Fatalf("Open tmp store for bench build: %v", err)
	}

	rnd := rand.New(rand.NewSource(1337))
	commonWords := []string{
		"data", "value", "string", "error", "context", "request", "response",
		"result", "type", "node", "index", "build", "test", "file", "name",
		"code", "line", "time", "user", "path", "func", "struct", "table",
		"query", "match", "term", "call", "read", "write", "token", "item",
		"key", "field", "size", "count", "list", "info", "block", "state",
		"task", "server", "client", "http", "header", "status", "event",
		"point", "level", "order", "start",
	}

	mediumWords := make([]string, 500)
	for i := range mediumWords {
		mediumWords[i] = fmt.Sprintf("module_term_%03d", i)
	}

	rareWords := make([]string, 5000)
	for i := range rareWords {
		rareWords[i] = fmt.Sprintf("rare_symbol_%04d", i)
	}

	codeIdentifiers := []string{
		"handleRequestValidator", "buildLexicalSidecar", "parseHTTPResponseHeader",
		"formatPassagePID", "verifyMerkleTreeHash", "dispatchWorkerTask",
		"compactVectorSlotIndex", "resolveASTCallGraph", "computeCrossEntropyScore",
		"benchmarkDiskIOThroughput",
	}

	analyzer := analysis.New("")
	batchSize := 5000
	ctx := context.Background()

	startBuild := time.Now()
	for start := 0; start < n; start += batchSize {
		end := min(start+batchSize, n)
		tx, err := s.writer.BeginTx(ctx, nil)
		if err != nil {
			tb.Fatalf("begin batch tx at %d: %v", start, err)
		}

		insPassage, err := tx.PrepareContext(ctx, `INSERT INTO passages (id, pid, source, chunk_no, kind, text) VALUES (?, ?, ?, ?, 'code', ?)`)
		if err != nil {
			tx.Rollback()
			tb.Fatalf("prepare insert passage: %v", err)
		}
		insLex, err := tx.PrepareContext(ctx, `INSERT INTO lex (rowid, tokens) VALUES (?, ?)`)
		if err != nil {
			insPassage.Close()
			tx.Rollback()
			tb.Fatalf("prepare insert lex: %v", err)
		}

		for i := start; i < end; i++ {
			id := int64(i + 1)
			pid := fmt.Sprintf("pid_%08d", id)
			src := fmt.Sprintf("src/pkg_%d/file_%d.go", (i/50)%100, i%50)

			var words []string
			// 10-15 common words
			numCommon := 10 + rnd.Intn(6)
			for c := 0; c < numCommon; c++ {
				words = append(words, commonWords[rnd.Intn(len(commonWords))])
			}
			// 8-12 medium words
			numMed := 8 + rnd.Intn(5)
			for m := 0; m < numMed; m++ {
				words = append(words, mediumWords[rnd.Intn(len(mediumWords))])
			}
			// 1-3 rare words
			numRare := 1 + rnd.Intn(3)
			for r := 0; r < numRare; r++ {
				words = append(words, rareWords[rnd.Intn(len(rareWords))])
			}
			// Code identifier
			words = append(words, codeIdentifiers[rnd.Intn(len(codeIdentifiers))])

			text := strings.Join(words, " ")
			tokens := strings.Join(analyzer.Tokens(text, analysis.KindCode), " ")

			if _, err := insPassage.ExecContext(ctx, id, pid, src, i%10, text); err != nil {
				insPassage.Close()
				insLex.Close()
				tx.Rollback()
				tb.Fatalf("insert passage %d: %v", id, err)
			}
			if _, err := insLex.ExecContext(ctx, id, tokens); err != nil {
				insPassage.Close()
				insLex.Close()
				tx.Rollback()
				tb.Fatalf("insert lex %d: %v", id, err)
			}
		}

		insPassage.Close()
		insLex.Close()
		if err := tx.Commit(); err != nil {
			tb.Fatalf("commit batch tx at %d: %v", start, err)
		}
	}

	tb.Logf("Inserted %d passages in %v. Checkpointing...", n, time.Since(startBuild))
	var cp1, cp2 int
	_ = s.writer.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&cp1, &cp2)
	s.Close()

	if err := os.Rename(tmpPath, dbPath); err != nil {
		tb.Fatalf("rename tmp db to cached db: %v", err)
	}

	cachedStore, err := Open(dbPath, Options{})
	if err != nil {
		tb.Fatalf("opening newly created cached db: %v", err)
	}
	return cachedStore, func() { cachedStore.Close() }
}

// generateBenchQueries returns 200 deterministic queries mixing rare and common terms.
func generateBenchQueries() []string {
	rnd := rand.New(rand.NewSource(2026))

	commonTerms := []string{
		"data", "value", "string", "error", "context", "request", "response",
		"result", "type", "node", "index", "build", "test", "file", "name",
		"code", "line", "time", "user", "path", "query", "match", "token",
	}

	mediumTerms := []string{
		"module_term_010", "module_term_025", "module_term_050",
		"module_term_075", "module_term_100", "module_term_150",
	}

	rareTerms := []string{
		"rare_symbol_0042", "rare_symbol_0100", "rare_symbol_0250",
		"rare_symbol_0500", "rare_symbol_0999", "rare_symbol_1500",
		"rare_symbol_2500", "rare_symbol_3500", "rare_symbol_4500",
	}

	codeTerms := []string{
		"handleRequestValidator", "buildLexicalSidecar", "parseHTTPResponseHeader",
		"formatPassagePID", "verifyMerkleTreeHash", "dispatchWorkerTask",
	}

	var queries []string
	// 50 common + rare
	for i := 0; i < 50; i++ {
		c := commonTerms[rnd.Intn(len(commonTerms))]
		r := rareTerms[rnd.Intn(len(rareTerms))]
		queries = append(queries, c+" "+r)
	}
	// 40 common + code identifier
	for i := 0; i < 40; i++ {
		c := commonTerms[rnd.Intn(len(commonTerms))]
		ci := codeTerms[rnd.Intn(len(codeTerms))]
		queries = append(queries, c+" "+ci)
	}
	// 30 common + medium
	for i := 0; i < 30; i++ {
		c := commonTerms[rnd.Intn(len(commonTerms))]
		m := mediumTerms[rnd.Intn(len(mediumTerms))]
		queries = append(queries, c+" "+m)
	}
	// 30 rare + rare
	for i := 0; i < 30; i++ {
		r1 := rareTerms[rnd.Intn(len(rareTerms))]
		r2 := rareTerms[rnd.Intn(len(rareTerms))]
		queries = append(queries, r1+" "+r2)
	}
	// 20 common + common
	for i := 0; i < 20; i++ {
		c1 := commonTerms[rnd.Intn(len(commonTerms))]
		c2 := commonTerms[rnd.Intn(len(commonTerms))]
		queries = append(queries, c1+" "+c2)
	}
	// 30 common + medium + rare
	for i := 0; i < 30; i++ {
		c := commonTerms[rnd.Intn(len(commonTerms))]
		m := mediumTerms[rnd.Intn(len(mediumTerms))]
		r := rareTerms[rnd.Intn(len(rareTerms))]
		queries = append(queries, c+" "+m+" "+r)
	}

	return queries[:200]
}

// PercentileResults holds measured p50, p95, p99.
type PercentileResults struct {
	P50 time.Duration
	P95 time.Duration
	P99 time.Duration
}

func measureCombo(ctx context.Context, s *Store, queries []string, an analysis.Analyzer, opts LexQueryOptions) PercentileResults {
	// Warm up cache
	for i := 0; i < 5; i++ {
		_, _ = s.Search(ctx, LexQuery{Text: queries[i], Analyzer: an, Options: opts}, 10)
	}

	durations := make([]time.Duration, len(queries))
	for i, q := range queries {
		start := time.Now()
		_, _ = s.Search(ctx, LexQuery{Text: q, Analyzer: an, Options: opts}, 10)
		durations[i] = time.Since(start)
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50 := durations[int(float64(len(durations))*0.50)]
	p95 := durations[int(float64(len(durations))*0.95)]
	p99 := durations[int(float64(len(durations))*0.99)]

	return PercentileResults{P50: p50, P95: p95, P99: p99}
}

func BenchmarkLexicalSearch(b *testing.B) {
	ctx := context.Background()
	queries := generateBenchQueries()
	an := analysis.New("")

	for _, n := range []int{100_000, 1_000_000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			s, cleanup := getOrBuildSyntheticStore(b, n)
			defer cleanup()

			fmt.Printf("\n=== Latency Benchmark: N = %d passages (over %d queries) ===\n", n, len(queries))
			fmt.Printf("| %-28s | %10s | %10s | %10s |\n", "Strategy", "p50", "p95", "p99")
			fmt.Printf("|------------------------------|------------|------------|------------|\n")

			for _, combo := range testCombos {
				b.Run(combo.name, func(b *testing.B) {
					// Measure 200 queries once and print table row
					res := measureCombo(ctx, s, queries, an, combo.opts)
					fmt.Printf("| %-28s | %10v | %10v | %10v |\n",
						combo.name, res.P50.Round(time.Microsecond), res.P95.Round(time.Microsecond), res.P99.Round(time.Microsecond))

					// Standard Go benchmark loop for benchmark metrics
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						q := queries[i%len(queries)]
						_, _ = s.Search(ctx, LexQuery{Text: q, Analyzer: an, Options: combo.opts}, 10)
					}
				})
			}
			fmt.Println()
		})
	}
}
