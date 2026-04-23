//go:build treesitter

package community

import (
	"testing"
)

func TestComputeRiskScores_Simple(t *testing.T) {
	nodes := []Node{
		{ID: "A", Name: "A", Kind: "function", File: "a.go"},
		{ID: "B", Name: "B", Kind: "function", File: "a.go"},
		{ID: "C", Name: "C", Kind: "function", File: "b.go"},
		{ID: "D", Name: "D", Kind: "function", File: "b.go"},
	}
	edges := []Edge{
		{From: "A", To: "B"},
		{From: "A", To: "C"},
		{From: "B", To: "C"},
		{From: "C", To: "D"},
	}

	cfg := DefaultRiskConfig()
	scores := ComputeRiskScores(nodes, edges, cfg)
	if len(scores) != 4 {
		t.Fatalf("expected 4 scores, got %d", len(scores))
	}
	// Scores should be sorted descending.
	for i := 1; i < len(scores); i++ {
		if scores[i].Score > scores[i-1].Score {
			t.Errorf("scores not sorted: [%d]=%f > [%d]=%f", i, scores[i].Score, i-1, scores[i-1].Score)
		}
	}
}

func TestComputeRiskScores_Empty(t *testing.T) {
	scores := ComputeRiskScores(nil, nil, DefaultRiskConfig())
	if scores != nil {
		t.Error("expected nil for empty graph")
	}
}

func TestComputeRiskScores_Hub(t *testing.T) {
	// Star graph: center node should have highest risk.
	nodes := []Node{
		{ID: "hub", Name: "hub", Kind: "function", File: "hub.go"},
	}
	edges := []Edge{}
	for i := 0; i < 10; i++ {
		spoke := Node{ID: string(rune('a' + i)), Name: string(rune('a' + i)), Kind: "function", File: "spoke.go"}
		nodes = append(nodes, spoke)
		edges = append(edges, Edge{From: spoke.ID, To: "hub"})
		edges = append(edges, Edge{From: "hub", To: spoke.ID})
	}

	scores := ComputeRiskScores(nodes, edges, DefaultRiskConfig())
	if scores[0].ID != "hub" {
		t.Errorf("expected hub to be highest risk, got %s", scores[0].ID)
	}
	if scores[0].RiskLevel == "low" {
		t.Error("hub should not be low risk")
	}
}

func TestTopRisks(t *testing.T) {
	scores := []RiskScore{
		{ID: "A", Score: 0.9},
		{ID: "B", Score: 0.5},
		{ID: "C", Score: 0.1},
	}
	top := TopRisks(scores, 2)
	if len(top) != 2 {
		t.Fatalf("expected 2, got %d", len(top))
	}
	if top[0].ID != "A" || top[1].ID != "B" {
		t.Error("wrong top risks")
	}
}

func TestTopRisks_Empty(t *testing.T) {
	if TopRisks(nil, 5) != nil {
		t.Error("expected nil")
	}
	if TopRisks([]RiskScore{{ID: "A"}}, 0) != nil {
		t.Error("expected nil for k=0")
	}
}

func TestFileRiskSummary(t *testing.T) {
	scores := []RiskScore{
		{ID: "A", File: "a.go", Score: 0.8},
		{ID: "B", File: "a.go", Score: 0.9},
		{ID: "C", File: "b.go", Score: 0.3},
	}
	summary := FileRiskSummary(scores)
	if len(summary) != 2 {
		t.Fatalf("expected 2 files, got %d", len(summary))
	}
	// a.go should have max from B (0.9).
	if summary[0].File != "a.go" || summary[0].Score != 0.9 {
		t.Errorf("expected a.go with 0.9, got %s with %f", summary[0].File, summary[0].Score)
	}
}

func TestClassifyRisk(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0.80, "critical"},
		{0.75, "critical"},
		{0.60, "high"},
		{0.50, "high"},
		{0.30, "medium"},
		{0.25, "medium"},
		{0.10, "low"},
		{0.0, "low"},
	}
	for _, tc := range tests {
		got := classifyRisk(tc.score)
		if got != tc.want {
			t.Errorf("classifyRisk(%f) = %q, want %q", tc.score, got, tc.want)
		}
	}
}
