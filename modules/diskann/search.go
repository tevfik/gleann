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
//   - rerankK: how many PQ candidates to rerank (0 = 2*topK)
//   - getVector: callback to fetch raw embedding (from mmap/disk/recompute)
func SearchDiskIndex(idx *DiskIndex, query []float32, topK, searchL, rerankK int, distFunc DistanceFunc, getVector func(int64) []float32) []Candidate {
	if idx.NumNodes == 0 {
		return nil
	}
	if searchL < topK {
		searchL = topK
	}
	if rerankK <= 0 {
		rerankK = topK * 2
	}
	if rerankK < topK {
		rerankK = topK
	}

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
	visited[idx.Medoid] = true

	medoidDist := ADCDistance(pqTable, idx.PQCodes[idx.Medoid])
	candidates := []Candidate{{ID: idx.Medoid, Distance: medoidDist}}

	// Best-first search: expand closest unvisited candidate.
	expanded := 0

	for expanded < len(candidates) {
		// Find the closest unexpanded candidate.
		bestIdx := -1
		bestDist := float32(1e30)
		for i := expanded; i < len(candidates); i++ {
			if candidates[i].Distance < bestDist {
				bestDist = candidates[i].Distance
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}

		// Move best to the expanded partition.
		candidates[expanded], candidates[bestIdx] = candidates[bestIdx], candidates[expanded]
		current := candidates[expanded]
		expanded++

		// Early termination: if we have L candidates and current is worse than L-th best.
		if len(candidates) >= l {
			sort.Slice(candidates, func(a, b int) bool {
				return candidates[a].Distance < candidates[b].Distance
			})
			if current.Distance > candidates[l-1].Distance {
				break
			}
		}

		// Expand neighbors.
		neighbors := idx.GetNeighbors(current.ID)
		for _, nID := range neighbors {
			if nID < 0 || nID >= idx.NumNodes {
				continue
			}
			if visited[nID] {
				continue
			}
			visited[nID] = true

			dist := ADCDistance(pqTable, idx.PQCodes[nID])
			candidates = append(candidates, Candidate{ID: nID, Distance: dist})
		}

		// Trim to keep candidate list manageable.
		if len(candidates) > l*3 {
			sort.Slice(candidates, func(a, b int) bool {
				return candidates[a].Distance < candidates[b].Distance
			})
			candidates = candidates[:l*2]
			expanded = 0
			for i, c := range candidates {
				if visited[c.ID] {
					// Count all visited nodes as "expanded" conceptually,
					// but we need to re-check if they were actually expanded.
				}
				_ = i
			}
			// Reset expanded counter to re-examine from best.
			expanded = 0
		}
	}

	// Return sorted by PQ distance.
	sort.Slice(candidates, func(a, b int) bool {
		return candidates[a].Distance < candidates[b].Distance
	})
	if len(candidates) > l {
		candidates = candidates[:l]
	}
	return candidates
}

// SearchExact performs beam search with exact distances (all embeddings in RAM).
// This is used when all embeddings fit in memory (small datasets or testing).
func SearchExact(idx *DiskIndex, query []float32, topK, searchL int, distFunc DistanceFunc) []Candidate {
	getVector := func(id int64) []float32 {
		if id >= 0 && id < idx.NumNodes {
			return idx.Embeddings[id]
		}
		return nil
	}
	return SearchDiskIndex(idx, query, topK, searchL, 0, distFunc, getVector)
}
