package graphstore

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/store"
)

// openTestGraph opens a fresh format-2 store and its graph side in a temp
// directory, closing the store on cleanup.
func openTestGraph(t *testing.T) (*SQLite, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idx", "idx.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s), s
}

func mustApply(t *testing.T, g *SQLite, d FileDelta) {
	t.Helper()
	if err := g.ApplyFileDelta(context.Background(), d); err != nil {
		t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
	}
}

// mkSym builds a symbol with a concrete source anchor (AGENTS.md section
// 2.2: no phantom symbols).
func mkSym(fqn, file string, line int64) Symbol {
	return Symbol{
		FQN: fqn, Kind: "function", File: file, Name: fqn,
		StartLine: line, EndLine: line + 5, StartCol: 1, ByteOffset: line * 40,
	}
}

// bfsLevels is the reference breadth-first search the CTE answers must
// equal: shortest hop count from root over adj, capped at depth. The root is
// excluded except when reached directly (depth 1) via a self edge — the same
// rule as the CTE's "exclude the start node unless it is a direct self
// neighbour" (kuzu reports a self-recursive symbol as its own callee/caller
// but not through a longer cycle).
func bfsLevels(adj map[string][]string, root string, depth int) map[string]int {
	out := make(map[string]int)
	visited := make(map[string]bool)
	frontier := []string{root}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, n := range frontier {
			for _, m := range adj[n] {
				if m == root {
					// A direct self edge reports the root at depth 1; a longer
					// cycle revisits it at depth > 1 and must not.
					if d == 1 {
						out[m] = d
					}
					continue
				}
				if !visited[m] {
					visited[m] = true
					out[m] = d
					next = append(next, m)
				}
			}
		}
		frontier = next
	}
	return out
}

func hopLevels(hops []Hop) map[string]int {
	m := make(map[string]int, len(hops))
	for _, h := range hops {
		m[h.FQN] = h.Depth
	}
	return m
}

// randomGraph builds a random call graph including cycles and self-loops,
// split into per-file deltas. Every internal edge has both endpoints
// declared, so the deltas are valid; some edges are external. Returns the
// deltas and the reference adjacency (CALLS only, internal).
func randomGraph(seed int64, nFiles, nSyms, nEdges int) ([]FileDelta, map[string][]string, map[string][]string, []string) {
	rng := rand.New(rand.NewSource(seed))
	fqns := make([]string, nSyms)
	fileOf := make(map[string]string)
	symsByFile := make(map[string][]Symbol)
	for i := 0; i < nSyms; i++ {
		fqns[i] = fmt.Sprintf("pkg%d.Sym%d", i%nFiles, i)
		file := fmt.Sprintf("f%d.go", i%nFiles)
		fileOf[fqns[i]] = file
		s := mkSym(fqns[i], file, int64(i+1))
		s.IsTest = i%7 == 0 // some test symbols, to exercise Impact's split
		symsByFile[file] = append(symsByFile[file], s)
	}

	out := make(map[string][]string) // callers walk backwards over this
	in := make(map[string][]string)  // callees walk forwards over this
	edgesByFile := make(map[string][]Edge)
	seen := make(map[string]bool)
	for i := 0; i < nEdges; i++ {
		src := fqns[rng.Intn(nSyms)]
		dst := fqns[rng.Intn(nSyms)] // may equal src: self-loop; cycles emerge naturally
		owner := fileOf[src]
		key := src + "\x00" + dst + "\x00" + owner
		if seen[key] {
			continue
		}
		seen[key] = true
		edgesByFile[owner] = append(edgesByFile[owner], Edge{Src: src, Dst: dst, Kind: "CALLS"})
		in[src] = append(in[src], dst)
		out[dst] = append(out[dst], src)
	}
	// A few external references: they must never enter the symbol walks.
	for i := 0; i < nFiles; i++ {
		src := fqns[rng.Intn(nSyms)]
		owner := fileOf[src]
		edgesByFile[owner] = append(edgesByFile[owner], Edge{
			Src: src, Dst: fmt.Sprintf("ext.Lib%d", i), Kind: "CALLS", DstExternal: true,
		})
	}

	var deltas []FileDelta
	var files []string
	for f := range symsByFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		deltas = append(deltas, FileDelta{
			File: f, GraphHash: fmt.Sprintf("hash-%d-%s", seed, f),
			Symbols: symsByFile[f], Edges: edgesByFile[f],
		})
	}
	return deltas, out, in, fqns
}

