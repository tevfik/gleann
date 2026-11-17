package community

import (
	"fmt"
	"reflect"
	"testing"
)

// detGraph builds a graph with many ties (equal weights, symmetric rings) so
// that any dependence on map iteration order shows up quickly.
func detGraph() *Graph {
	g := NewGraph()
	const files, perFile = 6, 7
	for f := 0; f < files; f++ {
		file := fmt.Sprintf("pkg%d/file.go", f)
		g.AddNode(Node{ID: file, Name: file, Kind: "file"})
		for i := 0; i < perFile; i++ {
			id := fmt.Sprintf("pkg%d.F%d", f, i)
			g.AddNode(Node{ID: id, Name: fmt.Sprintf("F%d", i), Kind: "function", File: file})
			g.AddEdge(file, id, 0.3)
			g.AddEdge(id, fmt.Sprintf("pkg%d.F%d", f, (i+1)%perFile), 1.0) // ring
		}
		g.AddEdge(fmt.Sprintf("pkg%d.F0", f), fmt.Sprintf("pkg%d.F3", (f+1)%files), 1.0) // bridge
		g.AddEdge(fmt.Sprintf("pkg%d.F3", (f+1)%files), fmt.Sprintf("pkg%d.F0", f), 1.0) // mutual
	}
	return g
}

func TestAnalysisIsDeterministic(t *testing.T) {
	type out struct {
		res   *Result
		ranks map[string]float64
		top   []RankedNode
		risks []RiskScore
		files []RiskScore
		edges []Edge
		repo  string
	}
	run := func() out {
		g := detGraph()
		res, err := Detect(g, 3, 20)
		if err != nil {
			t.Fatal(err)
		}
		nodes, edges := g.ExportForAnalysis()
		ranks := PageRank(nodes, edges, 0.85, 30)
		risks := ComputeRiskScores(nodes, edges, DefaultRiskConfig())
		return out{res, ranks, TopRanked(ranks, 10), risks, FileRiskSummary(risks), edges,
			GenerateRepoMap(nodes, edges, DefaultRepoMapConfig())}
	}
	want := run()
	for i := 0; i < 25; i++ {
		if got := run(); !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d differs from the first:\n got %+v\nwant %+v", i+2, got.res, want.res)
		}
	}
	if len(want.res.Communities) < 2 {
		t.Errorf("expected several communities, got %d", len(want.res.Communities))
	}
}

func TestEdgesAreDirected(t *testing.T) {
	g := NewGraph()
	g.AddEdge("a", "b", 1)
	g.AddEdge("b", "a", 1)
	g.AddEdge("a", "b", 1) // repeated: weights add up, no duplicate edge
	got := g.Edges()
	want := []Edge{{From: "a", To: "b", Weight: 2}, {From: "b", To: "a", Weight: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Edges() = %+v, want %+v", got, want)
	}
}
