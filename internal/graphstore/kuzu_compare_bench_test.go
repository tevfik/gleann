//go:build treesitter

// TestKuzuCompareBench benchmarks the Kùzu code graph (internal/graph/kuzu)
// against the SQLite graphstore (this package) on the SAME synthetic graph
// (GraphFixture, the generator of bench_test.go), so the maintainer can
// decide whether Kùzu is removed (RFC 003, docs/plans/003-storage.md:
// section 3.7 recursive CTEs vs section 6.2 Kùzu).
//
// It measures, per size:
//   - load time: Kùzu the way internal/graph/indexer does it (wipe in a
//     transaction, then CSV COPY FROM per table via kuzu/writer.go), and
//     graphstore via ReplaceAll (one bulk transaction: all symbols then all
//     edges in index order, internal targets resolved against the complete
//     symbol set, so a fresh build needs no dirty-file convergence pass);
//   - query latency p50/p95 over the same random FQN sample: callers depth 1
//     and 3, callees depth 3, impact depth 3, shortest path, symbol search
//     (substring), stats;
//   - incremental update: re-apply one file's delta renaming one symbol in
//     both, then consistency: no orphan edges, inbound edges from unchanged
//     files handled, counts equal;
//   - result-set parity per sampled query (Kùzu vs graphstore, as sets of
//     FQNs) with mismatch counts and examples;
//   - on-disk database size and process peak RSS (VmHWM, linux only).
//
// The Kùzu multi-hop queries are answered the way the package itself answers
// them: kuzu.Callers/Callees are single-hop, so depth 3 is a BFS in Go over
// single-hop queries, exactly like kuzu.Impact and kuzu.Neighbors do; the
// depth-3 impact uses kuzu.Impact directly.
//
// Run the small validation size (default; the orchestrator runs the larger
// sizes later on pinned cores, so this worktree only ever builds 10K):
//
//	GOTOOLCHAIN=go1.25.9 go test -tags treesitter \
//	  -run '^TestKuzuCompareBench$' -v ./internal/graphstore/
//
// Full sizes (one invocation per size, or comma-separated for one run):
//
//	GOTOOLCHAIN=go1.25.9 GLEANN_GRAPH_BENCH_EDGES=10000,100000,1000000 \
//	  taskset -c 18,19 nice -n 19 go test -tags treesitter \
//	  -run '^TestKuzuCompareBench$' -v -timeout 60m ./internal/graphstore/
//
// Optional env: GLEANN_GRAPH_BENCH_QUERIES (FQN sample per op, default 200),
// GLEANN_GRAPH_BENCH_PATHS (shortest-path pairs, default 20).
package graphstore_test

import (
	"context"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestKuzuCompareBench(t *testing.T) {
	sizes := benchSizes(t)
	nQueries := benchEnvInt(t, "GLEANN_GRAPH_BENCH_QUERIES", 200)
	nPaths := benchEnvInt(t, "GLEANN_GRAPH_BENCH_PATHS", 20)

	fmt.Printf("\n# Kùzu vs SQLite graphstore — %s\n", time.Now().Format(time.RFC3339))
	fmt.Printf("# go %s %s/%s, cpus=%d\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	fmt.Printf("# command: GOTOOLCHAIN=go1.25.9 GLEANN_GRAPH_BENCH_EDGES=%s go test -tags treesitter -run '^TestKuzuCompareBench$' -v ./internal/graphstore/\n",
		os.Getenv("GLEANN_GRAPH_BENCH_EDGES"))

	for _, size := range sizes {
		runKuzuCompare(t, size, nQueries, nPaths)
	}
}

// benchSizes parses GLEANN_GRAPH_BENCH_EDGES: comma-separated edge counts;
// default 10000 (the small validation size).
func benchSizes(t *testing.T) []int {
	t.Helper()
	raw := os.Getenv("GLEANN_GRAPH_BENCH_EDGES")
	if raw == "" {
		raw = "10000"
	}
	var sizes []int
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			t.Fatalf("GLEANN_GRAPH_BENCH_EDGES: %q is not a positive integer", part)
		}
		sizes = append(sizes, n)
	}
	if len(sizes) == 0 {
		t.Fatal("GLEANN_GRAPH_BENCH_EDGES is empty")
	}
	return sizes
}