// applyAll seeds every file's declarations first, then applies the full
// deltas. ApplyFileDelta refuses edges with undeclared internal endpoints
// (no dangling edges, AGENTS.md section 2.3), so cross-file references need
// the declarations to exist before the referencing delta commits — the same
// order a builder uses when it resolves references against the whole graph.
func applyAll(t *testing.T, g *SQLite, deltas []FileDelta) {
	t.Helper()
	for _, d := range deltas {
		mustApply(t, g, FileDelta{File: d.File, GraphHash: d.GraphHash, Symbols: d.Symbols})
	}
	for _, d := range deltas {
		mustApply(t, g, d)
	}
}

// TestCTEMatchesReferenceBFS is the RFC 003 section 7.1 property test: on
// random graphs including cycles, the recursive-CTE Callers and Callees
// answers equal a reference BFS, for several depths and fixed seeds.
func TestCTEMatchesReferenceBFS(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 42, 1337} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			g, _ := openTestGraph(t)
			deltas, out, in, fqns := randomGraph(seed, 6, 90, 300)
			applyAll(t, g, deltas)
			rng := rand.New(rand.NewSource(seed + 1000))
			for i := 0; i < 25; i++ {
				root := fqns[rng.Intn(len(fqns))]
				for depth := 1; depth <= 4; depth++ {
					gotCallers, err := g.Callers(root, depth)
					if err != nil {
						t.Fatalf("Callers(%s, %d): %v", root, depth, err)
					}
					want := bfsLevels(out, root, depth)
					if got := hopLevels(gotCallers); !reflect.DeepEqual(got, want) {
						t.Fatalf("Callers(%s, %d) = %v, want %v", root, depth, got, want)
					}
					gotCallees, err := g.Callees(root, depth)
					if err != nil {
						t.Fatalf("Callees(%s, %d): %v", root, depth, err)
					}
					want = bfsLevels(in, root, depth)
					if got := hopLevels(gotCallees); !reflect.DeepEqual(got, want) {
						t.Fatalf("Callees(%s, %d) = %v, want %v", root, depth, got, want)
					}
				}
			}
		})
	}
}

