package graphstore_test

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

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

func openGraph(t *testing.T) (*graphstore.SQLite, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "idx", "idx.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return graphstore.New(s), s
}

// dumpGraph reads the meaningful graph contents (everything except the
// internal surrogate symbols.id) so two loads can be compared row for row.
func dumpGraph(t *testing.T, s *store.Store) (syms, edges, files []string, counters map[string]string) {
	t.Helper()
	db := s.ReaderDB()
	ctx := context.Background()
	q := func(query string) []string {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		var out []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan: %v", err)
			}
			parts := make([]string, len(cols))
			for i, v := range vals {
				parts[i] = fmt.Sprintf("%v", v)
			}
			out = append(out, strings.Join(parts, "|"))
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return out
	}
	syms = q(`SELECT fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc FROM symbols ORDER BY fqn`)
	edges = q(`SELECT src, dst, kind, file, dst_external, confidence FROM edges ORDER BY src, dst, kind, file`)
	files = q(`SELECT path, graph_hash, graph_dirty FROM files ORDER BY path`)
	counters = map[string]string{}
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM meta WHERE key LIKE 'graph_count:%' ORDER BY key`)
	if err != nil {
		t.Fatalf("counters: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("counter scan: %v", err)
		}
		counters[k] = v
	}
	return syms, edges, files, counters
}

func assertGraphsEqual(t *testing.T, label string, a, b *store.Store) {
	t.Helper()
	as, ae, af, ac := dumpGraph(t, a)
	bs, be, bf, bc := dumpGraph(t, b)
	if strings.Join(as, "\n") != strings.Join(bs, "\n") {
		t.Errorf("%s: symbols differ (bulk %d rows vs per-file %d rows)", label, len(as), len(bs))
	}
	if strings.Join(ae, "\n") != strings.Join(be, "\n") {
		t.Errorf("%s: edges differ (bulk %d rows vs per-file %d rows)\nbulk: %v\nper-file: %v", label, len(ae), len(be), ae, be)
	}
	if strings.Join(af, "\n") != strings.Join(bf, "\n") {
		t.Errorf("%s: files graph columns differ\nbulk: %v\nper-file: %v", label, af, bf)
	}
	if fmt.Sprint(ac) != fmt.Sprint(bc) {
		t.Errorf("%s: counters differ bulk=%v per-file=%v", label, ac, bc)
	}
}

// TestReplaceAllMatchesPerFile is the bulk-load parity contract: ReplaceAll on
// the shared fixture produces exactly the rows the per-file ApplyFileDelta path
// produces after the dirty-file convergence pass — same symbols, same edges,
// same manifest graph columns, same counters — and needs no convergence pass
// itself (zero dirty files).
func TestReplaceAllMatchesPerFile(t *testing.T) {
	fx := graphstore.NewGraphFixture(2000, 8000, 20)
	deltas := fx.Deltas()
	ctx := context.Background()

	gBulk, sBulk := openGraph(t)
	if err := gBulk.ReplaceAll(ctx, deltas); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	if dirty, err := gBulk.DirtyFiles(); err != nil || len(dirty) != 0 {
		t.Fatalf("bulk load dirty files = %v (err %v); want none", dirty, err)
	}

	gPer, sPer := openGraph(t)
	for _, d := range deltas {
		if err := gPer.ApplyFileDelta(ctx, d); err != nil {
			t.Fatalf("ApplyFileDelta %s: %v", d.File, err)
		}
	}
	byFile := map[string]graphstore.FileDelta{}
	for _, d := range deltas {
		byFile[d.File] = d
	}
	for pass := 0; pass < 10; pass++ {
		dirty, err := gPer.DirtyFiles()
		if err != nil {
			t.Fatalf("DirtyFiles: %v", err)
		}
		if len(dirty) == 0 {
			break
		}
		for _, f := range dirty {
			if err := gPer.ApplyFileDelta(ctx, byFile[f]); err != nil {
				t.Fatalf("converge %s: %v", f, err)
			}
		}
	}
	if dirty, _ := gPer.DirtyFiles(); len(dirty) != 0 {
		t.Fatalf("per-file load did not converge: %v", dirty)
	}

	assertGraphsEqual(t, "fixture", sBulk, sPer)
}

// TestReplaceAllUnresolvedAndExternal checks the bulk path applies the same
// resolution rules as the per-file path when targets are missing or external:
// an internal dst no delta declares is dropped and its owner marked dirty; an
// external dst is kept; an edge to a symbol declared in a LATER file is kept on
// the first pass (no dirty pass needed for it).
func TestReplaceAllUnresolvedAndExternal(t *testing.T) {
	ctx := context.Background()
	mk := func(file string, syms []string, edges []graphstore.Edge) graphstore.FileDelta {
		d := graphstore.FileDelta{File: file, GraphHash: "h-" + file}
		for _, s := range syms {
			d.Symbols = append(d.Symbols, graphstore.Symbol{FQN: s, Name: s, Kind: "function", File: file, StartLine: 1, EndLine: 2, Weight: 1})
		}
		d.Edges = edges
		return d
	}
	deltas := []graphstore.FileDelta{
		mk("a.go", []string{"A"}, []graphstore.Edge{
			{Src: "A", Dst: "B", Kind: "CALLS"},                          // resolved (B in later file)
			{Src: "A", Dst: "C", Kind: "CALLS"},                          // resolved (C in later file)
			{Src: "A", Dst: "GHOST", Kind: "CALLS"},                      // unresolved internal -> dropped, a dirty
			{Src: "A", Dst: "ext.Lib", Kind: "CALLS", DstExternal: true}, // external -> kept
		}),
		mk("b.go", []string{"B"}, []graphstore.Edge{{Src: "B", Dst: "A", Kind: "CALLS"}}),
		mk("c.go", []string{"C"}, []graphstore.Edge{{Src: "C", Dst: "B", Kind: "IMPLEMENTS"}}),
	}

	gBulk, sBulk := openGraph(t)
	if err := gBulk.ReplaceAll(ctx, deltas); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}

	// Per-file: apply in order, then converge until the edge set is stable.
	gPer, sPer := openGraph(t)
	for _, d := range deltas {
		if err := gPer.ApplyFileDelta(ctx, d); err != nil {
			t.Fatalf("ApplyFileDelta %s: %v", d.File, err)
		}
	}
	byFile := map[string]graphstore.FileDelta{}
	for _, d := range deltas {
		byFile[d.File] = d
	}
	for pass := 0; pass < 5; pass++ {
		dirty, _ := gPer.DirtyFiles()
		if len(dirty) == 0 {
			break
		}
		for _, f := range dirty {
			if err := gPer.ApplyFileDelta(ctx, byFile[f]); err != nil {
				t.Fatalf("converge %s: %v", f, err)
			}
		}
	}
	assertGraphsEqual(t, "unresolved/external", sBulk, sPer)

	// Spot-check the bulk result directly.
	st, err := gBulk.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.Symbols != 3 || st.CallEdges != 4 || st.ImplementsEdges != 1 {
		t.Fatalf("Stats = %+v; want 3 symbols, 4 CALLS (A->B,A->C,B->A,A->ext), 1 IMPLEMENTS", st)
	}
	dirty, _ := gBulk.DirtyFiles()
	if len(dirty) != 1 || dirty[0] != "a.go" {
		t.Fatalf("dirty = %v; want [a.go] (GHOST unresolved)", dirty)
	}
}

// TestReplaceAllRefusesBadSource checks the owner invariant is enforced on the
// bulk path too: an edge whose src is not declared in its owning file refuses
// the whole rebuild (and leaves the previous graph intact via rollback).
func TestReplaceAllRefusesBadSource(t *testing.T) {
	ctx := context.Background()
	g, _ := openGraph(t)
	good := []graphstore.FileDelta{{
		File: "a.go", GraphHash: "h",
		Symbols: []graphstore.Symbol{{FQN: "A", Name: "A", Kind: "function", File: "a.go", Weight: 1}},
	}}
	if err := g.ReplaceAll(ctx, good); err != nil {
		t.Fatalf("ReplaceAll good: %v", err)
	}
	bad := []graphstore.FileDelta{{
		File:    "a.go",
		Symbols: []graphstore.Symbol{{FQN: "A", Name: "A", Kind: "function", File: "a.go", Weight: 1}},
		Edges:   []graphstore.Edge{{Src: "GHOST", Dst: "A", Kind: "CALLS"}},
	}}
	if err := g.ReplaceAll(ctx, bad); err == nil {
		t.Fatalf("ReplaceAll with undeclared src succeeded; want error")
	}
	st, err := g.Stats()
	if err != nil || st.Symbols != 1 {
		t.Fatalf("after refused rebuild Stats = %+v (err %v); want the previous 1-symbol graph intact", st, err)
	}
}

// referenceBFS is a plain unidirectional BFS over the same traversal relation
// Path uses (CALLS forward + backward, IMPLEMENTS forward), capped at depth 10.
// It is the oracle for the bidirectional implementation's shortest distances.
func referenceBFS(edges []graphstore.Edge, from, to string, cap int) int {
	if from == to {
		return 0
	}
	fwd := map[string][]string{} // CALLS + IMPLEMENTS forward
	bwd := map[string][]string{} // CALLS backward
	for _, e := range edges {
		if e.DstExternal {
			// forward arm excludes external dst; backward CALLS arm keeps it
			if e.Kind == "CALLS" || e.Kind == "" {
				bwd[e.Dst] = append(bwd[e.Dst], e.Src)
			}
			continue
		}
		switch e.Kind {
		case "CALLS", "":
			fwd[e.Src] = append(fwd[e.Src], e.Dst)
			bwd[e.Dst] = append(bwd[e.Dst], e.Src)
		case "IMPLEMENTS":
			fwd[e.Src] = append(fwd[e.Src], e.Dst)
		}
	}
	dist := map[string]int{from: 0}
	front := []string{from}
	for d := 0; d < cap && len(front) > 0; d++ {
		var next []string
		for _, n := range front {
			for _, m := range fwd[n] {
				if _, ok := dist[m]; !ok {
					dist[m] = d + 1
					next = append(next, m)
				}
			}
			for _, m := range bwd[n] {
				if _, ok := dist[m]; !ok {
					dist[m] = d + 1
					next = append(next, m)
				}
			}
		}
		front = next
	}
	if d, ok := dist[to]; ok {
		return d
	}
	return -1
}

// TestPathShortestCrossCheck cross-checks the bidirectional BFS against a plain
// BFS oracle over many random graphs and pairs: the returned length must equal
// the true shortest distance (or both report no path within the cap), and every
// returned step must be a real traversal edge in the right direction.
func TestPathShortestCrossCheck(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 40; trial++ {
		n := 12 + rng.Intn(20)
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("p.S%d", i)
		}
		var edges []graphstore.Edge
		ne := n + rng.Intn(2*n)
		for i := 0; i < ne; i++ {
			s, d := rng.Intn(n), rng.Intn(n)
			kind := "CALLS"
			if rng.Intn(4) == 0 {
				kind = "IMPLEMENTS"
			}
			ext := rng.Intn(8) == 0
			edges = append(edges, graphstore.Edge{Src: names[s], Dst: names[d], Kind: kind, DstExternal: ext})
		}
		// The edges primary key is (src, dst, kind, file); INSERT OR IGNORE
		// keeps the FIRST draw of a duplicate key (including its dst_external
		// flag). Dedup the same way so the oracle models the stored graph.
		seenKey := map[string]bool{}
		var dedup []graphstore.Edge
		for _, e := range edges {
			k := e.Src + "|" + e.Dst + "|" + e.Kind
			if seenKey[k] {
				continue
			}
			seenKey[k] = true
			dedup = append(dedup, e)
		}
		edges = dedup
		d := graphstore.FileDelta{File: "f.go", GraphHash: "h"}
		for _, nm := range names {
			d.Symbols = append(d.Symbols, graphstore.Symbol{FQN: nm, Name: nm, Kind: "function", File: "f.go", Weight: 1})
		}
		d.Edges = edges
		g, _ := openGraph(t)
		if err := g.ReplaceAll(ctx, []graphstore.FileDelta{d}); err != nil {
			t.Fatalf("trial %d ReplaceAll: %v", trial, err)
		}
		// Build the valid-step lookup (forward CALLS/IMPLEMENTS non-external,
		// backward CALLS as CALLED_BY).
		valid := map[string]bool{}
		for _, e := range edges {
			if !e.DstExternal && (e.Kind == "CALLS" || e.Kind == "IMPLEMENTS") {
				valid[e.Src+"|"+e.Dst+"|"+e.Kind] = true
			}
			if e.Kind == "CALLS" {
				valid[e.Dst+"|"+e.Src+"|CALLED_BY"] = true
			}
		}
		for q := 0; q < 15; q++ {
			a, b := names[rng.Intn(n)], names[rng.Intn(n)]
			want := referenceBFS(edges, a, b, 10)
			steps, err := g.Path(a, b)
			if want < 0 {
				if err == nil {
					t.Errorf("trial %d Path(%s,%s): got %d steps, want no path", trial, a, b, len(steps))
				}
				continue
			}
			if err != nil {
				t.Errorf("trial %d Path(%s,%s): unexpected err %v (want %d)", trial, a, b, err, want)
				continue
			}
			if len(steps) != want {
				t.Errorf("trial %d Path(%s,%s): len %d, want shortest %d", trial, a, b, len(steps), want)
			}
			// Validate the chain: contiguous, correct endpoints, real edges.
			cur := a
			for _, st := range steps {
				if st.From != cur {
					t.Errorf("trial %d Path(%s,%s): broken chain at %s (want from %s)", trial, a, b, st.From, cur)
					break
				}
				if !valid[st.From+"|"+st.To+"|"+st.Relation] {
					t.Errorf("trial %d Path(%s,%s): invalid step %s->%s %s", trial, a, b, st.From, st.To, st.Relation)
				}
				cur = st.To
			}
			if len(steps) > 0 && cur != b {
				t.Errorf("trial %d Path(%s,%s): ends at %s, want %s", trial, a, b, cur, b)
			}
		}
	}
}

// TestPathDepthCap checks the cap boundary and the IMPLEMENTS asymmetry and
// external-source behaviour explicitly.
func TestPathDepthCap(t *testing.T) {
	ctx := context.Background()
	// Chain of 12 nodes: 11 hops end to end.
	var d graphstore.FileDelta
	d.File = "chain.go"
	d.GraphHash = "h"
	for i := 0; i < 12; i++ {
		fqn := fmt.Sprintf("c.N%d", i)
		d.Symbols = append(d.Symbols, graphstore.Symbol{FQN: fqn, Name: fqn, Kind: "function", File: "chain.go", Weight: 1})
		if i > 0 {
			d.Edges = append(d.Edges, graphstore.Edge{Src: fmt.Sprintf("c.N%d", i-1), Dst: fqn, Kind: "CALLS"})
		}
	}
	g, _ := openGraph(t)
	if err := g.ReplaceAll(ctx, []graphstore.FileDelta{d}); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	if steps, err := g.Path("c.N0", "c.N10"); err != nil || len(steps) != 10 {
		t.Fatalf("Path N0->N10 = %d steps, err %v; want 10 (at cap)", len(steps), err)
	}
	if _, err := g.Path("c.N0", "c.N11"); err == nil {
		t.Fatalf("Path N0->N11 (11 hops) succeeded; want no path within cap 10")
	}
	if steps, err := g.Path("c.N5", "c.N5"); err != nil || len(steps) != 0 {
		t.Fatalf("Path self = %d steps err %v; want empty", len(steps), err)
	}

	// IMPLEMENTS is forward-only; an external source reaches its caller.
	d2 := graphstore.FileDelta{File: "i.go", GraphHash: "h",
		Symbols: []graphstore.Symbol{
			{FQN: "Iface", Name: "Iface", Kind: "interface", File: "i.go", Weight: 1},
			{FQN: "Impl", Name: "Impl", Kind: "type", File: "i.go", Weight: 1},
			{FQN: "Caller", Name: "Caller", Kind: "function", File: "i.go", Weight: 1},
		},
		Edges: []graphstore.Edge{
			{Src: "Impl", Dst: "Iface", Kind: "IMPLEMENTS"},
			{Src: "Caller", Dst: "ext.Fn", Kind: "CALLS", DstExternal: true},
		},
	}
	g2, _ := openGraph(t)
	if err := g2.ReplaceAll(ctx, []graphstore.FileDelta{d2}); err != nil {
		t.Fatalf("ReplaceAll d2: %v", err)
	}
	if steps, err := g2.Path("Impl", "Iface"); err != nil || len(steps) != 1 || steps[0].Relation != "IMPLEMENTS" {
		t.Fatalf("Path Impl->Iface = %v err %v; want 1 IMPLEMENTS step", steps, err)
	}
	if _, err := g2.Path("Iface", "Impl"); err == nil {
		t.Fatalf("Path Iface->Impl succeeded; IMPLEMENTS must not walk backward")
	}
	if steps, err := g2.Path("ext.Fn", "Caller"); err != nil || len(steps) != 1 || steps[0].Relation != "CALLED_BY" {
		t.Fatalf("Path ext.Fn->Caller = %v err %v; want 1 CALLED_BY step", steps, err)
	}
}

// TestPathScalePerformance guards the bidirectional speedup at scale: on the
// 100K-edge fixture the old recursive-CTE Path took ~1.5s p50; the batched
// bidirectional BFS must answer in well under a second. (Fails on the old
// code, which is both slower and lacks ReplaceAll.)
func TestPathScalePerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("scale perf guard skipped in -short")
	}
	fx := graphstore.NewGraphFixture(20000, 100000, 200)
	g, _ := openGraph(t)
	ctx := context.Background()
	if err := g.ReplaceAll(ctx, fx.Deltas()); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	var lat []time.Duration
	for i := 0; i < 12; i++ {
		a := fx.FQNs[i*13%len(fx.FQNs)]
		b := fx.FQNs[(i*13+5000)%len(fx.FQNs)]
		st := time.Now()
		if _, err := g.Path(a, b); err != nil {
			t.Fatalf("Path: %v", err)
		}
		lat = append(lat, time.Since(st))
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	p50 := lat[len(lat)/2]
	if p50 > 750*time.Millisecond {
		t.Fatalf("Path p50 at 100K = %s; want < 750ms (bidirectional BFS)", p50)
	}
	t.Logf("Path p50 at 100K = %s", p50)
}

// TestSymbolsSubstringSemantics checks the search keeps case-insensitive
// substring semantics, the fqn ORDER BY and the LIMIT, for short queries,
// LIKE metacharacters and quotes — cross-checked against a brute-force scan.
func TestSymbolsSubstringSemantics(t *testing.T) {
	ctx := context.Background()
	names := []string{
		"pkg.Alpha", "pkg.alphabet", "pkg.Beta", "pkg.gamma", "pkg.Under_score",
		"pkg.Percent%Sign", "pkg.Quote\"Mark", "pkg.AB", "pkg.a", "pkg.Sym123",
		"other.Alpha", "pkg.AlPhA", "pkg.βeta", "pkg.longernamehere",
	}
	d := graphstore.FileDelta{File: "s.go", GraphHash: "h"}
	for _, nm := range names {
		short := nm[strings.LastIndex(nm, ".")+1:]
		d.Symbols = append(d.Symbols, graphstore.Symbol{FQN: nm, Name: short, Kind: "function", File: "s.go", Weight: 1})
	}
	g, s := openGraph(t)
	if err := g.ReplaceAll(ctx, []graphstore.FileDelta{d}); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	// Brute-force reference: case-insensitive substring on fqn OR name,
	// ordered by fqn, capped at 50.
	reference := func(q string) []string {
		lq := strings.ToLower(q)
		var hits []string
		for _, nm := range names {
			short := nm[strings.LastIndex(nm, ".")+1:]
			if q == "" || strings.Contains(strings.ToLower(nm), lq) || strings.Contains(strings.ToLower(short), lq) {
				hits = append(hits, nm)
			}
		}
		sort.Strings(hits)
		if len(hits) > 50 {
			hits = hits[:50]
		}
		return hits
	}
	queries := []string{"", "a", "AL", "alpha", "Alpha", "%", "_", "\"", "Sym", "123", "β", "pkg.A", "under", "SCORE", "xyz", "p"}
	for _, q := range queries {
		got, err := g.Symbols(q)
		if err != nil {
			t.Fatalf("Symbols(%q): %v", q, err)
		}
		var gotFQNs []string
		for _, sym := range got {
			gotFQNs = append(gotFQNs, sym.FQN)
		}
		want := reference(q)
		if strings.Join(gotFQNs, ",") != strings.Join(want, ",") {
			t.Errorf("Symbols(%q) = %v; want %v", q, gotFQNs, want)
		}
	}
	// The FTS index must exist and stay in sync after an incremental delta.
	var n int
	if err := s.ReaderDB().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='symbols_fts'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("symbols_fts missing (n=%d err=%v)", n, err)
	}
	if err := g.ApplyFileDelta(ctx, graphstore.FileDelta{File: "s2.go", GraphHash: "h2",
		Symbols: []graphstore.Symbol{{FQN: "new.FreshSymbol", Name: "FreshSymbol", Kind: "function", File: "s2.go", Weight: 1}}}); err != nil {
		t.Fatalf("ApplyFileDelta: %v", err)
	}
	got, err := g.Symbols("FreshSym")
	if err != nil || len(got) != 1 || got[0].FQN != "new.FreshSymbol" {
		t.Fatalf("Symbols(FreshSym) after delta = %v err %v; want new.FreshSymbol (FTS trigger sync)", got, err)
	}
}

// TestStatsCountersExact checks the meta counters stay exactly equal to the
// live row counts through inserts, retraction, removal and a bulk rebuild.
func TestStatsCountersExact(t *testing.T) {
	ctx := context.Background()
	g, s := openGraph(t)
	live := func() (syms, calls, impl int64) {
		db := s.ReaderDB()
		db.QueryRowContext(ctx, `SELECT count(*) FROM symbols`).Scan(&syms)
		db.QueryRowContext(ctx, `SELECT count(*) FROM edges WHERE kind='CALLS'`).Scan(&calls)
		db.QueryRowContext(ctx, `SELECT count(*) FROM edges WHERE kind='IMPLEMENTS'`).Scan(&impl)
		return
	}
	check := func(label string) {
		st, err := g.Stats()
		if err != nil {
			t.Fatalf("%s Stats: %v", label, err)
		}
		sy, ca, im := live()
		if int64(st.Symbols) != sy || int64(st.CallEdges) != ca || int64(st.ImplementsEdges) != im {
			t.Errorf("%s: Stats{%d syms,%d calls,%d impl} != live{%d,%d,%d}", label, st.Symbols, st.CallEdges, st.ImplementsEdges, sy, ca, im)
		}
	}
	d1 := graphstore.FileDelta{File: "a.go", GraphHash: "h",
		Symbols: []graphstore.Symbol{
			{FQN: "A", Name: "A", Kind: "function", File: "a.go", Weight: 1},
			{FQN: "B", Name: "B", Kind: "function", File: "a.go", Weight: 1},
		},
		Edges: []graphstore.Edge{{Src: "A", Dst: "B", Kind: "CALLS"}, {Src: "B", Dst: "A", Kind: "IMPLEMENTS"}},
	}
	if err := g.ApplyFileDelta(ctx, d1); err != nil {
		t.Fatalf("d1: %v", err)
	}
	check("after d1")
	// Re-apply with one symbol removed and an edge changed.
	d1b := graphstore.FileDelta{File: "a.go", GraphHash: "h2",
		Symbols: []graphstore.Symbol{{FQN: "A", Name: "A", Kind: "function", File: "a.go", Weight: 1}},
		Edges:   []graphstore.Edge{{Src: "A", Dst: "ext.X", Kind: "CALLS", DstExternal: true}},
	}
	if err := g.ApplyFileDelta(ctx, d1b); err != nil {
		t.Fatalf("d1b: %v", err)
	}
	check("after retract B")
	if err := g.RemoveFile(ctx, "a.go"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	check("after remove")
	fx := graphstore.NewGraphFixture(500, 2000, 5)
	if err := g.ReplaceAll(ctx, fx.Deltas()); err != nil {
		t.Fatalf("ReplaceAll: %v", err)
	}
	check("after bulk")
}

// A rebuild over an existing graph frees pages, so ReplaceAll VACUUMs. In WAL
// mode VACUUM writes the whole database through the WAL; the checkpoint must
// come after it or a database-sized WAL is left on disk (it was 181 MiB next
// to a 182 MiB database at 1M edges).
func TestReplaceAllLeavesNoWAL(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	g := graphstore.New(s)
	// A large graph replaced by a small one leaves free pages behind.
	for round, fx := range []*graphstore.GraphFixture{
		graphstore.NewGraphFixture(4000, 20000, 40),
		graphstore.NewGraphFixture(100, 400, 4),
	} {
		if err := g.ReplaceAll(ctx, fx.Deltas()); err != nil {
			t.Fatalf("ReplaceAll round %d: %v", round, err)
		}
		fi, err := os.Stat(path + "-wal")
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("stat wal: %v", err)
		}
		if err == nil && fi.Size() != 0 {
			t.Fatalf("round %d: WAL is %d bytes after ReplaceAll; want 0", round, fi.Size())
		}
	}
}