func benchEnvInt(t *testing.T, name string, def int) int {
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

// runKuzuCompare runs the full comparison for one edge size and prints the
// markdown section. t.Errorf marks genuine parity/consistency bugs (the test
// keeps running and still prints the table); documented semantic differences
// (Kùzu ShortestPath's 1000-explored-nodes cap, SymbolSearch's missing
// ORDER BY) are reported in the table without failing.
func runKuzuCompare(t *testing.T, nEdges, nQueries, nPaths int) {
	ctx := context.Background()
	nSyms := max(1, nEdges/5)    // 200K symbols per 1M edges, as bench_test.go
	nFiles := max(1, nEdges/500) // 2K files per 1M edges, as bench_test.go
	fx := graphstore.NewGraphFixture(nSyms, nEdges, nFiles)

	fmt.Printf("\n### Size %d edges requested · %d stored · %d symbols · %d files\n",
		nEdges, len(fx.Edges), fx.NSyms, fx.NFiles)

	rss := newRSSPoints()
	rss.record("start")

	// ── SQLite graphstore load (one bulk ReplaceAll transaction) ──
	gsDir := filepath.Join(t.TempDir(), "sqlite")
	gs, gsStore, err := openGraphStore(gsDir)
	if err != nil {
		t.Fatalf("open graphstore: %v", err)
	}
	defer gsStore.Close()

	deltas := fx.Deltas()
	gsLoadStart := time.Now()
	if err := gs.ReplaceAll(ctx, deltas); err != nil {
		t.Fatalf("graphstore bulk load: %v", err)
	}
	gsLoad := time.Since(gsLoadStart)
	// The bulk path resolves every internal target against the complete symbol
	// set, so a fresh build converges in one pass with no dirty files.
	passes, dirtyFiles := 1, 0
	if dirty, err := gs.DirtyFiles(); err != nil {
		t.Fatalf("DirtyFiles: %v", err)
	} else if len(dirty) != 0 {
		t.Fatalf("bulk load left %d dirty file(s); want 0 (no convergence pass)", len(dirty))
	}
	rss.record("after graphstore load")

	gsStats, err := gs.Stats()
	if err != nil {
		t.Fatalf("graphstore Stats: %v", err)
	}

	// ── Kùzu load: wipe in a tx, then CSV COPY FROM per table (indexer path) ──
	kDir := filepath.Join(t.TempDir(), "g_graph")
	kdb, err := kgraph.Open(kDir)
	if err != nil {
		t.Fatalf("kuzu open: %v", err)
	}
	defer kdb.Close()

	files := make([]kgraph.FileNode, fx.NFiles)
	for f := 0; f < fx.NFiles; f++ {
		files[f] = kgraph.FileNode{Path: fmt.Sprintf("bench/f%d.go", f), Lang: "go"}
	}
	syms := make([]kgraph.SymbolNode, fx.NSyms)
	for i, fqn := range fx.FQNs {
		syms[i] = kgraph.SymbolNode{FQN: fqn, Kind: "function", File: fx.Files[i], Line: fx.Lines[i], Name: fx.Names[i], Weight: 1.0}
	}
	decls := make([]kgraph.EdgeDeclares, fx.NSyms)
	for i, fqn := range fx.FQNs {
		decls[i] = kgraph.EdgeDeclares{FilePath: fx.Files[i], SymbolFQN: fqn}
	}
	calls := make([]kgraph.EdgeCalls, len(fx.Edges))
	for i, e := range fx.Edges {
		calls[i] = kgraph.EdgeCalls{CallerFQN: e.Src, CalleeFQN: e.Dst, Confidence: "extracted"}
	}

	kuzuLoadStart := time.Now()
	if err := kgraph.ExecTxOn(kdb.Conn(), kgraph.DeleteAllCodeData()); err != nil {
		t.Fatalf("kuzu load wipe: %v", err)
	}
	csvBytes := int64(0)
	for _, c := range []struct {
		table string
		write func(p string) error
	}{
		{"CodeFile", func(p string) error { return kgraph.WriteFileNodesCSV(p, files) }},
		{"Symbol", func(p string) error { return kgraph.WriteSymbolNodesCSV(p, syms) }},
		{"DECLARES", func(p string) error { return kgraph.WriteDeclaresCSV(p, decls) }},
		{"CALLS", func(p string) error { return kgraph.WriteCallsCSV(p, calls) }},
	} {
		n, err := writeCopyCSV(kdb, c.table, c.write)
		if err != nil {
			t.Fatalf("kuzu load %s: %v", c.table, err)
		}
		csvBytes += n
	}
	kuzuLoad := time.Since(kuzuLoadStart)
	rss.record("after kuzu load")

	kzStats, err := kdb.Stats()
	if err != nil {
		t.Fatalf("kuzu Stats: %v", err)
	}

	// The loads must have produced the same graph.
	if gsStats.Files != kzStats.Files || gsStats.Symbols != kzStats.Symbols ||
		gsStats.CallEdges != kzStats.CallEdges || gsStats.DeclareEdges != kzStats.DeclareEdges ||
		gsStats.ImplementsEdges != kzStats.ImplementsEdges {
		t.Errorf("stats mismatch after load: graphstore %+v vs kuzu %+v", gsStats, kzStats)
	}
	if gsStats.Symbols != fx.NSyms {
		t.Errorf("symbols = %d, want %d", gsStats.Symbols, fx.NSyms)
	}
	if gsStats.CallEdges != len(fx.Edges) {
		t.Errorf("CALLS edges = %d, want %d", gsStats.CallEdges, len(fx.Edges))
	}
	if gsStats.Files != fx.NFiles {
		t.Errorf("files = %d, want %d", gsStats.Files, fx.NFiles)
	}

	fmt.Printf("\n**Load** (both build the identical unique edge set; graphstore needs %d apply pass(es), last re-applied %d dirty file(s))\n", passes, dirtyFiles)
	fmt.Printf("\n| backend | load time | detail |\n|---|---:|---|\n")
	fmt.Printf("| kuzu (CSV COPY FROM) | %s | wipe tx + 4 COPYs, %.2f MB CSV |\n",
		ms(kuzuLoad), float64(csvBytes)/(1<<20))
	fmt.Printf("| graphstore (ReplaceAll) | %s | %d file deltas, %d pass(es) |\n",
		ms(gsLoad), len(deltas), passes)
	fmt.Printf("| stats: files/symbols/CALLS | %d / %d / %d | equal on both |\n",
		gsStats.Files, gsStats.Symbols, gsStats.CallEdges)

	// ── Query sample: the same random FQNs for both stores ──
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

	// Warmup both stores once per op so one-time page/cache effects are not
	// part of the timed distribution.
	for _, q := range queries {
		_, _ = kuzuCallersSet(kdb, q, 3)
		_, _ = gs.Callers(q, 3)
		_, _ = kuzuCalleesSet(kdb, q, 3)
		_, _ = gs.Callees(q, 3)
		_, _ = kuzuImpactSet(kdb, q, 3)
		_, _ = gs.Impact(q, 3)
		_, _ = kuzuSearchSet(kdb, q)
		_, _ = gs.Symbols(q)
		_, _ = kdb.ShortestPath(pairs[0][0], pairs[0][1])
		_, _ = gs.Path(pairs[0][0], pairs[0][1])
	}
	_, _ = kdb.Stats()
	_, _ = gs.Stats()

	// ── Latency + parity ──
	ops := []struct {
		name string
		kuzu func(string) (resultSet, error)
		gs   func(string) (resultSet, error)
	}{
		{"callers d1", func(q string) (resultSet, error) { return kuzuCallersSet(kdb, q, 1) }, func(q string) (resultSet, error) { return gsCallersSet(gs, q, 1) }},
		{"callers d3", func(q string) (resultSet, error) { return kuzuCallersSet(kdb, q, 3) }, func(q string) (resultSet, error) { return gsCallersSet(gs, q, 3) }},
		{"callees d3", func(q string) (resultSet, error) { return kuzuCalleesSet(kdb, q, 3) }, func(q string) (resultSet, error) { return gsCalleesSet(gs, q, 3) }},
		{"impact d3", func(q string) (resultSet, error) { return kuzuImpactSet(kdb, q, 3) }, func(q string) (resultSet, error) { return gsImpactSet(gs, q, 3) }},
		{"search", func(q string) (resultSet, error) { return kuzuSearchSet(kdb, q) }, func(q string) (resultSet, error) { return gsSearchSet(gs, q) }},
	}
	reports := make(map[string]opReport, len(ops))
	for _, op := range ops {
		reports[op.name] = measureAndCompare(t, op.name, queries, op.kuzu, op.gs)
	}
	pathRep := measureAndComparePath(t, pairs, kdb, gs)

	statsStart := time.Now()
	kzStats, err = kdb.Stats()
	kzStatsLat := time.Since(statsStart)
	if err != nil {
		t.Fatalf("kuzu Stats (timed): %v", err)
	}
	statsStart = time.Now()
	gsStats, err = gs.Stats()
	gsStatsLat := time.Since(statsStart)
	if err != nil {
		t.Fatalf("graphstore Stats (timed): %v", err)
	}
	if gsStats.CallEdges != kzStats.CallEdges || gsStats.Symbols != kzStats.Symbols {
		t.Errorf("stats drifted between load and query phase: graphstore %+v vs kuzu %+v", gsStats, kzStats)
	}
	rss.record("after queries")

	fmt.Printf("\n**Query latency p50/p95 (µs), N=%d queries**\n", nQueries)
	fmt.Printf("\n| query | kuzu p50 | kuzu p95 | graphstore p50 | graphstore p95 |\n|---|---:|---:|---:|---:|\n")
	for _, op := range ops {
		r := reports[op.name]
		k50, k95 := p50p95(r.kuzuLat)
		g50, g95 := p50p95(r.gsLat)
		fmt.Printf("| %s | %s | %s | %s | %s |\n", op.name, k50, k95, g50, g95)
	}
	r := pathRep
	k50, k95 := p50p95(r.kuzuLat)
	g50, g95 := p50p95(r.gsLat)
	fmt.Printf("| shortest path (N=%d pairs) | %s | %s | %s | %s |\n", len(pairs), k50, k95, g50, g95)
	fmt.Printf("| stats (1x) | %s | — | %s | — |\n", ms(kzStatsLat), ms(gsStatsLat))

	fmt.Printf("\n**Result-set parity** (same queries, sets of FQNs; graphstore is the reference)\n")
	fmt.Printf("\n| query | mismatches | notes |\n|---|---:|---|\n")
	for _, op := range ops {
		or := reports[op.name]
		notes := ""
		if or.capped > 0 {
			notes = fmt.Sprintf("%d query(s) had >50 matches: both stores cap at 50 and kuzu SymbolSearch has no ORDER BY, so the capped subsets are arbitrary and need not agree", or.capped)
			for _, ex := range or.examples {
				notes += " · " + ex
			}
		}
		fmt.Printf("| %s | %d | %s |\n", op.name, or.mismatch, notes)
	}
	fmt.Printf("| shortest path | %d | kuzu caps exploration at 1000 nodes, graphstore at depth 10: see examples |\n", pathRep.mismatch)
	if pathRep.routeNote > 0 {
		fmt.Printf("| shortest path route note | — | %d pair(s) found a path of the same length in both but a different route (both valid shortest paths; tie-breaking differs) |\n", pathRep.routeNote)
	}
	for _, op := range ops {
		or := reports[op.name]
		if or.mismatch > 0 {
			fmt.Printf("- %s mismatches (%d):\n", op.name, or.mismatch)
			for _, ex := range or.examples {
				fmt.Printf("  - %s\n", ex)
			}
		}
	}
	if pathRep.mismatch > 0 {
		fmt.Printf("- shortest path mismatches (%d):\n", pathRep.mismatch)
		for _, ex := range pathRep.examples {
			fmt.Printf("  - %s\n", ex)
		}
	}
	for _, op := range ops {
		if reports[op.name].mismatch > 0 {
			t.Errorf("%s: %d result-set mismatch(es)", op.name, reports[op.name].mismatch)
		}
	}
	if pathRep.mismatch > 0 {
		// Existence mismatches are the documented cap difference (kuzu 1000
		// explored nodes vs graphstore depth 10); a length mismatch when both
		// find a path would be a real bug.
		t.Logf("shortest path: %d existence/length difference(s) — expected kuzu 1000-node cap vs graphstore depth-10 cap", pathRep.mismatch)
	}

	// ── Incremental update: rename one symbol in one file ──
	fqnToIdx := make(map[string]int, fx.NSyms)
	for i, f := range fx.FQNs {
		fqnToIdx[f] = i
	}
	inboundOther := make([]int, fx.NSyms)
	outDeg := make([]int, fx.NSyms)
	for _, e := range fx.Edges {
		outDeg[fqnToIdx[e.Src]]++
		if e.File != fx.Files[fqnToIdx[e.Dst]] {
			inboundOther[fqnToIdx[e.Dst]]++
		}
	}
	best := -1
	for i := 0; i < fx.NSyms; i++ {
		if inboundOther[i] > 0 && outDeg[i] > 0 {
			if best == -1 || inboundOther[i]+outDeg[i] > inboundOther[best]+outDeg[best] {
				best = i
			}
		}
	}
	if best == -1 {
		for i := 0; i < fx.NSyms; i++ {
			if inboundOther[i] > 0 {
				best = i
			}
		}
	}
	if best == -1 {
		best = 0
	}
	oldFQN, newFQN := fx.FQNs[best], fx.FQNs[best]+"_r"
	renFile := fx.Files[best]
	delta := renameDelta(fx, best, newFQN)

	// A surviving inbound edge (another file → a symbol of renFile that is not
	// renamed) for the preserved-edge check.
	var surviveSrc, surviveDst, surviveFile string
	for _, e := range fx.Edges {
		if e.File != renFile && e.Dst != oldFQN && fx.Files[fqnToIdx[e.Dst]] == renFile {
			surviveSrc, surviveDst, surviveFile = e.Src, e.Dst, e.File
			break
		}
	}
	dirtyOwners := map[string]bool{}
	for _, e := range fx.Edges {
		if e.Dst == oldFQN && e.File != renFile {
			dirtyOwners[e.File] = true
		}
	}
	droppedInbound := inboundOther[best]

	gUpdStart := time.Now()
	if err := gs.ApplyFileDelta(ctx, delta); err != nil {
		t.Fatalf("graphstore incremental rename: %v", err)
	}
	gsUpd := time.Since(gUpdStart)

	kUpdStart := time.Now()
	if err := kuzuRenameFile(t, kdb, fx, renFile, best, newFQN); err != nil {
		t.Fatalf("kuzu incremental rename: %v", err)
	}
	kuzuUpd := time.Since(kUpdStart)
	rss.record("after incremental update")

	// Consistency checks.
	gsStatsAfter, err := gs.Stats()
	if err != nil {
		t.Fatalf("graphstore Stats after update: %v", err)
	}
	kzStatsAfter, err := kdb.Stats()
	if err != nil {
		t.Fatalf("kuzu Stats after update: %v", err)
	}
	var gsOrphans, gsDirty, gsOldEdges, gsSurvive int
	if err := gsStore.ReaderDB().QueryRowContext(ctx, `SELECT count(*) FROM edges e
WHERE NOT EXISTS (SELECT 1 FROM symbols s WHERE s.fqn = e.src)
   OR (e.dst_external = 0 AND NOT EXISTS (SELECT 1 FROM symbols s WHERE s.fqn = e.dst))`).Scan(&gsOrphans); err != nil {
		t.Fatalf("orphan check: %v", err)
	}
	if err := gsStore.ReaderDB().QueryRowContext(ctx, `SELECT count(*) FROM files WHERE graph_dirty = 1`).Scan(&gsDirty); err != nil {
		t.Fatalf("dirty check: %v", err)
	}
	if err := gsStore.ReaderDB().QueryRowContext(ctx, `SELECT count(*) FROM edges WHERE src = ? OR dst = ?`, oldFQN, oldFQN).Scan(&gsOldEdges); err != nil {
		t.Fatalf("old-fqn edge check: %v", err)
	}
	if surviveSrc != "" {
		if err := gsStore.ReaderDB().QueryRowContext(ctx, `SELECT count(*) FROM edges WHERE src = ? AND dst = ? AND file = ?`,
			surviveSrc, surviveDst, surviveFile).Scan(&gsSurvive); err != nil {
			t.Fatalf("surviving inbound check: %v", err)
		}
	}
	kOldSyms := cypherCount(t, kdb, fmt.Sprintf(`MATCH (s:Symbol {fqn: %q}) RETURN count(s) AS c`, oldFQN))
	kOldEdges := cypherCount(t, kdb, fmt.Sprintf(`MATCH (a:Symbol)-[r:CALLS]->(b:Symbol) WHERE a.fqn = %q OR b.fqn = %q RETURN count(r) AS c`, oldFQN, oldFQN))
	kSurvive := int64(-1)
	if surviveSrc != "" {
		kSurvive = cypherCount(t, kdb, fmt.Sprintf(`MATCH (a:Symbol {fqn: %q})-[r:CALLS]->(b:Symbol {fqn: %q}) RETURN count(r) AS c`, surviveSrc, surviveDst))
	}
	expectedCallEdges := gsStats.CallEdges - droppedInbound

	fmt.Printf("\n**Incremental rename** `%s` → `%s` in `%s` (%d inbound edge(s) from other files dropped)\n",
		oldFQN, newFQN, renFile, droppedInbound)
	fmt.Printf("\n| check | kuzu | graphstore |\n|---|---:|---:|\n")
	fmt.Printf("| update latency | %s | %s |\n", ms(kuzuUpd), ms(gsUpd))
	fmt.Printf("| CALLS edges before → after | %d → %d | %d → %d |\n", kzStats.CallEdges, kzStatsAfter.CallEdges, gsStats.CallEdges, gsStatsAfter.CallEdges)
	fmt.Printf("| orphan edges | %d (FK-enforced: COPY requires both endpoints) | %d |\n", kOldEdges, gsOrphans)
	fmt.Printf("| rows touching the old FQN | %d symbol(s), %d edge(s) | %d edge(s) |\n", kOldSyms, kOldEdges, gsOldEdges)
	fmt.Printf("| graph_dirty files | n/a (no dirty flag in kuzu) | %d (want %d owners of dropped inbound) |\n", gsDirty, len(dirtyOwners))
	fmt.Printf("| surviving inbound preserved | %s | %s |\n", countStr(kSurvive), countStr(int64(gsSurvive)))

	if gsStatsAfter.CallEdges != expectedCallEdges || kzStatsAfter.CallEdges != expectedCallEdges {
		t.Errorf("CALLS after rename: graphstore %d, kuzu %d, want %d (= before %d − %d dropped inbound)",
			gsStatsAfter.CallEdges, kzStatsAfter.CallEdges, expectedCallEdges, gsStats.CallEdges, droppedInbound)
	}
	if gsStatsAfter.CallEdges != kzStatsAfter.CallEdges {
		t.Errorf("CALLS counts diverged after rename: graphstore %d vs kuzu %d", gsStatsAfter.CallEdges, kzStatsAfter.CallEdges)
	}
	if gsStatsAfter.Symbols != kzStatsAfter.Symbols || gsStatsAfter.Symbols != fx.NSyms {
		t.Errorf("symbols after rename: graphstore %d, kuzu %d, want %d", gsStatsAfter.Symbols, kzStatsAfter.Symbols, fx.NSyms)
	}
	if gsOrphans != 0 {
		t.Errorf("graphstore has %d orphan edge(s) after rename", gsOrphans)
	}
	if gsOldEdges != 0 {
		t.Errorf("graphstore has %d edge(s) still touching the renamed FQN", gsOldEdges)
	}
	if kOldSyms != 0 || kOldEdges != 0 {
		t.Errorf("kuzu still has %d symbol(s) / %d edge(s) touching the renamed FQN", kOldSyms, kOldEdges)
	}
	if gsDirty != len(dirtyOwners) {
		t.Errorf("graph_dirty files = %d, want %d (owners of dropped inbound edges)", gsDirty, len(dirtyOwners))
	}
	if surviveSrc != "" {
		if gsSurvive != 1 || kSurvive != 1 {
			t.Errorf("surviving inbound edge %q→%q: graphstore %d, kuzu %d, want 1", surviveSrc, surviveDst, gsSurvive, kSurvive)
		}
	} else {
		t.Log("no inbound edge to a non-renamed symbol of the file found; surviving-inbound check skipped")
	}

	// ── Storage ──
	gsDisk := dirSize(gsDir)
	kDisk := dirSize(kDir)
	fmt.Printf("\n**Storage** (measured while both stores are open; kuzu includes WAL)\n")
	fmt.Printf("\n| backend | disk (MiB) |\n|---|---:|\n")
	fmt.Printf("| kuzu (%s) | %.2f |\n", kDir, float64(kDisk)/(1<<20))
	fmt.Printf("| graphstore (%s) | %.2f |\n", gsDir, float64(gsDisk)/(1<<20))
	rss.print()
}

// ── load helpers ───────────────────────────────────────────────────────────

func openGraphStore(dir string) (*graphstore.SQLite, *store.Store, error) {
	path := filepath.Join(dir, "idx", "idx.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		return nil, nil, err
	}
	return graphstore.New(s), s, nil
}

// writeCopyCSV writes one table's CSV to a temp file and COPYs it in, the
// way internal/graph/indexer.doCopy does; returns the CSV bytes written.
func writeCopyCSV(kdb *kgraph.DB, table string, write func(p string) error) (int64, error) {
	tmp, err := os.CreateTemp("", "kuzu_"+table+"_*.csv")
	if err != nil {
		return 0, err
	}
	csvPath := tmp.Name()
	tmp.Close()
	defer os.Remove(csvPath)
	if err := write(csvPath); err != nil {
		return 0, err
	}
	info, err := os.Stat(csvPath)
	if err != nil {
		return 0, err
	}
	if err := kgraph.ExecCopyCSV(kdb.Conn(), table, csvPath); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// ── query wrappers ─────────────────────────────────────────────────────────

// resultSet is the FQN result of one query compared across stores. syms is
// the symbol set; files is used by impact (affected files).
type resultSet struct {
	syms  map[string]bool
	files map[string]bool
}

func newResultSet() resultSet {
	return resultSet{syms: map[string]bool{}, files: map[string]bool{}}
}

// kuzuCallersSet answers callers to depth using the same BFS-in-Go over the
// single-hop kuzu.Callers that kuzu.Impact uses (the package has no
// multi-hop callers query). The root FQN is excluded, as graphstore does.
func kuzuCallersSet(db *kgraph.DB, fqn string, depth int) (resultSet, error) {
	seen := map[string]bool{fqn: true}
	frontier := []string{fqn}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, n := range frontier {
			callers, err := db.Callers(n)
			if err != nil {
				return resultSet{}, err
			}
			for _, c := range callers {
				if !seen[c.FQN] {
					seen[c.FQN] = true
					next = append(next, c.FQN)
				}
			}
		}
		frontier = next
	}
	delete(seen, fqn)
	return resultSet{syms: seen}, nil
}

// kuzuCalleesSet is the forward twin over kuzu.Callees.
func kuzuCalleesSet(db *kgraph.DB, fqn string, depth int) (resultSet, error) {
	seen := map[string]bool{fqn: true}
	frontier := []string{fqn}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, n := range frontier {
			callees, err := db.Callees(n)
			if err != nil {
				return resultSet{}, err
			}
			for _, c := range callees {
				if !seen[c.FQN] {
					seen[c.FQN] = true
					next = append(next, c.FQN)
				}
			}
		}
		frontier = next
	}
	delete(seen, fqn)
	return resultSet{syms: seen}, nil
}