// TestImpactMatchesReferenceBFS checks the production-only expansion of
// Impact against a reference BFS that never expands test callers.
func TestImpactMatchesReferenceBFS(t *testing.T) {
	g, _ := openTestGraph(t)
	deltas, out, _, fqns := randomGraph(7, 5, 80, 260)
	applyAll(t, g, deltas)
	isTest := make(map[string]bool)
	fileOf := make(map[string]string)
	for _, d := range deltas {
		for _, s := range d.Symbols {
			isTest[s.FQN] = s.IsTest
			fileOf[s.FQN] = s.File
		}
	}

	// Reference: kuzu.Impact's BFS. Direct callers split test/production;
	// only production callers propagate; test callers are collected at any
	// level but never expanded. A direct self edge reports the root itself as
	// a direct (or test) caller; visited is seeded with root so a longer
	// cycle never re-adds it.
	reference := func(root string, maxDepth int) *ImpactResult {
		res := &ImpactResult{Symbol: root, Depth: maxDepth}
		visited := map[string]bool{root: true}
		var queue []string
		for _, c := range out[root] {
			if c == root {
				// Direct self edge: the root is its own caller.
				if isTest[c] {
					res.TestCallers = append(res.TestCallers, c)
				} else {
					res.DirectCallers = append(res.DirectCallers, c)
				}
				continue
			}
			if visited[c] {
				continue
			}
			visited[c] = true
			if isTest[c] {
				res.TestCallers = append(res.TestCallers, c)
			} else {
				res.DirectCallers = append(res.DirectCallers, c)
				queue = append(queue, c)
			}
		}
		for d := 1; d < maxDepth && len(queue) > 0; d++ {
			var next []string
			for _, n := range queue {
				for _, c := range out[n] {
					if visited[c] {
						continue
					}
					visited[c] = true
					if isTest[c] {
						res.TestCallers = append(res.TestCallers, c)
					} else {
						res.TransitiveCallers = append(res.TransitiveCallers, c)
						next = append(next, c)
					}
				}
			}
			queue = next
		}
		fileSet := make(map[string]bool)
		for _, s := range append(append([]string{}, res.DirectCallers...), res.TransitiveCallers...) {
			fileSet[fileOf[s]] = true
		}
		for f := range fileSet {
			res.AffectedFiles = append(res.AffectedFiles, f)
		}
		sort.Strings(res.DirectCallers)
		sort.Strings(res.TransitiveCallers)
		sort.Strings(res.TestCallers)
		sort.Strings(res.AffectedFiles)
		return res
	}

	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 20; i++ {
		root := fqns[rng.Intn(len(fqns))]
		for _, depth := range []int{1, 2, 3, 5} {
			got, err := g.Impact(root, depth)
			if err != nil {
				t.Fatalf("Impact(%s, %d): %v", root, depth, err)
			}
			want := reference(root, depth)
			sort.Strings(got.DirectCallers)
			sort.Strings(got.TransitiveCallers)
			sort.Strings(got.TestCallers)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Impact(%s, %d) = %+v, want %+v", root, depth, got, want)
			}
		}
	}
}

// TestPathShortestAndValid checks Path against reference BFS distances on
// random graphs, and validates that every returned step is a real edge in
// the right direction.
func TestPathShortestAndValid(t *testing.T) {
	g, _ := openTestGraph(t)
	deltas, out, in, fqns := randomGraph(11, 5, 70, 220)
	applyAll(t, g, deltas)
	// The path walk expands CALLS forward, CALLS backward and IMPLEMENTS
	// forward; this graph has only CALLS, so the adjacency is their union.
	adj := make(map[string][]string)
	for n, ms := range in {
		adj[n] = append(adj[n], ms...)
	}
	for n, ms := range out {
		adj[n] = append(adj[n], ms...)
	}
	edgeSet := make(map[string]bool)
	for _, d := range deltas {
		for _, e := range d.Edges {
			if !e.DstExternal {
				edgeSet[e.Src+"\x00"+e.Dst] = true
			}
		}
	}

	rng := rand.New(rand.NewSource(1234))
	for i := 0; i < 60; i++ {
		from := fqns[rng.Intn(len(fqns))]
		to := fqns[rng.Intn(len(fqns))]
		wantDist := bfsLevels(adj, from, maxPathDepth)
		steps, err := g.Path(from, to)
		if from == to {
			if err != nil || len(steps) != 0 {
				t.Fatalf("Path(%s, %s) = %v, %v; want empty, nil", from, to, steps, err)
			}
			continue
		}
		d, reachable := wantDist[to]
		if !reachable {
			if err == nil {
				t.Fatalf("Path(%s, %s) = %v; want no-path error", from, to, steps)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Path(%s, %s): %v", from, to, err)
		}
		if len(steps) != d {
			t.Fatalf("Path(%s, %s) has %d steps, want shortest %d: %+v", from, to, len(steps), d, steps)
		}
		cur := from
		for _, s := range steps {
			if s.From != cur {
				t.Fatalf("Path(%s, %s) broken chain at %+v", from, to, s)
			}
			switch s.Relation {
			case "CALLS", "IMPLEMENTS":
				if !edgeSet[s.From+"\x00"+s.To] {
					t.Fatalf("Path step %+v is not a forward edge", s)
				}
			case "CALLED_BY":
				if !edgeSet[s.To+"\x00"+s.From] {
					t.Fatalf("Path step %+v is not a backward CALLS edge", s)
				}
			default:
				t.Fatalf("Path step %+v has unknown relation", s)
			}
			cur = s.To
		}
		if cur != to {
			t.Fatalf("Path(%s, %s) ends at %s", from, to, cur)
		}
	}
}

func TestSymbolsSearch(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{
		File: "a.go",
		Symbols: []Symbol{
			mkSym("pkg.Alpha", "a.go", 1),
			mkSym("pkg.alphabet", "a.go", 10),
			mkSym("pkg.Beta", "a.go", 20),
		},
	})
	got, err := g.Symbols("ALPH")
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Symbols(ALPH) returned %d, want 2: %+v", len(got), got)
	}
	// LIKE metacharacters in the query match literally, not as wildcards.
	mustApply(t, g, FileDelta{File: "b.go", Symbols: []Symbol{mkSym("pkg.100%_done", "b.go", 1)}})
	got, err = g.Symbols("0%_d")
	if err != nil {
		t.Fatalf("Symbols: %v", err)
	}
	if len(got) != 1 || got[0].FQN != "pkg.100%_done" {
		t.Fatalf("Symbols(0%%_d) = %+v, want only pkg.100%%_done", got)
	}
	// Anchors survive the round trip.
	s := got[0]
	if s.StartLine != 1 || s.EndLine != 6 || s.StartCol != 1 || s.ByteOffset != 40 {
		t.Fatalf("anchors lost: %+v", s)
	}
}

