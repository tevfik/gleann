//go:build cgo && faiss

package faiss

/*
#include <faiss/c_api/Index_c.h>
*/
import "C"
import (
	"bytes"
	"context"
	"fmt"

	"github.com/tevfik/gleann/modules/hnsw"
	"github.com/tevfik/gleann/pkg/gleann"
)

// HybridBuilder builds indexes using FAISS's SIMD-accelerated HNSW
// construction (16-45x faster) and then exports the graph topology
// into CSR format with embedding pruning — giving both build speed
// and the storage/recompute advantages of the pure-Go backend.
//
// The output format is CSR (identical to the "hnsw" backend), so
// the standard Go HNSW Searcher handles search + SearchWithRecompute.
type HybridBuilder struct {
	config gleann.Config
}

// NewHybridBuilder creates a HybridBuilder. Exported so the registration
// adapter in pkg/backends can call it.
func NewHybridBuilder(config gleann.Config) gleann.BackendBuilder {
	return &HybridBuilder{config: config}
}

func (b *HybridBuilder) Build(ctx context.Context, embeddings [][]float32) ([]byte, error) {
	m := b.config.HNSWConfig.M
	if m <= 0 {
		m = 32
	}

	// ── 1. Build the FAISS HNSW index (fast, SIMD-accelerated). ──
	// Always use flat HNSW for graph extraction (PQ/SQ changes
	// internal representation and doesn't help the hybrid path).
	index, err := buildFAISSIndex(embeddings, m, gleann.FAISSConfig{})
	if err != nil {
		return nil, fmt.Errorf("faiss hybrid build: %w", err)
	}
	defer C.faiss_Index_free(index)

	// ── 2. Extract HNSW topology from FAISS. ──
	topo, err := ExtractTopology(index, embeddings, m)
	if err != nil {
		return nil, fmt.Errorf("faiss hybrid extract: %w", err)
	}

	// ── 3. Reconstruct Go HNSW graph from topology + embeddings. ──
	distFunc := hnsw.GetDistanceFunc(string(b.config.HNSWConfig.DistanceMetric))
	graph := hnsw.NewGraphFromTopology(topo, distFunc)

	// ── 4. Convert to CSR with embedding pruning. ──
	csr := hnsw.ConvertToCSR(graph)
	if b.config.HNSWConfig.PruneEmbeddings {
		hnsw.PruneEmbeddings(graph, csr, b.config.HNSWConfig.PruneKeepFraction)
	} else {
		hnsw.PruneEmbeddings(graph, csr, 1.0) // keep all
	}

	// ── 5. Serialize CSR. ──
	var buf bytes.Buffer
	if _, err := csr.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("faiss hybrid serialize: %w", err)
	}
	return buf.Bytes(), nil
}

func (b *HybridBuilder) AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error) {
	// The hybrid format stores pruned CSR — original embeddings are not
	// recoverable. A full rebuild is required (and fast, thanks to FAISS).
	return nil, fmt.Errorf("faiss-hybrid: AddVectors requires full rebuild (FAISS build is 16-45x faster than Go, so rebuilding is practical)")
}

func (b *HybridBuilder) RemoveVectors(ctx context.Context, indexData []byte, ids []int64) ([]byte, error) {
	return nil, fmt.Errorf("faiss-hybrid: RemoveVectors requires full rebuild")
}
