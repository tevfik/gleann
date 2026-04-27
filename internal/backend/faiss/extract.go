//go:build cgo && faiss

package faiss

/*
#cgo CXXFLAGS: -I${SRCDIR}/include -I/usr/local/include -std=c++17
#include "faiss_graph.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/tevfik/gleann/modules/hnsw"
)

// ExtractTopology extracts the HNSW graph structure from a FAISS index
// and combines it with the original embeddings to produce a GraphTopology
// that can be fed to hnsw.NewGraphFromTopology.
func ExtractTopology(index *C.FaissIndex, embeddings [][]float32, m int) (hnsw.GraphTopology, error) {
	var raw C.GleannFAISSRawHNSW
	if rc := C.gleann_faiss_extract_graph(index, &raw); rc != 0 {
		return hnsw.GraphTopology{}, fmt.Errorf("gleann_faiss_extract_graph failed (rc=%d); index may not be HNSW", rc)
	}
	defer C.gleann_faiss_free_graph(&raw)

	n := int(raw.levels_len)
	if n == 0 {
		return hnsw.GraphTopology{}, fmt.Errorf("empty graph")
	}
	if n != len(embeddings) {
		return hnsw.GraphTopology{}, fmt.Errorf("node count mismatch: FAISS has %d, embeddings has %d", n, len(embeddings))
	}

	dim := len(embeddings[0])
	maxLevel := int(raw.max_level)
	entryPoint := int64(raw.entry_point)

	// Map C arrays to Go slices (zero-copy; valid until free_graph).
	neighbors := unsafe.Slice((*int64)(unsafe.Pointer(raw.neighbors)), int(raw.neighbors_len))
	offsets := unsafe.Slice((*int64)(unsafe.Pointer(raw.offsets)), int(raw.offsets_len))
	levels := unsafe.Slice((*int32)(unsafe.Pointer(raw.levels)), n)
	cumNLen := int(raw.cum_nneighbor_len)
	cumN := unsafe.Slice((*int32)(unsafe.Pointer(raw.cum_nneighbor)), cumNLen)

	// Build per-node neighbor lists.
	nodeLevels := make([]int, n)
	allNeighbors := make([][][]int64, n)

	for i := 0; i < n; i++ {
		nodeLevel := int(levels[i])
		nodeLevels[i] = nodeLevel
		nodeBase := offsets[i]

		perLevel := make([][]int64, nodeLevel+1)
		for l := 0; l <= nodeLevel; l++ {
			if l+1 >= cumNLen {
				break
			}
			begin := nodeBase + int64(cumN[l])
			end := nodeBase + int64(cumN[l+1])

			var nbrs []int64
			for j := begin; j < end; j++ {
				// Within-bounds check (defensive).
				if j < 0 || j >= int64(len(neighbors)) {
					break
				}
				nbr := neighbors[j]
				if nbr >= 0 { // -1 means empty slot
					nbrs = append(nbrs, nbr)
				}
			}
			perLevel[l] = nbrs
		}
		allNeighbors[i] = perLevel
	}

	return hnsw.GraphTopology{
		NumNodes:   n,
		Dimensions: dim,
		M:          m,
		MaxLevel:   maxLevel,
		EntryPoint: entryPoint,
		NodeLevels: nodeLevels,
		Embeddings: embeddings,
		Neighbors:  allNeighbors,
	}, nil
}