// kuzuImpactSet flattens kuzu.Impact's caller buckets and affected files
// into a resultSet; the root FQN is excluded.
func kuzuImpactSet(db *kgraph.DB, fqn string, depth int) (resultSet, error) {
	r, err := db.Impact(fqn, depth)
	if err != nil {
		return resultSet{}, err
	}
	out := newResultSet()
	for _, s := range append(append(r.DirectCallers, r.TransitiveCallers...), r.TestCallers...) {
		if s != fqn {
			out.syms[s] = true
		}
	}
	for _, f := range r.AffectedFiles {
		out.files[f] = true
	}
	return out, nil
}

func kuzuSearchSet(db *kgraph.DB, q string) (resultSet, error) {
	res, err := db.SymbolSearch(q)
	if err != nil {
		return resultSet{}, err
	}
	out := newResultSet()
	for _, c := range res {
		out.syms[c.FQN] = true
	}
	return out, nil
}

func gsCallersSet(gs *graphstore.SQLite, fqn string, depth int) (resultSet, error) {
	h, err := gs.Callers(fqn, depth)
	if err != nil {
		return resultSet{}, err
	}
	out := newResultSet()
	for _, x := range h {
		out.syms[x.FQN] = true
	}
	return out, nil
}

func gsCalleesSet(gs *graphstore.SQLite, fqn string, depth int) (resultSet, error) {
	h, err := gs.Callees(fqn, depth)
	if err != nil {
		return resultSet{}, err
	}
	out := newResultSet()
	for _, x := range h {
		out.syms[x.FQN] = true
	}
	return out, nil
}

