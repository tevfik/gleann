//go:build treesitter

package community

import "math"

// PageRank computes PageRank scores for all nodes in the graph.
// dampingFactor is typically 0.85. iterations controls convergence (20-50 typical).
// Returns a map from node ID to PageRank score (sums to ~1.0).
func PageRank(nodes []Node, edges []Edge, dampingFactor float64, iterations int) map[string]float64 {
	if len(nodes) == 0 {
		return nil
	}

	n := float64(len(nodes))
	ranks := make(map[string]float64, len(nodes))
	outDeg := make(map[string]int, len(nodes))

	// Build adjacency: outgoing edges per node.
	outgoing := make(map[string][]string)
	incoming := make(map[string][]string)

	for _, node := range nodes {
		ranks[node.ID] = 1.0 / n
	}

	for _, e := range edges {
		outgoing[e.From] = append(outgoing[e.From], e.To)
		incoming[e.To] = append(incoming[e.To], e.From)
		outDeg[e.From]++
	}

	// Iterative PageRank.
	for iter := 0; iter < iterations; iter++ {
		newRanks := make(map[string]float64, len(nodes))
		sinkRank := 0.0

		// Accumulate sink node rank (nodes with no outgoing edges).
		for _, node := range nodes {
			if outDeg[node.ID] == 0 {
				sinkRank += ranks[node.ID]
			}
		}

		for _, node := range nodes {
			rank := (1.0 - dampingFactor) / n
			rank += dampingFactor * sinkRank / n

			for _, fromID := range incoming[node.ID] {
				if outDeg[fromID] > 0 {
					rank += dampingFactor * ranks[fromID] / float64(outDeg[fromID])
				}
			}
			newRanks[node.ID] = rank
		}
		ranks = newRanks
	}

	return ranks
}

// TopRanked returns the top-k nodes by PageRank score.
func TopRanked(ranks map[string]float64, k int) []RankedNode {
	if k <= 0 || len(ranks) == 0 {
		return nil
	}

	result := make([]RankedNode, 0, len(ranks))
	for id, score := range ranks {
		result = append(result, RankedNode{ID: id, Score: score})
	}

	// Sort by score descending.
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].Score > result[i].Score {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	if k > len(result) {
		k = len(result)
	}
	return result[:k]
}

// RankedNode holds a node ID and its PageRank score.
type RankedNode struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

// BlastRadius computes the set of nodes reachable from a starting node
// within maxDepth hops, along with an impact score based on PageRank.
// Returns the affected nodes and the total "blast" score (sum of their PageRank).
func BlastRadius(startID string, edges []Edge, ranks map[string]float64, maxDepth int) ([]string, float64) {
	if maxDepth <= 0 {
		maxDepth = 3
	}

	// Build adjacency list (both directions for impact).
	adj := make(map[string][]string)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		// Reverse edges too: if A calls B, changing B impacts A.
		adj[e.To] = append(adj[e.To], e.From)
	}

	// BFS from startID.
	visited := map[string]bool{startID: true}
	frontier := []string{startID}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, nodeID := range frontier {
			for _, neighbor := range adj[nodeID] {
				if !visited[neighbor] {
					visited[neighbor] = true
					next = append(next, neighbor)
				}
			}
		}
		frontier = next
	}

	affected := make([]string, 0, len(visited))
	totalScore := 0.0
	for id := range visited {
		if id == startID {
			continue
		}
		affected = append(affected, id)
		if ranks != nil {
			totalScore += ranks[id]
		}
	}

	// Normalize by total rank mass for a 0-1 score.
	totalMass := 0.0
	for _, s := range ranks {
		totalMass += s
	}
	if totalMass > 0 {
		totalScore /= totalMass
	}

	return affected, math.Min(1.0, totalScore)
}
