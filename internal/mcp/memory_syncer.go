//go:build treesitter

package mcp

import (
	"github.com/tevfik/gleann/internal/embedding"
	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// wireMemorySyncer configures the VectorSyncer factory on the memPool so that
// Memory Engine entity writes are automatically reflected in the vector index.
func (srv *Server) wireMemorySyncer(cfg Config, glCfg gleann.Config, embedder *embedding.Computer) {
	if cfg.EmbeddingModel == "" || srv.memPool == nil {
		return
	}
	srv.memPool.syncerFactory = func(name string) kgraph.VectorSyncer {
		builder, err := gleann.NewBuilder(glCfg, embedder)
		if err != nil {
			return nil
		}
		return kgraph.NewLeannSyncer(builder, name+"_memory_vectors", embedder)
	}
}