func TestStatsAndInDegreeTop(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "ha",
		Symbols: []Symbol{mkSym("a.Hub", "a.go", 1), mkSym("a.One", "a.go", 10)},
		Edges:   []Edge{{Src: "a.One", Dst: "a.Hub"}},
	})
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "hb",
		Symbols: []Symbol{mkSym("b.Two", "b.go", 1), mkSym("b.Impl", "b.go", 10)},
		Edges: []Edge{
			{Src: "b.Two", Dst: "a.Hub"},
			{Src: "b.Impl", Dst: "a.Hub", Kind: "IMPLEMENTS"},
			{Src: "b.Two", Dst: "ext.Std", DstExternal: true},
		},
	})
	st, err := g.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := Stats{Files: 2, Symbols: 4, CallEdges: 3, DeclareEdges: 4, ImplementsEdges: 1}
	if st != want {
		t.Fatalf("Stats = %+v, want %+v", st, want)
	}
	top, err := g.InDegreeTop(5)
	if err != nil {
		t.Fatalf("InDegreeTop: %v", err)
	}
	if len(top) != 1 || top[0].FQN != "a.Hub" || top[0].InDegree != 2 {
		t.Fatalf("InDegreeTop = %+v, want a.Hub with 2 call edges (IMPLEMENTS and external excluded)", top)
	}
}

func TestDepthNormalisation(t *testing.T) {
	g, _ := openTestGraph(t)
	// A chain longer than the cap: depth 0 means direct only, and a huge
	// depth is capped at maxTraversalDepth.
	var syms []Symbol
	var edges []Edge
	for i := 0; i < 15; i++ {
		syms = append(syms, mkSym(fmt.Sprintf("c.N%d", i), "c.go", int64(i+1)))
		if i > 0 {
			edges = append(edges, Edge{Src: fmt.Sprintf("c.N%d", i), Dst: fmt.Sprintf("c.N%d", i-1)})
		}
	}
	mustApply(t, g, FileDelta{File: "c.go", Symbols: syms, Edges: edges})
	if hops, err := g.Callers("c.N0", 0); err != nil || len(hops) != 1 || hops[0].FQN != "c.N1" {
		t.Fatalf("Callers depth 0 = %+v, %v; want direct caller c.N1 only", hops, err)
	}
	hops, err := g.Callers("c.N0", 1000)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	if len(hops) != maxTraversalDepth {
		t.Fatalf("Callers depth 1000 returned %d hops, want cap %d", len(hops), maxTraversalDepth)
	}
	imp, err := g.Impact("c.N0", 0)
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if imp.Depth != 5 {
		t.Fatalf("Impact depth 0 normalised to %d, want kuzu's default 5", imp.Depth)
	}
}