func gsImpactSet(gs *graphstore.SQLite, fqn string, depth int) (resultSet, error) {
	r, err := gs.Impact(fqn, depth)
	if err != nil {
		return resultSet{}, err
	}
	out := newResultSet()
	for _, s := range append(append(r.DirectCallers, r.TransitiveCallers...), r.TestCallers...) {
		if s != fqn {
			out.syms[s] = true
		}
	}
	for _, f := range r.AffectedFiles {
		out.files[f] = true
	}
	return out, nil
}

func gsSearchSet(gs *graphstore.SQLite, q string) (resultSet, error) {
	res, err := gs.Symbols(q)
	if err != nil {
		return resultSet{}, err
	}
	out := newResultSet()
	for _, s := range res {
		out.syms[s.FQN] = true
	}
	return out, nil
}

// ── measurement and comparison ────────────────────────────────────────────

// opReport is one query op measured on both stores plus its parity outcome.
// capped counts search queries where >50 symbols matched: both stores cap at
// 50 and kuzu's SymbolSearch has no ORDER BY, so the two capped subsets are
// arbitrary and are reported, not counted as mismatches.
type opReport struct {
	kuzuLat, gsLat []time.Duration
	mismatch       int
	capped         int
	examples       []string
}

func measureAndCompare(t *testing.T, name string, queries []string, kuzuFn, gsFn func(string) (resultSet, error)) opReport {
	t.Helper()
	var r opReport
	for _, q := range queries {
		start := time.Now()
		kr, kerr := kuzuFn(q)
		r.kuzuLat = append(r.kuzuLat, time.Since(start))
		start = time.Now()
		gr, gerr := gsFn(q)
		r.gsLat = append(r.gsLat, time.Since(start))
		if (kerr != nil) != (gerr != nil) {
			r.mismatch++
			r.examples = append(r.examples, fmt.Sprintf("query %q: kuzu err=%v, graphstore err=%v", q, kerr, gerr))
			continue
		}
		if kerr != nil {
			continue // both errored identically
		}
		if setEq(kr.syms, gr.syms) && setEq(kr.files, gr.files) {
			continue
		}
		if name == "search" && (len(kr.syms) >= 50 || len(gr.syms) >= 50) {
			r.capped++
			if len(r.examples) < 5 {
				r.examples = append(r.examples, fmt.Sprintf("query %q: %d matches, both capped at 50 without a shared order; kuzu-only=%v graphstore-only=%v",
					q, len(kr.syms), setDiff(kr.syms, gr.syms), setDiff(gr.syms, kr.syms)))
			}
			continue
		}
		r.mismatch++
		if len(r.examples) < 5 {
			r.examples = append(r.examples, fmt.Sprintf("query %q: kuzu %d syms/%d files, graphstore %d syms/%d files; kuzu-only=%v graphstore-only=%v",
				q, len(kr.syms), len(kr.files), len(gr.syms), len(gr.files), setDiff(kr.syms, gr.syms), setDiff(gr.syms, kr.syms)))
		}
	}
	return r
}

