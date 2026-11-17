package server

import (
	"fmt"
	"strings"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/pkg/gleann"
)

// a2aCommunitiesHandler runs Louvain community detection on the code graph.
func (s *Server) a2aCommunitiesHandler(ctx a2a.SkillContext) (string, error) {
	indexName := resolveIndex(ctx.Metadata, s.cfg().IndexDir)
	if indexName == "" {
		return "", fmt.Errorf("no indexes available")
	}
	if err := gleann.ValidateName(indexName); err != nil {
		return "", err
	}

	h, err := s.a2aAnalysis(indexName)
	if err != nil {
		return "", err
	}

	result, err := h.Communities(5, 20)
	if err != nil {
		return "", fmt.Errorf("community detection failed: %v", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Community Detection — %s\n", indexName))
	sb.WriteString(fmt.Sprintf("Nodes: %d | Edges: %d | Communities: %d | Modularity: %.4f\n\n",
		result.NodeCount, result.EdgeCount, len(result.Communities), result.Modularity))

	for _, c := range result.Communities {
		sb.WriteString(fmt.Sprintf("Community %d: %s (%d nodes, cohesion=%.3f)\n",
			c.ID, c.Label, c.NodeCount, c.Cohesion))
	}

	if len(result.GodNodes) > 0 {
		sb.WriteString("\nGod Nodes:\n")
		for _, g := range result.GodNodes {
			sb.WriteString(fmt.Sprintf("  [%s] %s (in=%d, out=%d)\n", g.Kind, g.Name, g.InDeg, g.OutDeg))
		}
	}

	return sb.String(), nil
}

// a2aRepoMapHandler generates a compact repository map.
func (s *Server) a2aRepoMapHandler(ctx a2a.SkillContext) (string, error) {
	indexName := resolveIndex(ctx.Metadata, s.cfg().IndexDir)
	if indexName == "" {
		return "", fmt.Errorf("no indexes available")
	}
	if err := gleann.ValidateName(indexName); err != nil {
		return "", err
	}

	h, err := s.a2aAnalysis(indexName)
	if err != nil {
		return "", err
	}

	g, err := h.CallGraph()
	if err != nil {
		return "", fmt.Errorf("loading call graph: %v", err)
	}
	nodes, edges := g.ExportForAnalysis()

	if len(nodes) == 0 {
		return "No nodes in graph. Build index with --graph flag.", nil
	}

	cfg := community.DefaultRepoMapConfig()
	return community.GenerateRepoMap(nodes, edges, cfg), nil
}

// a2aRiskHandler computes change risk scores.
func (s *Server) a2aRiskHandler(ctx a2a.SkillContext) (string, error) {
	indexName := resolveIndex(ctx.Metadata, s.cfg().IndexDir)
	if indexName == "" {
		return "", fmt.Errorf("no indexes available")
	}
	if err := gleann.ValidateName(indexName); err != nil {
		return "", err
	}

	h, err := s.a2aAnalysis(indexName)
	if err != nil {
		return "", err
	}

	g, err := h.CallGraph()
	if err != nil {
		return "", fmt.Errorf("loading call graph: %v", err)
	}
	nodes, edges := g.ExportForAnalysis()

	if len(nodes) == 0 {
		return "No nodes in graph. Build index with --graph flag.", nil
	}

	cfg := community.DefaultRiskConfig()
	allScores := community.ComputeRiskScores(nodes, edges, cfg)
	top := community.TopRisks(allScores, 20)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Risk Analysis — %s (top %d)\n\n", indexName, len(top)))
	for _, rs := range top {
		sb.WriteString(fmt.Sprintf("  [%s] %s — %s (score=%.4f, in=%d, out=%d, blast=%d)\n",
			rs.Kind, rs.Name, rs.RiskLevel, rs.Score, rs.InDegree, rs.OutDegree, rs.BlastRadiusSize))
	}

	return sb.String(), nil
}

// resolveIndex picks the index name from metadata or falls back to first available.
func resolveIndex(metadata map[string]interface{}, indexDir string) string {
	if idx, ok := metadata["index"].(string); ok && idx != "" {
		return idx
	}
	indexes, err := gleann.ListIndexes(indexDir)
	if err != nil || len(indexes) == 0 {
		return ""
	}
	return indexes[0].Name
}

// a2aAnalysis returns the analysis view of an index's code graph (SQLite of a
// format-2 index in every build).
func (s *Server) a2aAnalysis(indexName string) (analysisHandle, error) {
	db, err := s.graphPool.get(indexName)
	if err != nil {
		return nil, fmt.Errorf("graph index %q not found: %v", indexName, err)
	}
	h, ok := db.(analysisHandle)
	if !ok {
		return nil, fmt.Errorf("graph index %q does not support analysis", indexName)
	}
	return h, nil
}
