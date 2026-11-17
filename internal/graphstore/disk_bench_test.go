package graphstore

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/store"
)

// TestDiskReport measures the SQLite graphstore alone (no Kùzu): on-disk size
// of the .db/-wal/-shm files, per-object sizes via the dbstat virtual table
// before and after an explicit PRAGMA wal_checkpoint(TRUNCATE), freelist
// count, and load time of ReplaceAll on the synthetic fixture. The default
// It only runs when GLEANN_GRAPH_BENCH_EDGES is set, on pinned cores:
//
//	GOTOOLCHAIN=go1.25.9 GLEANN_GRAPH_BENCH_EDGES=1000000 \
//	  taskset -c 4-19 nice -n 19 go test -run '^TestDiskReport$' -v \
//	  -timeout 60m ./internal/graphstore/
//
// GLEANN_GRAPH_BENCH_QUERIES (default 200) and GLEANN_GRAPH_BENCH_PATHS
// (default 20) size the query-latency phase, which reports p50/p95 for
// callers/callees/impact/search/path on the same random FQN sample the Kùzu
// parity harness uses.
func TestDiskReport(t *testing.T) {
	if os.Getenv("GLEANN_GRAPH_BENCH_EDGES") == "" {
		t.Skip("set GLEANN_GRAPH_BENCH_EDGES to run the disk + load report")
	}
	nEdges := diskBenchEdges(t)
	nQueries := diskBenchEnvInt(t, "GLEANN_GRAPH_BENCH_QUERIES", 200)
	nPaths := diskBenchEnvInt(t, "GLEANN_GRAPH_BENCH_PATHS", 20)
	nSyms := max(1, nEdges/5)
	nFiles := max(1, nEdges/500)
	fx := NewGraphFixture(nSyms, nEdges, nFiles)
	ctx := context.Background()

	fmt.Printf("\n# SQLite graphstore disk + load report — %s\n", time.Now().Format(time.RFC3339))
	fmt.Printf("# size %d edges requested · %d stored · %d symbols · %d files\n",
		nEdges, len(fx.Edges), fx.NSyms, fx.NFiles)

	dir := t.TempDir()
	path := filepath.Join(dir, "idx", "idx.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	g := New(s)

	start := time.Now()
	if err := g.ReplaceAll(ctx, fx.Deltas()); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	load := time.Since(start)
	fmt.Printf("\n**Load** ReplaceAll: %s\n", load.Round(time.Millisecond))

	var pageSize, freelist int64
	if err := s.WriterDB().QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatalf("page_size: %v", err)
	}
	if err := s.WriterDB().QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freelist); err != nil {
		t.Fatalf("freelist_count: %v", err)
	}
	fmt.Printf("**Pragmas** page_size=%d freelist_count=%d\n", pageSize, freelist)

	// Before / after an explicit checkpoint, per file and per dbstat object.
	diskReport(t, ctx, s, dir, "after load (ReplaceAll does VACUUM+checkpoint(TRUNCATE))")
	if _, err := s.WriterDB().ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("wal_checkpoint(TRUNCATE): %v", err)
	}
	diskReport(t, ctx, s, dir, "after explicit wal_checkpoint(TRUNCATE)")

	// ── Query latency p50/p95 over the same random sample as the Kùzu harness ──
	rng := rand.New(rand.NewSource(2027))
	sampleIdx := make([]int, 0, nQueries)
	seen := make(map[int]bool, nQueries)
	for len(sampleIdx) < nQueries {
		i := rng.Intn(fx.NSyms)
		if !seen[i] {
			seen[i] = true
			sampleIdx = append(sampleIdx, i)
		}
	}
	queries := make([]string, nQueries)
	for j, i := range sampleIdx {
		queries[j] = fx.FQNs[i]
	}
	pairs := make([][2]string, 0, nPaths)
	for len(pairs) < nPaths {
		a, b := rng.Intn(fx.NSyms), rng.Intn(fx.NSyms)
		if a == b {
			continue
		}
		pairs = append(pairs, [2]string{fx.FQNs[a], fx.FQNs[b]})
	}
	for _, q := range queries {
		_, _ = g.Callers(q, 3)
		_, _ = g.Callees(q, 3)
		_, _ = g.Impact(q, 3)
		_, _ = g.Symbols(q)
		_, _ = g.Path(pairs[0][0], pairs[0][1])
	}
	lat := func(fn func(q string) error) []time.Duration {
		ds := make([]time.Duration, 0, nQueries)
		for _, q := range queries {
			st := time.Now()
			if err := fn(q); err != nil {
				t.Fatalf("query %q: %v", q, err)
			}
			ds = append(ds, time.Since(st))
		}
		return ds
	}
	fmt.Printf("\n**Query latency p50/p95 (µs), N=%d queries**\n", nQueries)
	fmt.Printf("\n| query | p50 | p95 |\n|---|---:|---:|\n")
	for _, o := range []struct {
		name string
		fn   func(q string) error
	}{
		{"callers d3", func(q string) error { _, err := g.Callers(q, 3); return err }},
		{"callees d3", func(q string) error { _, err := g.Callees(q, 3); return err }},
		{"impact d3", func(q string) error { _, err := g.Impact(q, 3); return err }},
		{"search", func(q string) error { _, err := g.Symbols(q); return err }},
	} {
		p50, p95 := diskP50P95(lat(o.fn))
		fmt.Printf("| %s | %s | %s |\n", o.name, p50, p95)
	}
	{
		ds := make([]time.Duration, 0, nPaths)
		for _, p := range pairs {
			st := time.Now()
			if _, err := g.Path(p[0], p[1]); err != nil {
				t.Fatalf("path %q -> %q: %v", p[0], p[1], err)
			}
			ds = append(ds, time.Since(st))
		}
		p50, p95 := diskP50P95(ds)
		fmt.Printf("| path (N=%d pairs) | %s | %s |\n", len(pairs), p50, p95)
	}
}