// pathReport is the shortest-path op. mismatch counts pairs where one store
// found a path and the other did not (the documented cap difference: kuzu
// stops after 1000 explored nodes, graphstore after depth 10) or where both
// found a path of different length (a real bug). routeNote counts same-length
// paths through different intermediate symbols (both valid, tie-breaking
// differs).
type pathReport struct {
	kuzuLat, gsLat []time.Duration
	mismatch       int
	routeNote      int
	examples       []string
}

func measureAndComparePath(t *testing.T, pairs [][2]string, kdb *kgraph.DB, gs *graphstore.SQLite) pathReport {
	t.Helper()
	var r pathReport
	for _, p := range pairs {
		start := time.Now()
		ks, kerr := kdb.ShortestPath(p[0], p[1])
		r.kuzuLat = append(r.kuzuLat, time.Since(start))
		start = time.Now()
		gsSteps, gerr := gs.Path(p[0], p[1])
		r.gsLat = append(r.gsLat, time.Since(start))
		kFound, gFound := kerr == nil, gerr == nil
		if kFound != gFound {
			r.mismatch++
			if len(r.examples) < 5 {
				r.examples = append(r.examples, fmt.Sprintf("%q → %q: kuzu found=%v len=%d err=%v; graphstore found=%v len=%d err=%v",
					p[0], p[1], kFound, len(ks), kerr, gFound, len(gsSteps), gerr))
			}
			continue
		}
		if !kFound {
			continue
		}
		if len(ks) != len(gsSteps) {
			r.mismatch++
			if len(r.examples) < 5 {
				r.examples = append(r.examples, fmt.Sprintf("%q → %q: kuzu path length %d, graphstore %d",
					p[0], p[1], len(ks), len(gsSteps)))
			}
			continue
		}
		if !samePathFQNs(ks, gsSteps) {
			r.routeNote++
		}
	}
	return r
}

