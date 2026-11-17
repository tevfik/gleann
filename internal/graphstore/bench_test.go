package graphstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/store"
)

// BenchmarkCallersDepth3 measures callers to depth 3 on the RFC 003 section
// 1.1 scale fixture: 200K symbols and 1M CALLS edges, to compare with the
// spike's recursive-CTE p50 of 388 µs (and the CSR's 8.6 µs, which this
// design deliberately does not build). Run with -bench; the fixture build
// takes a while and is excluded from the timed loop.
//
// Measured on the review machine (E-cores 18,19 pinned, nice 19, modernc
// v1.59.0, Go 1.25.9): ~1.8 ms/op. The spike's 388 µs ran on unrestricted
// P-cores with v1.62.0; RFC 9.14 asks for a re-run on the declared toolchain
// before that number is used as a budget. EXPLAIN QUERY PLAN confirms the
// walk is index-only (covering graph_edges_dst lookups + symbols primary
// key). The fixture is loaded with ReplaceAll.
//
//	GOTOOLCHAIN=go1.25.9 taskset -c 18,19 nice -n 19 go test -p 1 -run '^$' \
//	  -bench BenchmarkCallersDepth3 -benchtime 200x ./internal/graphstore/
func BenchmarkCallersDepth3(b *testing.B) {
	path := filepath.Join(b.TempDir(), "idx", "idx.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		b.Fatalf("store.Open: %v", err)
	}
	b.Cleanup(func() { s.Close() })
	g := New(s)
	fx := NewGraphFixture(200_000, 1_000_000, 2_000)

	start := time.Now()
	ctx := context.Background()
	if err := g.ReplaceAll(ctx, fx.Deltas()); err != nil {
		b.Fatalf("loading fixture: %v", err)
	}
	b.Logf("fixture built in %s (%d symbols, %d edges)", time.Since(start).Round(time.Millisecond), fx.NSyms, len(fx.Edges))

	// Roots with callers, so every timed query does real traversal work.
	rootRows, err := g.reader.QueryContext(ctx, `SELECT n.fqn FROM graph_nodes n WHERE EXISTS (SELECT 1 FROM graph_edges e WHERE e.dst = n.id) ORDER BY n.fqn LIMIT 64`)
	if err != nil {
		b.Fatalf("pick roots: %v", err)
	}
	var roots []string
	for rootRows.Next() {
		var r string
		if err := rootRows.Scan(&r); err != nil {
			b.Fatal(err)
		}
		roots = append(roots, r)
	}
	rootRows.Close()
	if len(roots) == 0 {
		b.Fatal("no roots with callers")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Callers(roots[i%len(roots)], 3); err != nil {
			b.Fatalf("Callers: %v", err)
		}
	}
}
