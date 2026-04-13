//go:build treesitter

package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/community"
)

func TestWriteMarkdown(t *testing.T) {
	result := &community.Result{
		Communities: []community.Community{
			{ID: 0, Label: "server", NodeCount: 10, Nodes: []string{"a", "b", "c"}, Cohesion: 0.5},
			{ID: 1, Label: "client", NodeCount: 5, Nodes: []string{"d", "e"}, Cohesion: 0.8},
		},
		GodNodes: []community.GodNode{
			{ID: "hub.Func", Name: "Func", Kind: "function", InDeg: 20, OutDeg: 5, TotalDeg: 25},
		},
		SurprisingEdges: []community.SurprisingEdge{
			{From: "a", To: "d", FromCommunity: 0, ToCommunity: 1, Weight: 1.0},
		},
		Modularity: 0.45,
		NodeCount:  15,
		EdgeCount:  20,
	}

	var buf bytes.Buffer
	err := WriteMarkdown(&buf, result, Options{IndexName: "test-idx"})
	if err != nil {
		t.Fatal(err)
	}

	md := buf.String()

	if !strings.Contains(md, "# Graph Report: test-idx") {
		t.Error("missing title")
	}
	if !strings.Contains(md, "| Nodes | 15 |") {
		t.Error("missing node count")
	}
	if !strings.Contains(md, "God Nodes") {
		t.Error("missing god nodes section")
	}
	if !strings.Contains(md, "`hub.Func`") {
		t.Error("missing god node name")
	}
	if !strings.Contains(md, "Community 0: server") {
		t.Error("missing community")
	}
	if !strings.Contains(md, "Cross-Community Edges") {
		t.Error("missing surprising edges section")
	}
	if !strings.Contains(md, "well-modularized") {
		t.Error("missing modularity interpretation")
	}
}

func TestSuggestQuestions(t *testing.T) {
	result := &community.Result{
		Communities: []community.Community{
			{ID: 0, Label: "server", NodeCount: 10, Nodes: []string{"a", "b"}, Cohesion: 0.5},
			{ID: 1, Label: "client", NodeCount: 5, Nodes: []string{"d", "e"}, Cohesion: 0.8},
		},
		GodNodes: []community.GodNode{
			{ID: "hub.Func", Name: "Func", Kind: "function", InDeg: 20, OutDeg: 5, TotalDeg: 25},
			{ID: "hub.Init", Name: "Init", Kind: "function", InDeg: 10, OutDeg: 3, TotalDeg: 13},
		},
		SurprisingEdges: []community.SurprisingEdge{
			{From: "a", To: "d", FromCommunity: 0, ToCommunity: 1, Weight: 1.0},
		},
		Modularity: 0.45,
		NodeCount:  15,
		EdgeCount:  20,
	}

	qs := suggestQuestions(result)

	if len(qs) < 3 {
		t.Errorf("expected ≥3 questions, got %d", len(qs))
	}

	joined := strings.Join(qs, " | ")
	// Should include question about first god node
	if !strings.Contains(joined, "Func") {
		t.Errorf("expected question mentioning 'Func' god node, got: %v", qs)
	}
	// Should include question about cross-community edges
	if !strings.Contains(joined, "server") || !strings.Contains(joined, "client") {
		t.Errorf("expected question about server/client communities, got: %v", qs)
	}
	// Should include question about surprising edge
	if !strings.Contains(joined, "surprising") && !strings.Contains(joined, "cross-community") {
		t.Errorf("expected question about surprising edge, got: %v", qs)
	}
}

func TestSurprisingScore(t *testing.T) {
	// Basic same-package, close communities
	e1 := community.SurprisingEdge{
		From: "pkg.A", To: "pkg.B", FromCommunity: 0, ToCommunity: 1, Weight: 1.0,
	}
	s1 := surprisingScore(e1)

	// Cross-package: should score higher (1.5x multiplier)
	e2 := community.SurprisingEdge{
		From: "server.Handler", To: "db.Query", FromCommunity: 0, ToCommunity: 1, Weight: 1.0,
	}
	s2 := surprisingScore(e2)

	if s2 <= s1 {
		t.Errorf("cross-package score (%.2f) should be > same-package (%.2f)", s2, s1)
	}

	// Large community distance: extra 1.2x multiplier
	e3 := community.SurprisingEdge{
		From: "server.Handler", To: "util.Log", FromCommunity: 0, ToCommunity: 5, Weight: 1.0,
	}
	s3 := surprisingScore(e3)

	if s3 <= s2 {
		t.Errorf("large-distance score (%.2f) should be > small-distance (%.2f)", s3, s2)
	}
}