// ── incremental update helpers ─────────────────────────────────────────────

// renameDelta builds the FileDelta that renames symbol index k (FQN old) to
// newFQN in its file: the symbol row, every edge owned by the file that
// starts or ends at the old FQN, and nothing else — exactly what re-parsing
// the file after a rename would produce.
func renameDelta(fx *graphstore.GraphFixture, k int, newFQN string) graphstore.FileDelta {
	oldFQN, renFile := fx.FQNs[k], fx.Files[k]
	d := graphstore.FileDelta{File: renFile, GraphHash: "rename-v2"}
	for i, fqn := range fx.FQNs {
		if fx.Files[i] != renFile {
			continue
		}
		f, n := fqn, fx.Names[i]
		if fqn == oldFQN {
			f, n = newFQN, fx.Names[i]+"_r"
		}
		line := fx.Lines[i]
		d.Symbols = append(d.Symbols, graphstore.Symbol{
			FQN: f, Kind: "function", File: renFile, Name: n,
			StartLine: line, EndLine: line + 5, StartCol: 1, ByteOffset: line * 40,
			Weight: 1.0,
		})
	}
	for _, e := range fx.Edges {
		if e.File != renFile {
			continue
		}
		s, dst := e.Src, e.Dst
		if s == oldFQN {
			s = newFQN
		}
		if dst == oldFQN {
			dst = newFQN
		}
		d.Edges = append(d.Edges, graphstore.Edge{Src: s, Dst: dst, Kind: "CALLS", Confidence: "extracted"})
	}
	return d
}

