package hnsw

import (
	"bytes"
	"context"
	"fmt"
	"sync"
)

// Factory creates HNSW backend builders and searchers.
type Factory struct{}

func (f *Factory) Name() string { return "hnsw" }

func (f *Factory) NewBuilder(config Config) BackendBuilder {
	return &Builder{config: config}
}

func (f *Factory) NewSearcher(config Config) BackendSearcher {
	if config.HNSWConfig.UseMmap {
		return &MmapSearcher{config: config}
	}
	return &Searcher{config: config}
}

func init() {
	// gleann.RegisterBackend removed (standalone module)
}

// Builder builds HNSW indexes.
type Builder struct {
	config Config
}

func (b *Builder) Build(ctx context.Context, embeddings [][]float32) ([]byte, error) {
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings to build index from")
	}

	dims := len(embeddings[0])
	hc := b.config.HNSWConfig

	m := hc.M
	if m <= 0 {
		m = 32
	}
	efConstruction := hc.EfConstruction
	if efConstruction <= 0 {
		efConstruction = 200
	}

	distFunc := GetDistanceFunc(string(hc.DistanceMetric))
	graph := NewGraphWithOptions(m, efConstruction, dims, distFunc, hc.UseHeuristic)

	for i, emb := range embeddings {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		graph.Insert(int64(i), emb)
	}

	// Convert to CSR format.
	csr := ConvertToCSR(graph)

	// Prune embeddings if configured.
	if hc.PruneEmbeddings {
		PruneEmbeddings(graph, csr, hc.PruneKeepFraction)
	} else {
		// Keep all embeddings.
		PruneEmbeddings(graph, csr, 1.0)
	}

	// Serialize.
	var buf bytes.Buffer
	if _, err := csr.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize index: %w", err)
	}

	return buf.Bytes(), nil
}

func (b *Builder) AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error) {
	// Load existing index.
	csr, err := ReadCSR(bytes.NewReader(indexData))
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}

	distFunc := GetDistanceFunc(string(b.config.HNSWConfig.DistanceMetric))
	graph := csr.ToGraphWithDistance(distFunc)

	// Add new vectors.
	for i, emb := range embeddings {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		graph.Insert(startID+int64(i), emb)
	}

	// Re-convert.
	newCSR := ConvertToCSR(graph)
	hc := b.config.HNSWConfig
	if hc.PruneEmbeddings {
		PruneEmbeddings(graph, newCSR, hc.PruneKeepFraction)
	} else {
		PruneEmbeddings(graph, newCSR, 1.0)
	}

	var buf bytes.Buffer
	if _, err := newCSR.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize index: %w", err)
	}

	return buf.Bytes(), nil
}

func (b *Builder) RemoveVectors(ctx context.Context, indexData []byte, ids []int64) ([]byte, error) {
	// Load, rebuild without the removed IDs.
	csr, err := ReadCSR(bytes.NewReader(indexData))
	if err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}

	distFunc := GetDistanceFunc(string(b.config.HNSWConfig.DistanceMetric))
	graph := csr.ToGraphWithDistance(distFunc)

	// Remove is expensive — rebuild the graph without those nodes.
	removeSet := make(map[int64]bool, len(ids))
	for _, id := range ids {
		removeSet[id] = true
	}

	// Collect remaining nodes.
	remainingNodes := make(map[int64]*Node)
	for id, node := range graph.nodes {
		if !removeSet[id] {
			remainingNodes[id] = node
		}
	}

	// Rebuild graph with remaining nodes.
	newGraph := NewGraphWithOptions(graph.m, graph.efConstruction, graph.dimensions, distFunc, graph.useHeuristic)
	for id, node := range remainingNodes {
		if len(node.Vector) > 0 {
			newGraph.Insert(id, node.Vector)
		}
	}

	newCSR := ConvertToCSR(newGraph)
	hc := b.config.HNSWConfig
	if hc.PruneEmbeddings {
		PruneEmbeddings(newGraph, newCSR, hc.PruneKeepFraction)
	} else {
		PruneEmbeddings(newGraph, newCSR, 1.0)
	}

	var buf bytes.Buffer
	if _, err := newCSR.WriteTo(&buf); err != nil {
		return nil, fmt.Errorf("serialize index: %w", err)
	}

	return buf.Bytes(), nil
}

// Searcher searches an HNSW index.
type Searcher struct {
	config Config
	graph  *Graph
	csr    *CSRGraph
	mu     sync.RWMutex
}

func (s *Searcher) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	csr, err := ReadCSR(bytes.NewReader(indexData))
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	distFunc := GetDistanceFunc(string(s.config.HNSWConfig.DistanceMetric))
	s.csr = csr
	s.graph = csr.ToGraphWithDistance(distFunc)
	return nil
}

func (s *Searcher) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.graph == nil {
		return nil, nil, fmt.Errorf("no index loaded")
	}

	ef := s.config.HNSWConfig.EfSearch
	if ef <= 0 {
		ef = 128
	}
	if ef < topK {
		ef = topK
	}

	candidates := s.graph.Search(query, topK, ef)

	ids := make([]int64, len(candidates))
	distances := make([]float32, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
		distances[i] = c.Distance
	}

	return ids, distances, nil
}

func (s *Searcher) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.graph == nil {
		return nil, nil, fmt.Errorf("no index loaded")
	}

	ef := s.config.HNSWConfig.EfSearch
	if ef <= 0 {
		ef = 128
	}
	if ef < topK {
		ef = topK
	}

	recomputeSync := func(ids []int64) [][]float32 {
		vecs, err := recompute(ctx, ids)
		if err != nil {
			return nil
		}
		return vecs
	}

	candidates := s.graph.SearchWithRecompute(query, topK, ef, recomputeSync)

	ids := make([]int64, len(candidates))
	distances := make([]float32, len(candidates))
	for i, c := range candidates {
		ids[i] = c.ID
		distances[i] = c.Distance
	}

	return ids, distances, nil
}

func (s *Searcher) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.graph = nil
	s.csr = nil
	return nil
}