// TestInDegreeTopTiesAndOrdering guards the aggregate-first rewrite of
// InDegreeTop: aggregate on edges, LIMIT, then join symbols for the top rows
// only. Degrees count CALLS edge rows (IMPLEMENTS and external targets are
// excluded), ties break by FQN, and n <= 0 keeps the default of 10.
func TestInDegreeTopTiesAndOrdering(t *testing.T) {
	g, _ := openTestGraph(t)
	hub := FileDelta{File: "hub.go", Symbols: []Symbol{
		mkSym("hub.A", "hub.go", 1), mkSym("hub.B", "hub.go", 10),
		mkSym("hub.C", "hub.go", 20), mkSym("hub.D", "hub.go", 30),
	}}
	var callers []Symbol
	for i := 1; i <= 6; i++ {
		callers = append(callers, mkSym(fmt.Sprintf("c.X%d", i), "callers.go", int64(i)))
	}
	full := FileDelta{File: "callers.go", Symbols: callers, Edges: []Edge{
		{Src: "c.X1", Dst: "hub.A"}, {Src: "c.X2", Dst: "hub.A"}, {Src: "c.X3", Dst: "hub.A"},
		{Src: "c.X4", Dst: "hub.B"}, {Src: "c.X5", Dst: "hub.B"},
		{Src: "c.X6", Dst: "hub.C"}, {Src: "c.X1", Dst: "hub.C"},
		{Src: "c.X2", Dst: "hub.D"},
		{Src: "c.X3", Dst: "hub.A", Kind: "IMPLEMENTS"},  // not a call
		{Src: "c.X4", Dst: "ext.Lib", DstExternal: true}, // not a declaration
	}}
	// Declarations first, so the cross-file edges resolve (as applyAll does).
	mustApply(t, g, FileDelta{File: hub.File, Symbols: hub.Symbols})
	mustApply(t, g, FileDelta{File: full.File, Symbols: full.Symbols})
	mustApply(t, g, hub)
	mustApply(t, g, full)

	top, err := g.InDegreeTop(3)
	if err != nil {
		t.Fatalf("InDegreeTop: %v", err)
	}
	want := []InDegree{
		{FQN: "hub.A", Name: "hub.A", Kind: "function", File: "hub.go", InDegree: 3},
		{FQN: "hub.B", Name: "hub.B", Kind: "function", File: "hub.go", InDegree: 2},
		{FQN: "hub.C", Name: "hub.C", Kind: "function", File: "hub.go", InDegree: 2},
	}
	if !reflect.DeepEqual(top, want) {
		t.Fatalf("InDegreeTop(3) = %+v, want %+v (tie hub.B/hub.C by FQN)", top, want)
	}
	all, err := g.InDegreeTop(100)
	if err != nil || len(all) != 4 || all[3].FQN != "hub.D" || all[3].InDegree != 1 {
		t.Fatalf("InDegreeTop(100) = %+v, %v; want 4 rows ending hub.D:1", all, err)
	}
	def, err := g.InDegreeTop(0)
	if err != nil || !reflect.DeepEqual(def, all) {
		t.Fatalf("InDegreeTop(0) = %+v, %v; want the default top 10 (= all 4)", def, err)
	}
}

// TestStatsKindCounts guards the single-pass rewrite of Stats: one GROUP BY
// over edges maps each kind to its counter, REFERENCES counts towards
// neither CALLS nor IMPLEMENTS, and DeclareEdges stays the symbol count.
func TestStatsKindCounts(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "ha",
		Symbols: []Symbol{mkSym("a.S1", "a.go", 1), mkSym("a.S2", "a.go", 10)},
		Edges:   []Edge{{Src: "a.S1", Dst: "a.S2"}},
	})
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "hb",
		Symbols: []Symbol{mkSym("b.S1", "b.go", 1)},
		Edges: []Edge{
			{Src: "b.S1", Dst: "a.S1", Kind: "IMPLEMENTS"},
			{Src: "b.S1", Dst: "a.S2", Kind: "REFERENCES"},
			{Src: "b.S1", Dst: "ext.Std", DstExternal: true},
		},
	})
	st, err := g.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := Stats{Files: 2, Symbols: 3, CallEdges: 2, DeclareEdges: 3, ImplementsEdges: 1}
	if st != want {
		t.Fatalf("Stats = %+v, want %+v (external CALLS counts, REFERENCES does not)", st, want)
	}
}