// kuzuRenameFile mirrors internal/graph/indexer.IndexFiles for one file:
// capture incoming edges, RemoveFileSymbols, re-insert the file's nodes and
// edges (with the rename) via CSV COPY, then restore the incoming edges whose
// target symbol survived (the renamed symbol's are dropped).
func kuzuRenameFile(t *testing.T, kdb *kgraph.DB, fx *graphstore.GraphFixture, file string, k int, newFQN string) error {
	t.Helper()
	oldFQN := fx.FQNs[k]

	incoming, err := kdb.IncomingEdges([]string{file})
	if err != nil {
		return fmt.Errorf("IncomingEdges: %w", err)
	}
	if err := kdb.RemoveFileSymbols(file); err != nil {
		return fmt.Errorf("RemoveFileSymbols: %w", err)
	}

	kFile := kgraph.FileNode{Path: file, Lang: "go"}
	var kSyms []kgraph.SymbolNode
	var kDecls []kgraph.EdgeDeclares
	newSyms := make(map[string]bool, fx.NSyms/fx.NFiles+1)
	for i, fqn := range fx.FQNs {
		if fx.Files[i] != file {
			continue
		}
		f, n := fqn, fx.Names[i]
		if fqn == oldFQN {
			f, n = newFQN, fx.Names[i]+"_r"
		}
		kSyms = append(kSyms, kgraph.SymbolNode{FQN: f, Kind: "function", File: file, Line: fx.Lines[i], Name: n, Weight: 1.0})
		kDecls = append(kDecls, kgraph.EdgeDeclares{FilePath: file, SymbolFQN: f})
		newSyms[f] = true
	}
	var kCalls []kgraph.EdgeCalls
	for _, e := range fx.Edges {
		if e.File != file {
			continue
		}
		s, dst := e.Src, e.Dst
		if s == oldFQN {
			s = newFQN
		}
		if dst == oldFQN {
			dst = newFQN
		}
		kCalls = append(kCalls, kgraph.EdgeCalls{CallerFQN: s, CalleeFQN: dst, Confidence: "extracted"})
	}
	seenCalls := make(map[string]bool, len(kCalls))
	for _, c := range kCalls {
		seenCalls[c.CallerFQN+"->"+c.CalleeFQN] = true
	}
	for _, c := range incoming.Calls {
		key := c.CallerFQN + "->" + c.CalleeFQN
		if !seenCalls[key] && newSyms[c.CalleeFQN] {
			seenCalls[key] = true
			kCalls = append(kCalls, c)
		}
	}

	for _, c := range []struct {
		table string
		write func(p string) error
	}{
		{"CodeFile", func(p string) error { return kgraph.WriteFileNodesCSV(p, []kgraph.FileNode{kFile}) }},
		{"Symbol", func(p string) error { return kgraph.WriteSymbolNodesCSV(p, kSyms) }},
		{"DECLARES", func(p string) error { return kgraph.WriteDeclaresCSV(p, kDecls) }},
		{"CALLS", func(p string) error { return kgraph.WriteCallsCSV(p, kCalls) }},
	} {
		if _, err := writeCopyCSV(kdb, c.table, c.write); err != nil {
			return fmt.Errorf("reindex %s: %w", c.table, err)
		}
	}
	return nil
}