// diskReport prints the .db/-wal/-shm sizes and the dbstat per-object sizes.
func diskReport(t *testing.T, ctx context.Context, s *store.Store, dir, label string) {
	t.Helper()
	var dbSize, walSize, shmSize int64
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		switch {
		case strings.HasSuffix(p, "-wal"):
			walSize = info.Size()
		case strings.HasSuffix(p, "-shm"):
			shmSize = info.Size()
		case filepath.Ext(p) == ".db":
			dbSize = info.Size()
		}
		return nil
	})
	fmt.Printf("\n**Disk — %s**\n", label)
	fmt.Printf("\n| file | bytes | MiB |\n|---|---:|---:|\n")
	fmt.Printf("| .db | %d | %.2f |\n", dbSize, float64(dbSize)/(1<<20))
	fmt.Printf("| -wal | %d | %.2f |\n", walSize, float64(walSize)/(1<<20))
	fmt.Printf("| -shm | %d | %.2f |\n", shmSize, float64(shmSize)/(1<<20))
	fmt.Printf("| total | %d | %.2f |\n", dbSize+walSize+shmSize, float64(dbSize+walSize+shmSize)/(1<<20))

	rows, err := s.ReaderDB().QueryContext(ctx, `SELECT name, sum(pgsize) FROM dbstat GROUP BY name ORDER BY 2 DESC`)
	if err != nil {
		t.Fatalf("dbstat: %v", err)
	}
	defer rows.Close()
	type obj struct {
		name string
		size int64
	}
	var objs []obj
	for rows.Next() {
		var o obj
		if err := rows.Scan(&o.name, &o.size); err != nil {
			t.Fatalf("dbstat scan: %v", err)
		}
		objs = append(objs, o)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("dbstat rows: %v", err)
	}
	fmt.Printf("\n| dbstat object | bytes | MiB |\n|---|---:|---:|\n")
	var sum int64
	for _, o := range objs {
		fmt.Printf("| %s | %d | %.2f |\n", o.name, o.size, float64(o.size)/(1<<20))
		sum += o.size
	}
	fmt.Printf("| dbstat sum | %d | %.2f |\n", sum, float64(sum)/(1<<20))
}

func diskBenchEdges(t testing.TB) int {
	t.Helper()
	raw := os.Getenv("GLEANN_GRAPH_BENCH_EDGES")
	if raw == "" {
		return 10000
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("GLEANN_GRAPH_BENCH_EDGES: %q is not a positive integer", raw)
	}
	return n
}

func diskBenchEnvInt(t testing.TB, name string, def int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("%s: %q is not a positive integer", name, raw)
	}
	return n
}

func diskP50P95(ds []time.Duration) (string, string) {
	if len(ds) == 0 {
		return "-", "-"
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return fmt.Sprintf("%.0f", float64(s[len(s)*50/100])/1000),
		fmt.Sprintf("%.0f", float64(s[len(s)*95/100])/1000)
}
