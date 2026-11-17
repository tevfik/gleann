package graphstore

import (
	"fmt"
	"math/rand"
)

// GraphFixture is the synthetic code graph the graphstore benchmarks run on
// (the RFC 003 section 1.1 scale fixture BenchmarkCallersDepth3 used to build
// inline): nSyms symbols spread over nFiles packages/files, and nEdges random
// CALLS edges where edge i is owned by the file of its source symbol (the
// owner rule of RFC 003 section 6.5). The naming and the edge stream are
// identical to the benchmark's: FQNs "bench/p<file>.Sym<i>", files
// "bench/f<file>.go", rand seed 2026 and the same draw order.
//
// Duplicate (src, dst) draws are dropped here instead of by INSERT OR IGNORE
// (the edges primary key (src, dst, kind, file) and the fixture's "edge file
// = source file" rule make the database ignore exactly those), so a database
// loaded from NewGraphFixture stores precisely the rows BenchmarkCallersDepth3
// stored, and len(Edges) is the true stored edge count (slightly below NEdges).
//
// It lives in a non-test file so both the internal benchmarks and the external
// comparison harness (package graphstore_test, which cannot share internal
// test helpers) build the identical graph.
type GraphFixture struct {
	NSyms  int
	NEdges int // target draw count; len(Edges) can be slightly lower
	NFiles int

	FQNs  []string // len NSyms
	Names []string // len NSyms
	Files []string // declaring file of FQNs[i]
	Pkgs  []string // package of FQNs[i]
	Lines []int64  // start_line of FQNs[i] (i%100 + 1, as the benchmark used)

	// Edges are the unique CALLS pairs; Edges[j].File owns the edge (the
	// file of the source symbol).
	Edges []FixtureEdge
}

// FixtureEdge is one directed CALLS edge of the synthetic graph.
type FixtureEdge struct {
	Src  string
	Dst  string
	File string // declaring file of Src; the owner of the edge
}

// NewGraphFixture builds the fixture. It is deterministic: the same arguments
// always produce the same graph.
func NewGraphFixture(nSyms, nEdges, nFiles int) *GraphFixture {
	if nSyms < 1 {
		nSyms = 1
	}
	if nFiles < 1 {
		nFiles = 1
	}
	fx := &GraphFixture{
		NSyms:  nSyms,
		NEdges: nEdges,
		NFiles: nFiles,
		FQNs:   make([]string, nSyms),
		Names:  make([]string, nSyms),
		Files:  make([]string, nSyms),
		Pkgs:   make([]string, nSyms),
		Lines:  make([]int64, nSyms),
	}
	for i := 0; i < nSyms; i++ {
		fx.FQNs[i] = fmt.Sprintf("bench/p%d.Sym%d", i%nFiles, i)
		fx.Names[i] = fmt.Sprintf("Sym%d", i)
		fx.Files[i] = fmt.Sprintf("bench/f%d.go", i%nFiles)
		fx.Pkgs[i] = fmt.Sprintf("bench/p%d", i%nFiles)
		fx.Lines[i] = int64(i%100 + 1)
	}
	rng := rand.New(rand.NewSource(2026))
	seen := make(map[[2]int]bool, nEdges)
	for i := 0; i < nEdges; i++ {
		s, d := rng.Intn(nSyms), rng.Intn(nSyms)
		key := [2]int{s, d}
		if seen[key] {
			continue // INSERT OR IGNORE would have ignored this duplicate
		}
		seen[key] = true
		fx.Edges = append(fx.Edges, FixtureEdge{Src: fx.FQNs[s], Dst: fx.FQNs[d], File: fx.Files[s]})
	}
	return fx
}

// Deltas returns the per-file FileDeltas of the fixture, one per file in file
// order: every symbol anchored in its declaring file and every edge owned by
// the file of its source, with the neutral fields the benchmark used (kind
// "function", weight 1.0, no test flag, no doc).
func (fx *GraphFixture) Deltas() []FileDelta {
	deltas := make([]FileDelta, fx.NFiles)
	byFile := make(map[string]*FileDelta, fx.NFiles)
	for f := 0; f < fx.NFiles; f++ {
		file := fmt.Sprintf("bench/f%d.go", f)
		deltas[f] = FileDelta{File: file}
		byFile[file] = &deltas[f]
	}
	for i, fqn := range fx.FQNs {
		d := byFile[fx.Files[i]]
		line := fx.Lines[i]
		d.Symbols = append(d.Symbols, Symbol{
			FQN: fqn, Kind: "function", File: fx.Files[i], Name: fx.Names[i],
			StartLine: line, EndLine: line + 5, StartCol: 1, ByteOffset: line * 40,
			Weight: 1.0,
		})
	}
	for _, e := range fx.Edges {
		d := byFile[e.File]
		d.Edges = append(d.Edges, Edge{Src: e.Src, Dst: e.Dst, Kind: "CALLS", Confidence: "extracted"})
	}
	return deltas
}
