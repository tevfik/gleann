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
// walk is index-only (covering edges_dst lookups + symbols primary key).
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
	tx, err := g.writer.BeginTx(ctx, nil)
	if err != nil {
		b.Fatalf("begin fixture transaction: %v", err)
	}
	symIns, err := tx.PrepareContext(ctx, `INSERT INTO symbols
(fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc)
VALUES (?, ?, 'function', ?, ?, ?, 1, ?, 0, 1.0, '')`)
	if err != nil {
		b.Fatalf("prepare symbol insert: %v", err)
	}
	for i, fqn := range fx.FQNs {
		line := fx.Lines[i]
		if _, err := symIns.ExecContext(ctx, fqn, fx.Names[i], fx.Files[i], line, line+5, line*40); err != nil {
			b.Fatalf("insert symbol %d: %v", i, err)
		}
	}
	symIns.Close()
	edgeIns, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO edges (src, dst, kind, file, dst_external, confidence)
VALUES (?, ?, 'CALLS', ?, 0, 'extracted')`)
	if err != nil {
		b.Fatalf("prepare edge insert: %v", err)
	}
	for _, e := range fx.Edges {
		if _, err := edgeIns.ExecContext(ctx, e.Src, e.Dst, e.File); err != nil {
			b.Fatalf("insert edge %q -> %q: %v", e.Src, e.Dst, err)
		}
	}
	edgeIns.Close()
	if err := tx.Commit(); err != nil {
		b.Fatalf("commit fixture: %v", err)
	}
	b.Logf("fixture built in %s (%d symbols, %d edges)", time.Since(start).Round(time.Millisecond), fx.NSyms, len(fx.Edges))

	// Roots with callers, so every timed query does real traversal work.
	rootRows, err := g.reader.QueryContext(ctx, `SELECT dst FROM edges GROUP BY dst ORDER BY dst LIMIT 64`)
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
