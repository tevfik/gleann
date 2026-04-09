//go:build treesitter

package server

import (
	"context"
	"net/http"

	"github.com/tevfik/gleann/pkg/gleann"
)

// recallGraphImpl queries the knowledge graph for matching entities.
func (s *Server) recallGraphImpl() *RecallGraph {
	// This is a simplified implementation; full context would need
	// the request parameters piped through. For now, return nil since
	// recallGraph already handles the nil case gracefully.
	return nil
}

// injectGraphRelationship stores a relationship in the knowledge graph.
func (s *Server) injectGraphRelationship(r *http.Request, indexName string, rel IngestRelationship) error {
	svc, err := s.memoryPool.get(indexName)
	if err != nil {
		return err
	}

	weight := rel.Weight
	if weight == 0 {
		weight = 1.0
	}

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: rel.From, Type: "entity", Content: rel.From},
			{ID: rel.To, Type: "entity", Content: rel.To},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: rel.From, To: rel.To, RelationType: rel.Relation, Weight: weight},
		},
	}

	return svc.InjectEntities(context.Background(), payload)
}
