package diskann

import (
	"sort"
)

// SearchDiskIndex performs DiskANN's beam search with PQ prefiltering.
//
// Algorithm:
//  1. Beam search on graph using PQ approximate distances (cheap, RAM-only).
//  2. Collect top-L candidates by PQ distance.
//  3. Rerank top candidates using exact distances (expensive, disk read).
//
// Parameters:
//   - query: the query vector
//   - topK: number of results to return
//   - searchL: candidate list size (beam width)
//   - rerankK: how many PQ candidates to rerank with exact distances
//     (0 = DefaultRerankK: all searchL candidates)
//   - getVector: callback to fetch raw embedding (from mmap/disk/recompute)
func SearchDiskIndex(idx *DiskIndex, query []float32, topK, searchL, rerankK int, distFunc DistanceFunc, getVector func(int64) []float32) []Candidate {
	if idx.NumNodes == 0 {
		return nil
	}
	if searchL < topK {
		searchL = topK
	}
	rerankK = DefaultRerankK(rerankK, topK, searchL)

	// Precompute PQ distance table for this query: O(M*K) = fast.
	pqTable := idx.Codebook.BuildDistanceTable(query, distFunc)

	// Beam search using PQ distances (no disk reads).
	candidates := beamSearchPQ(idx, query, searchL, pqTable)

	// Rerank top candidates with exact distances.
	if len(candidates) > rerankK {
		candidates = candidates[:rerankK]
	}

	reranked := make([]Candidate, 0, len(candidates))
	for _, c := range candidates {
		vec := getVector(c.ID)
		if vec == nil {
			continue
		}
		reranked = append(reranked, Candidate{
			ID:       c.ID,
			Distance: distFunc(query, vec),
		})
	}

	sort.Slice(reranked, func(i, j int) bool {
		return reranked[i].Distance < reranked[j].Distance
	})
	if len(reranked) > topK {
		reranked = reranked[:topK]
	}
	return reranked
}

// beamSearchPQ performs greedy beam search using PQ approximate distances.
// This is the core DiskANN search — it only reads graph neighbors and PQ codes
// from RAM, never touching raw embeddings.
func beamSearchPQ(idx *DiskIndex, query []float32, l int, pqTable [][]float32) []Candidate {
	visited := make(map[int64]bool)
	expanded := make(map[int64]bool)

	medoidDist := ADCDistance(pqTable, idx.PQCodes[idx.Medoid])
	candidates := []Candidate{{ID: idx.Medoid, Distance: medoidDist}}
	visited[idx.Medoid] = true

	for {
		// Find closest unexpanded candidate in beam
		bestIdx := -1
		bestDist := float32(1e30)
		for i, c := range candidates {
			if !expanded[c.ID] && c.Distance < bestDist {
				bestDist = c.Distance
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break // all candidates in beam expanded
		}

		current := candidates[bestIdx]
		expanded[current.ID] = true

		neighbors := idx.GetNeighbors(current.ID)
		for _, nID := range neighbors {
			if nID < 0 || nID >= idx.NumNodes || visited[nID] {
				continue
			}
			visited[nID] = true

			dist := ADCDistance(pqTable, idx.PQCodes[nID])
			candidates = append(candidates, Candidate{ID: nID, Distance: dist})
		}

		if len(candidates) > l*2 {
			sort.Slice(candidates, func(a, b int) bool {
				return candidates[a].Distance < candidates[b].Distance
			})
			candidates = candidates[:l]
		}
	}

	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].Distance < candidates[b].Distance
	})
	if len(candidates) > l {
		candidates = candidates[:l]
	}
	return candidates
}

// DefaultRerankK is the number of PQ candidates reranked with exact distances:
// the configured value, or else all searchL candidates of the beam search.
// It is at least topK.
//
// The old default, 2*topK, capped recall@10 at about 0.91 whatever searchL
// was: PQ distances misorder close neighbours, and only the first 20 by PQ
// distance got an exact distance. Reranking the whole list costs searchL
// exact distance computations, which is small next to the beam search
// (bench/vectordb, 10K x 384: 0.919 -> 0.998 at searchL 100).
func DefaultRerankK(configured, topK, searchL int) int {
	k := configured
	if k <= 0 {
		k = searchL
	}
	if k < topK {
		k = topK
	}
	return k
}

// SearchExact performs beam search with exact distances (from in-memory or mmap embeddings).
func SearchExact(idx *DiskIndex, query []float32, topK, searchL int, distFunc DistanceFunc) []Candidate {
	return SearchExactRerank(idx, query, topK, searchL, 0, distFunc)
}

// SearchExactRerank is SearchExact with an explicit rerank width (0 =
// DefaultRerankK).
func SearchExactRerank(idx *DiskIndex, query []float32, topK, searchL, rerankK int, distFunc DistanceFunc) []Candidate {
	getVector := func(id int64) []float32 {
		return idx.GetVector(id)
	}
	return SearchDiskIndex(idx, query, topK, searchL, rerankK, distFunc, getVector)
}
