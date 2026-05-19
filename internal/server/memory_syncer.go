//go:build treesitter

package server

import (
	"github.com/tevfik/gleann/internal/embedding"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// initMemorySyncer configures the VectorSyncer factory on the memoryPool so
// that Memory Engine entity writes are automatically reflected in the HNSW+BM25
// vector index.  Called once from NewServer.
func (s *Server) initMemorySyncer(config gleann.Config, embedder *embedding.Computer) {
	if embedder == nil || config.EmbeddingModel == "" || s.memoryPool == nil {
		return
	}
	s.memoryPool.syncerFactory = func(name string) kgraph.VectorSyncer {
		builder, err := gleann.NewBuilder(config, embedder)
		if err != nil {
			return nil
		}
		return kgraph.NewLeannSyncer(builder, name+"_memory_vectors", embedder)
	}
}
