package hnsw

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/tevfik/gleann/pkg/gleann"
)

// MmapSearcher implements BackendSearcher directly on a memory-mapped graph.
type MmapSearcher struct {
	config    gleann.Config
	csrFile   string // We keep the path so we can mmap it.
	mmapGraph *MmapGraph
	mu        sync.RWMutex
}

func (s *MmapSearcher) Load(ctx context.Context, indexData []byte, meta gleann.IndexMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// In the gleann.BackendSearcher paradigm, Load gets `indexData []byte`.
	// For zero-copy, we ideally want to bypass loading bytes into RAM.
	// But to conform with `Load([]byte)`, if we are passed bytes, we must write them to index.csr and then mmap it,
	// or provide a new LoadFromFile function in the interface.

	// Assuming the index file is located at s.config.IndexDir + "/index.csr"
	// For this prototype, we'll try to mmap the underlying file directly if we know it.
	// We'll write a small logic down here that mmaps the standard path.

	// (Note: To perfectly fit gleann's BackendSearcher API which passes []byte, a real production
	// version might alter the API to allow `LoadFromFile(path string)`. Here we'll simulate mapping.)

	return fmt.Errorf("MmapSearcher requires LoadFromFile to mmap")
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
	// Not supporting `Search` without recompute if it's strictly a pruned graph,
	// unless we stored all embeddings (keep_fraction=1.0).
	return nil, nil, fmt.Errorf("MmapSearcher is optimized for SearchWithRecompute")
}

func (s *MmapSearcher) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute gleann.EmbeddingRecomputer) ([]int64, []float32, error) {
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

	recomputeSync := func(id int64) []float32 {
		vecs, err := recompute(ctx, []int64{id})
		if err != nil || len(vecs) == 0 {
			return nil
		}
		return vecs[0]
	}

	candidates := s.searchZeroCopy(query, topK, ef, distFunc, recomputeSync)

	ids := make([]int64, len(candidates))
	distances := make([]float32, len(candidates))
	for i, c := range candidates {
		// Convert internal index to external ID!
		ids[i] = s.mmapGraph.GetExternalID(c.ID)
		distances[i] = c.Distance
	}

	return ids, distances, nil
}

func (s *MmapSearcher) searchZeroCopy(query []float32, topK, ef int, distFunc DistanceFunc, recompute func(int64) []float32) []Candidate {
	epIdx := s.mmapGraph.EntryPoint
	maxLevel := s.mmapGraph.MaxLevel

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
		vec = recompute(extID)
		if vec != nil {
			embeddingCache[idx] = vec
		}
		return vec
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