// ── small helpers ──────────────────────────────────────────────────────────

func p50p95(ds []time.Duration) (string, string) {
	if len(ds) == 0 {
		return "-", "-"
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return fmt.Sprintf("%.0f", float64(s[len(s)*50/100])/1000),
		fmt.Sprintf("%.0f", float64(s[len(s)*95/100])/1000)
}

func ms(d time.Duration) string { return d.Round(time.Millisecond).String() }

func setEq(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func setDiff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func samePathFQNs(kuzu []gleann.PathStep, gs []graphstore.Step) bool {
	set := func(steps []graphstore.Step) map[string]bool {
		m := map[string]bool{}
		for _, s := range steps {
			m[s.From] = true
			m[s.To] = true
		}
		return m
	}
	ku := make([]graphstore.Step, len(kuzu))
	for i, s := range kuzu {
		ku[i] = graphstore.Step{From: s.From, To: s.To, Relation: s.Relation}
	}
	return setEq(set(ku), set(gs))
}

func cypherCount(t *testing.T, db *kgraph.DB, cypher string) int64 {
	t.Helper()
	res, err := db.Conn().Query(cypher)
	if err != nil {
		t.Fatalf("cypher %q: %v", cypher, err)
	}
	defer res.Close()
	var n int64
	if res.HasNext() {
		row, err := res.Next()
		if err != nil {
			t.Fatalf("cypher %q: %v", cypher, err)
		}
		m, err := row.GetAsMap()
		if err != nil {
			t.Fatalf("cypher %q: %v", cypher, err)
		}
		switch v := m["c"].(type) {
		case int64:
			n = v
		case float64:
			n = int64(v)
		}
	}
	return n
}

func countStr(n int64) string {
	if n < 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d", n)
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// rssPoints records process VmHWM (linux) at named points. VmHWM is the
// high-water mark of resident memory, monotonic within the process; both
// stores are built in this process, so per-store attribution is approximate.
type rssPoints struct {
	points []struct {
		at string
		kb int64
	}
}

func newRSSPoints() *rssPoints {
	return &rssPoints{}
}

func (r *rssPoints) record(at string) {
	r.points = append(r.points, struct {
		at string
		kb int64
	}{at, vmHWMKB()})
}

func (r *rssPoints) print() {
	if len(r.points) == 0 || r.points[0].kb < 0 {
		fmt.Printf("\n| process peak RSS (VmHWM) | n/a (linux only) |\n")
		return
	}
	base := r.points[0].kb
	fmt.Printf("\n**Process peak RSS** (VmHWM, MiB; both stores live in this one process, deltas from start)\n")
	fmt.Printf("\n| point | VmHWM (MiB) | delta (MiB) |\n|---|---:|---:|\n")
	for _, p := range r.points {
		fmt.Printf("| %s | %.1f | %.1f |\n", p.at, float64(p.kb)/1024, float64(p.kb-base)/1024)
	}
}

func vmHWMKB() int64 {
	if runtime.GOOS != "linux" {
		return -1
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return kb
				}
			}
		}
	}
	return -1
}
