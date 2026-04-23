//go:build treesitter

package community

import (
	"math"
	"sort"
)

// RiskScore represents a node's change risk based on graph metrics.
type RiskScore struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	File            string  `json:"file"`
	Centrality      float64 `json:"centrality"`        // PageRank-based importance
	InDegree        int     `json:"in_degree"`         // Number of callers
	OutDegree       int     `json:"out_degree"`        // Number of callees
	BlastRadiusSize int     `json:"blast_radius_size"` // Number of affected nodes
	BlastImpact     float64 `json:"blast_impact"`      // PageRank-weighted blast score
	Coupling        float64 `json:"coupling"`          // Fan-in × Fan-out normalized
	RiskLevel       string  `json:"risk_level"`        // "critical", "high", "medium", "low"
	Score           float64 `json:"score"`             // Composite risk score [0,1]
}

// RiskConfig controls risk scoring parameters.
type RiskConfig struct {
	// Weights for composite score (should sum to 1.0).
	CentralityWeight  float64
	CouplingWeight    float64
	BlastRadiusWeight float64

	// Blast radius computation depth.
	MaxBlastDepth int

	// PageRank parameters.
	DampingFactor float64
	Iterations    int
}

// DefaultRiskConfig returns sensible defaults for risk scoring.
func DefaultRiskConfig() RiskConfig {
	return RiskConfig{
		CentralityWeight:  0.35,
		CouplingWeight:    0.30,
		BlastRadiusWeight: 0.35,
		MaxBlastDepth:     3,
		DampingFactor:     0.85,
		Iterations:        30,
	}
}

// ComputeRiskScores calculates risk scores for all nodes in the graph.
// It combines PageRank centrality, coupling (fan-in × fan-out), and blast radius impact.
func ComputeRiskScores(nodes []Node, edges []Edge, cfg RiskConfig) []RiskScore {
	if len(nodes) == 0 {
		return nil
	}

	// 1. Compute PageRank for centrality.
	ranks := PageRank(nodes, edges, cfg.DampingFactor, cfg.Iterations)

	// 2. Compute in/out degree.
	inDeg := make(map[string]int, len(nodes))
	outDeg := make(map[string]int, len(nodes))
	for _, e := range edges {
		outDeg[e.From]++
		inDeg[e.To]++
	}

	// 3. Find max values for normalization.
	maxRank := 0.0
	maxCoupling := 0.0
	maxIn, maxOut := 0, 0
	for _, n := range nodes {
		if ranks[n.ID] > maxRank {
			maxRank = ranks[n.ID]
		}
		c := float64(inDeg[n.ID]) * float64(outDeg[n.ID])
		if c > maxCoupling {
			maxCoupling = c
		}
		if inDeg[n.ID] > maxIn {
			maxIn = inDeg[n.ID]
		}
		if outDeg[n.ID] > maxOut {
			maxOut = outDeg[n.ID]
		}
	}

	// 4. Compute risk for each node.
	results := make([]RiskScore, 0, len(nodes))
	for _, n := range nodes {
		// Normalize centrality.
		centralityNorm := 0.0
		if maxRank > 0 {
			centralityNorm = ranks[n.ID] / maxRank
		}

		// Normalize coupling (fan-in × fan-out).
		coupling := float64(inDeg[n.ID]) * float64(outDeg[n.ID])
		couplingNorm := 0.0
		if maxCoupling > 0 {
			couplingNorm = coupling / maxCoupling
		}

		// Blast radius.
		affected, blastScore := BlastRadius(n.ID, edges, ranks, cfg.MaxBlastDepth)

		// Composite score.
		score := cfg.CentralityWeight*centralityNorm +
			cfg.CouplingWeight*couplingNorm +
			cfg.BlastRadiusWeight*blastScore
		score = math.Min(1.0, score)

		results = append(results, RiskScore{
			ID:              n.ID,
			Name:            n.Name,
			Kind:            n.Kind,
			File:            n.File,
			Centrality:      ranks[n.ID],
			InDegree:        inDeg[n.ID],
			OutDegree:       outDeg[n.ID],
			BlastRadiusSize: len(affected),
			BlastImpact:     blastScore,
			Coupling:        couplingNorm,
			RiskLevel:       classifyRisk(score),
			Score:           score,
		})
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// TopRisks returns the top-k highest risk nodes.
func TopRisks(scores []RiskScore, k int) []RiskScore {
	if k <= 0 || len(scores) == 0 {
		return nil
	}
	if k > len(scores) {
		k = len(scores)
	}
	return scores[:k]
}

// FileRiskSummary aggregates risk scores by file, returning the max risk per file.
func FileRiskSummary(scores []RiskScore) []RiskScore {
	fileMax := make(map[string]*RiskScore)
	for i := range scores {
		s := &scores[i]
		if s.File == "" {
			continue
		}
		if existing, ok := fileMax[s.File]; !ok || s.Score > existing.Score {
			fileMax[s.File] = s
		}
	}

	result := make([]RiskScore, 0, len(fileMax))
	for _, rs := range fileMax {
		result = append(result, *rs)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result
}

func classifyRisk(score float64) string {
	switch {
	case score >= 0.75:
		return "critical"
	case score >= 0.50:
		return "high"
	case score >= 0.25:
		return "medium"
	default:
		return "low"
	}
}