// resolveFixture seeds the symbols and CALLS edges ResolveSymbols tests need:
// Go-style FQNs ('.' and '/' qualifiers), one Rust-style '::' FQN, two
// symbols that share the base name "Close", and a symbol whose FQN is exactly
// "StoreA.Close" so the exact-vs-suffix preference is observable.
func resolveFixture(t *testing.T) *SQLite {
	t.Helper()
	g, _ := openTestGraph(t)
	sym := func(fqn, name, file string, line int64) Symbol {
		return Symbol{FQN: fqn, Name: name, Kind: "method", File: file,
			StartLine: line, EndLine: line + 1, StartCol: 1, ByteOffset: line}
	}
	// declarations and outgoing edges, grouped by the owner file of each edge.
	files := map[string]struct {
		syms  []Symbol
		edges []Edge
	}{
		"pkg/a/store.go": {syms: []Symbol{sym("pkg/a.StoreA.Close", "Close", "pkg/a/store.go", 50)}},
		"pkg/b/store.go": {syms: []Symbol{sym("pkg/b.StoreB.Close", "Close", "pkg/b/store.go", 80)}},
		"pkg/a/worker.go": {syms: []Symbol{sym("pkg/a.WorkerA.Run", "Run", "pkg/a/worker.go", 20)},
			edges: []Edge{{Src: "pkg/a.WorkerA.Run", Dst: "pkg/a.StoreA.Close"}}},
		"pkg/b/worker.go": {syms: []Symbol{sym("pkg/b.WorkerB.Run", "Run", "pkg/b/worker.go", 35)},
			edges: []Edge{{Src: "pkg/b.WorkerB.Run", Dst: "pkg/b.StoreB.Close"}}},
		"pkg/extra.go": {syms: []Symbol{
			sym("StoreA.Close", "Close", "pkg/extra.go", 5),
			sym("pkg/extra.Extra", "Extra", "pkg/extra.go", 7),
		}, edges: []Edge{{Src: "pkg/extra.Extra", Dst: "StoreA.Close"}}},
		"mod/util/search.go": {syms: []Symbol{sym("mod/util.Search", "Search", "mod/util/search.go", 5)}},
		"mod/pkg/lookup.go": {syms: []Symbol{sym("mod/pkg.Lookup", "Lookup", "mod/pkg/lookup.go", 12)},
			edges: []Edge{{Src: "mod/pkg.Lookup", Dst: "mod/util.Search"}}},
		"crate/mod.rs": {syms: []Symbol{
			sym("crate::mod::Thing", "Thing", "crate/mod.rs", 3),
			sym("crate::mod::Caller", "Caller", "crate/mod.rs", 9),
		}, edges: []Edge{{Src: "crate::mod::Caller", Dst: "crate::mod::Thing"}}},
	}
	names := make([]string, 0, len(files))
	for f := range files {
		names = append(names, f)
	}
	sort.Strings(names)
	// Declarations first so cross-file edges resolve, then full deltas.
	for _, f := range names {
		mustApply(t, g, FileDelta{File: f, Symbols: files[f].syms})
	}
	for _, f := range names {
		mustApply(t, g, FileDelta{File: f, Symbols: files[f].syms, Edges: files[f].edges})
	}
	return g
}

