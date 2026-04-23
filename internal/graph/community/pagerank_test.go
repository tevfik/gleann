//go:build treesitter

package community

import (
	"math"
	"testing"
)

func TestPageRank_Simple(t *testing.T) {
	// A → B → C chain: C should have highest rank (most "authority").
	nodes := []Node{
		{ID: "A", Name: "A", Kind: "function"},
		{ID: "B", Name: "B", Kind: "function"},
		{ID: "C", Name: "C", Kind: "function"},
	}
	edges := []Edge{
		{From: "A", To: "B"},
		{From: "B", To: "C"},
	}

	ranks := PageRank(nodes, edges, 0.85, 30)

	if len(ranks) != 3 {
		t.Fatalf("expected 3 ranks, got %d", len(ranks))
	}

	// C should have higher rank than A (it receives links).
	if ranks["C"] <= ranks["A"] {
		t.Errorf("C should rank higher than A: C=%f A=%f", ranks["C"], ranks["A"])
	}
}

func TestPageRank_Star(t *testing.T) {
	// Star topology: A, B, C, D all point to E.
	nodes := []Node{
		{ID: "A"}, {ID: "B"}, {ID: "C"}, {ID: "D"}, {ID: "E"},
	}
	edges := []Edge{
		{From: "A", To: "E"},
		{From: "B", To: "E"},
		{From: "C", To: "E"},
		{From: "D", To: "E"},
	}

	ranks := PageRank(nodes, edges, 0.85, 30)

	// E should have the highest rank.
	for id, r := range ranks {
		if id != "E" && r >= ranks["E"] {
			t.Errorf("E should have highest rank, but %s=%f >= E=%f", id, r, ranks["E"])
		}
	}
}

func TestPageRank_SumsToOne(t *testing.T) {
	nodes := []Node{{ID: "A"}, {ID: "B"}, {ID: "C"}}
	edges := []Edge{
		{From: "A", To: "B"},
		{From: "B", To: "C"},
		{From: "C", To: "A"},
	}

	ranks := PageRank(nodes, edges, 0.85, 50)
	sum := 0.0
	for _, r := range ranks {
		sum += r
	}
	if math.Abs(sum-1.0) > 0.01 {
		t.Errorf("PageRank should sum to ~1.0, got %f", sum)
	}
}

func TestPageRank_Empty(t *testing.T) {
	ranks := PageRank(nil, nil, 0.85, 30)
	if ranks != nil {
		t.Error("empty graph should return nil")
	}
}

func TestTopRanked(t *testing.T) {
	ranks := map[string]float64{
		"A": 0.1, "B": 0.3, "C": 0.2, "D": 0.4,
	}
	top := TopRanked(ranks, 2)
	if len(top) != 2 {
		t.Fatalf("expected 2 results, got %d", len(top))
	}
	if top[0].ID != "D" {
		t.Errorf("first should be D, got %s", top[0].ID)
	}
	if top[1].ID != "B" {
		t.Errorf("second should be B, got %s", top[1].ID)
	}
}

func TestBlastRadius(t *testing.T) {
	edges := []Edge{
		{From: "A", To: "B"},
		{From: "B", To: "C"},
		{From: "C", To: "D"},
		{From: "D", To: "E"},
	}
	ranks := map[string]float64{
		"A": 0.2, "B": 0.2, "C": 0.2, "D": 0.2, "E": 0.2,
	}

	affected, score := BlastRadius("C", edges, ranks, 1)
	if len(affected) < 2 {
		t.Errorf("blast radius of C at depth 1 should include B and D, got %d nodes", len(affected))
	}
	if score <= 0 {
		t.Errorf("blast score should be > 0, got %f", score)
	}
}

func TestBlastRadius_Isolated(t *testing.T) {
	edges := []Edge{{From: "A", To: "B"}}
	ranks := map[string]float64{"A": 0.5, "B": 0.5}

	affected, _ := BlastRadius("C", edges, ranks, 3)
	if len(affected) != 0 {
		t.Errorf("isolated node should have empty blast radius, got %d", len(affected))
	}
}
