package hnsw

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MmapSearcher implements BackendSearcher directly on a memory-mapped graph.
type MmapSearcher struct {
	config    Config
	mmapGraph *MmapGraph
	mu        sync.RWMutex
}

func (s *MmapSearcher) Load(ctx context.Context, indexData []byte, meta IndexMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mg, err := OpenMmapGraphFromData(indexData)
	if err != nil {
		return err
	}
	s.mmapGraph = mg
	return nil
}

// LoadFromFile allows mmapping the index directly bypassing bytes parameter.
func (s *MmapSearcher) LoadFromFile(ctx context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	mg, err := OpenMmapGraph(path)
	if err != nil {
		return err
	}
	s.mmapGraph = mg
	return nil
}

func (s *MmapSearcher) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	// Search using stored embeddings (when stored, e.g. keepFraction=1.0 or unpruned).
	return s.SearchWithRecompute(ctx, query, topK, func(ctx context.Context, ids []int64) ([][]float32, error) {
		return make([][]float32, len(ids)), nil
	})
}

// VectorDistances computes the distance from the query vector to each of the
// given passage IDs using the memory-mapped stored embeddings on the fly.
func (s *MmapSearcher) VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.mmapGraph == nil {
		return nil, fmt.Errorf("no index loaded")
	}
	if len(query) != s.mmapGraph.Dimensions {
		return nil, fmt.Errorf("query has %d dimensions, index has %d", len(query), s.mmapGraph.Dimensions)
	}
	metric := s.config.HNSWConfig.DistanceMetric
	out := make([]float32, len(ids))
	for i, id := range ids {
		dist, ok := s.mmapGraph.DistanceToStoredEmbedding(query, id, metric)
		if !ok {
			return nil, fmt.Errorf("hnsw: passage %d has pruned embeddings", id)
		}
		out[i] = dist
	}
	return out, nil
}

func (s *MmapSearcher) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.mmapGraph == nil {
		return nil, nil, fmt.Errorf("mmap graph not loaded")
	}

	distFunc := GetDistanceFunc(string(s.config.HNSWConfig.DistanceMetric))
	ef := s.config.HNSWConfig.EfSearch
	if ef <= 0 {
		ef = 128
	}
	if ef < topK {
		ef = topK
	}

	// Embedding failures are recorded and returned: skipping the node would
	// give a silently degraded result (E-04).
	var recomputeErr error
	recomputeBatch := func(ids []int64) [][]float32 {
		if recomputeErr != nil {
			return make([][]float32, len(ids))
		}
		vecs, err := recompute(ctx, ids)
		if err == nil && len(vecs) != len(ids) {
			err = fmt.Errorf("embedder returned %d vectors for %d nodes", len(vecs), len(ids))
		}
		if err != nil {
			recomputeErr = fmt.Errorf("recompute embeddings: %w", err)
			return make([][]float32, len(ids))
		}
		return vecs
	}

	candidates := s.searchZeroCopy(query, topK, ef, distFunc, recomputeBatch)
	if recomputeErr != nil {
		return nil, nil, recomputeErr
	}

	ids := make([]int64, len(candidates))
	distances := make([]float32, len(candidates))
	for i, c := range candidates {
		// Convert internal index to external ID!
		ids[i] = s.mmapGraph.GetExternalID(c.ID)
		distances[i] = c.Distance
	}

	return ids, distances, nil
}

func (s *MmapSearcher) searchZeroCopy(query []float32, topK, ef int, distFunc DistanceFunc, recompute func([]int64) [][]float32) []Candidate {
	epIdx := s.mmapGraph.internalIndex(s.mmapGraph.EntryPoint)
	maxLevel := s.mmapGraph.MaxLevel
	if epIdx < 0 {
		return nil
	}

	embeddingCache := make(map[int64][]float32)
	getEmbedding := func(idx int64) []float32 {
		if vec, ok := embeddingCache[idx]; ok {
			return vec
		}
		// Try get stored
		extID := s.mmapGraph.GetExternalID(idx)
		vec := s.mmapGraph.GetStoredEmbedding(extID)
		if vec != nil {
			embeddingCache[idx] = vec
			return vec
		}
		// Recompute uses external ID
		vec = recompute([]int64{extID})[0]
		if vec != nil {
			embeddingCache[idx] = vec
		}
		return vec
	}
	// prefetch recomputes every neighbour that is not cached or stored in one
	// batched call instead of one request per node.
	prefetch := func(idxs []int64) {
		var missing, extIDs []int64
		for _, idx := range idxs {
			if _, ok := embeddingCache[idx]; ok {
				continue
			}
			extID := s.mmapGraph.GetExternalID(idx)
			if vec := s.mmapGraph.GetStoredEmbedding(extID); vec != nil {
				embeddingCache[idx] = vec
				continue
			}
			missing = append(missing, idx)
			extIDs = append(extIDs, extID)
		}
		if len(missing) == 0 {
			return
		}
		vecs := recompute(extIDs)
		for i, idx := range missing {
			if vecs[i] != nil {
				embeddingCache[idx] = vecs[i]
			}
		}
	}

	// Traverse top down
	for lc := maxLevel; lc > 0; lc-- {
		lvl := s.mmapGraph.GetNodeLevel(epIdx)
		if lc > lvl {
			continue
		}

		epVec := getEmbedding(epIdx)
		if epVec == nil {
			continue
		}

		changed := true
		for changed {
			changed = false
			neighbors := s.mmapGraph.GetNeighbors(epIdx, lc)
			prefetch(neighbors)

			for _, nIdx := range neighbors {
				nVec := getEmbedding(nIdx)
				if nVec == nil {
					continue
				}
				if distFunc(query, nVec) < distFunc(query, epVec) {
					epIdx = nIdx
					epVec = nVec
					changed = true
				}
			}
		}
	}

	// Search level 0
	visited := make(map[int64]bool)
	visited[epIdx] = true

	epVec := getEmbedding(epIdx)
	if epVec == nil {
		return nil
	}
	epDist := distFunc(query, epVec)

	candidates := &candidateHeap{
		items: []Candidate{{ID: epIdx, Distance: epDist}},
		isMin: true,
	}

	results := &candidateHeap{
		items: []Candidate{{ID: epIdx, Distance: epDist}},
		isMin: false,
	}

	for candidates.Len() > 0 {
		current := candidates.Pop()

		if results.Len() >= ef && current.Distance > results.Peek().Distance {
			break
		}

		neighbors := s.mmapGraph.GetNeighbors(current.ID, 0)
		unvisited := make([]int64, 0, len(neighbors))
		for _, nIdx := range neighbors {
			if !visited[nIdx] {
				unvisited = append(unvisited, nIdx)
			}
		}
		prefetch(unvisited)
		for _, nIdx := range neighbors {
			if visited[nIdx] {
				continue
			}
			visited[nIdx] = true

			nVec := getEmbedding(nIdx)
			if nVec == nil {
				continue
			}

			dist := distFunc(query, nVec)

			if results.Len() < ef || dist < results.Peek().Distance {
				candidates.Push(Candidate{ID: nIdx, Distance: dist})
				results.Push(Candidate{ID: nIdx, Distance: dist})

				if results.Len() > ef {
					results.Pop()
				}
			}
		}
	}

	sortedResults := results.items
	sort.Slice(sortedResults, func(i, j int) bool {
		return sortedResults[i].Distance < sortedResults[j].Distance
	})

	if len(sortedResults) > topK {
		sortedResults = sortedResults[:topK]
	}
	return sortedResults
}

func (s *MmapSearcher) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mmapGraph != nil {
		return s.mmapGraph.Close()
	}
	return nil
}