// TestResolveSymbols covers the three-step lookup kuzu.Callees/Callers apply
// to their argument, which ResolveSymbols ports to SQL: exact FQN, a
// qualified suffix ('.', '/' and '::'), then the base name (the union when it
// matches several symbols), with the first non-empty step winning.
func TestResolveSymbols(t *testing.T) {
	g := resolveFixture(t)

	cases := []struct {
		name string
		q    string
		dir  Direction
		want []string
	}{
		{"exact FQN", "pkg/a.StoreA.Close", DirectionCallers, []string{"pkg/a.StoreA.Close"}},
		{"short name", "Search", DirectionCallers, []string{"mod/util.Search"}},
		{"ambiguous base name union", "Close", DirectionCallers,
			[]string{"StoreA.Close", "pkg/a.StoreA.Close", "pkg/b.StoreB.Close"}},
		{"Type.Method suffix", "StoreB.Close", DirectionCallers, []string{"pkg/b.StoreB.Close"}},
		{"path-qualified suffix", "a.StoreA.Close", DirectionCallers, []string{"pkg/a.StoreA.Close"}},
		{"double-colon suffix", "mod::Thing", DirectionCallers, []string{"crate::mod::Thing"}},
		{"exact FQN preferred over suffix", "StoreA.Close", DirectionCallers, []string{"StoreA.Close"}},
		{"callee-side ambiguous union", "Run", DirectionCallees,
			[]string{"pkg/a.WorkerA.Run", "pkg/b.WorkerB.Run"}},
		{"no match", "NoSuchSymbol", DirectionCallers, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := g.ResolveSymbols(tc.q, tc.dir)
			if err != nil {
				t.Fatalf("ResolveSymbols(%q, %d): %v", tc.q, tc.dir, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ResolveSymbols(%q, %d) = %v, want %v", tc.q, tc.dir, got, tc.want)
			}
		})
	}
}

func TestHasTreeSitterEdges_LegacyFallback(t *testing.T) {
	ctx := context.Background()
	g, _ := openTestGraph(t)

	// Ensure no MetaGraphTreeSitter key exists
	_ = g.ClearMeta(ctx, store.MetaGraphTreeSitter)

	// Case 1: Empty graph -> false
	if g.HasTreeSitterEdges() {
		t.Fatal("empty graph must report false")
	}

	// Case 2: Only Go files with CALLS edges -> false
	deltaGo := FileDelta{
		File: "main.go",
		Symbols: []Symbol{
			{FQN: "main.Run", Name: "Run", Kind: "function", File: "main.go", StartLine: 1, EndLine: 5},
			{FQN: "main.Worker", Name: "Worker", Kind: "function", File: "main.go", StartLine: 6, EndLine: 10},
		},
		Edges: []Edge{
			{Src: "main.Run", Dst: "main.Worker", Kind: "CALLS"},
		},
	}
	if err := g.ApplyFileDelta(ctx, deltaGo); err != nil {
		t.Fatal(err)
	}
	_ = g.ClearMeta(ctx, store.MetaGraphTreeSitter)
	if g.HasTreeSitterEdges() {
		t.Fatal("Go-only graph must report false")
	}

	// Case 3: Python file with CALLS edge -> true
	deltaPy := FileDelta{
		File: "app.py",
		Symbols: []Symbol{
			{FQN: "app.caller", Name: "caller", Kind: "function", File: "app.py", StartLine: 1, EndLine: 5},
			{FQN: "app.callee", Name: "callee", Kind: "function", File: "app.py", StartLine: 6, EndLine: 10},
		},
		Edges: []Edge{
			{Src: "app.caller", Dst: "app.callee", Kind: "CALLS"},
		},
	}
	if err := g.ApplyFileDelta(ctx, deltaPy); err != nil {
		t.Fatal(err)
	}
	_ = g.ClearMeta(ctx, store.MetaGraphTreeSitter)
	start := time.Now()
	has := g.HasTreeSitterEdges()
	dur := time.Since(start)
	if !has {
		t.Fatal("Python CALLS edge must report true")
	}
	if dur > 100*time.Millisecond {
		t.Fatalf("HasTreeSitterEdges took too long: %v", dur)
	}
}
