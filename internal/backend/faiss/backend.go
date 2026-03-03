//go:build cgo && faiss

package faiss

/*
#cgo CFLAGS: -I/usr/local/include
#cgo LDFLAGS: -L/usr/local/lib -lfaiss_c -lfaiss -lstdc++ -lgomp -lopenblas -lm

#include <stdlib.h>
#include <faiss/c_api/faiss_c.h>
#include <faiss/c_api/Index_c.h>
#include <faiss/c_api/IndexFlat_c.h>
#include <faiss/c_api/AutoTune_c.h>
#include <faiss/c_api/index_factory_c.h>
#include <faiss/c_api/index_io_c.h>
#include "faiss_io.h"
*/
import "C"
import (
	"context"
	"fmt"
	"sync"
	"unsafe"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Factory creates FAISS backend builders and searchers.
type Factory struct{}

func (f *Factory) Name() string { return "faiss" }

func (f *Factory) NewBuilder(config gleann.Config) gleann.BackendBuilder {
	return &Builder{config: config}
}

func (f *Factory) NewSearcher(config gleann.Config) gleann.BackendSearcher {
	return &Searcher{config: config}
}

func init() {
	gleann.RegisterBackend(&Factory{})
}

// ──────────────────── Builder ────────────────────

// Builder builds FAISS HNSW indexes via CGo.
type Builder struct {
	config gleann.Config
}

func (b *Builder) Build(ctx context.Context, embeddings [][]float32) ([]byte, error) {
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings to build index from")
	}

	dim := len(embeddings[0])
	n := len(embeddings)

	// Flatten vectors into a contiguous C array.
	flat := flattenVectors(embeddings)

	// Determine M from config.
	m := b.config.HNSWConfig.M
	if m <= 0 {
		m = 32
	}

	// Create HNSW index via index_factory: "HNSW{M}"
	desc := fmt.Sprintf("HNSW%d", m)
	cDesc := C.CString(desc)
	defer C.free(unsafe.Pointer(cDesc))

	var index *C.FaissIndex
	rc := C.faiss_index_factory(&index, C.int(dim), cDesc, C.METRIC_L2)
	if rc != 0 {
		return nil, fmt.Errorf("faiss_index_factory failed: rc=%d", rc)
	}
	defer C.faiss_Index_free(index)

	// Set efConstruction via training (FAISS sets it during build).
	// FAISS HNSW builds the graph during add(), not train().

	// Add vectors.
	rc = C.faiss_Index_add(index, C.idx_t(n), (*C.float)(unsafe.Pointer(&flat[0])))
	if rc != 0 {
		return nil, fmt.Errorf("faiss_Index_add failed: rc=%d", rc)
	}

	// Serialize to memory — no temp files.
	return serializeIndex(index)
}

func (b *Builder) AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error) {
	// Load existing index.
	index, cleanup, err := loadFromBytes(indexData)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	flat := flattenVectors(embeddings)

	rc := C.faiss_Index_add(index, C.idx_t(len(embeddings)), (*C.float)(unsafe.Pointer(&flat[0])))
	if rc != 0 {
		return nil, fmt.Errorf("faiss_Index_add failed: rc=%d", rc)
	}

	return serializeIndex(index)
}

func (b *Builder) RemoveVectors(ctx context.Context, indexData []byte, ids []int64) ([]byte, error) {
	// FAISS HNSW does not support direct removal. Rebuild without removed IDs.
	// For now, return error — this is a known FAISS limitation.
	return nil, fmt.Errorf("FAISS HNSW does not support vector removal; rebuild required")
}

// ──────────────────── Searcher ────────────────────

// Searcher searches FAISS HNSW indexes via CGo.
type Searcher struct {
	config gleann.Config
	index  *C.FaissIndex
	mu     sync.RWMutex
}

func (s *Searcher) Load(ctx context.Context, indexData []byte, meta gleann.IndexMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(indexData) == 0 {
		return fmt.Errorf("empty index data")
	}

	// In-memory load — no temp files.
	var newIndex *C.FaissIndex
	rc := C.gleann_faiss_read_buf(
		(*C.uint8_t)(unsafe.Pointer(&indexData[0])),
		C.size_t(len(indexData)),
		&newIndex,
	)
	if rc != 0 {
		return fmt.Errorf("gleann_faiss_read_buf failed: rc=%d", rc)
	}

	// Free previous index if any.
	if s.index != nil {
		C.faiss_Index_free(s.index)
	}
	s.index = newIndex

	// Set efSearch via ParameterSpace (FAISS default is 16, which gives poor recall).
	efSearch := s.config.HNSWConfig.EfSearch
	if efSearch <= 0 {
		efSearch = 128
	}
	var ps *C.FaissParameterSpace
	if C.faiss_ParameterSpace_new(&ps) == 0 {
		cParam := C.CString("efSearch")
		C.faiss_ParameterSpace_set_index_parameter(ps, s.index, cParam, C.double(efSearch))
		C.free(unsafe.Pointer(cParam))
		C.faiss_ParameterSpace_free(ps)
	}

	return nil
}

func (s *Searcher) Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.index == nil {
		return nil, nil, fmt.Errorf("no index loaded")
	}

	// Allocate output arrays.
	distances := make([]float32, topK)
	labels := make([]int64, topK)

	rc := C.faiss_Index_search(
		s.index,
		1, // n queries
		(*C.float)(unsafe.Pointer(&query[0])),
		C.idx_t(topK),
		(*C.float)(unsafe.Pointer(&distances[0])),
		(*C.idx_t)(unsafe.Pointer(&labels[0])),
	)
	if rc != 0 {
		return nil, nil, fmt.Errorf("faiss_Index_search failed: rc=%d", rc)
	}

	// Filter out -1 labels (no result).
	validIDs := make([]int64, 0, topK)
	validDists := make([]float32, 0, topK)
	for i := 0; i < topK; i++ {
		if labels[i] >= 0 {
			validIDs = append(validIDs, labels[i])
			validDists = append(validDists, distances[i])
		}
	}

	return validIDs, validDists, nil
}

func (s *Searcher) SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute gleann.EmbeddingRecomputer) ([]int64, []float32, error) {
	// FAISS doesn't support selective recomputation natively.
	// Fall back to standard search (all embeddings are stored in FAISS).
	return s.Search(ctx, query, topK)
}

func (s *Searcher) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.index != nil {
		C.faiss_Index_free(s.index)
		s.index = nil
	}
	return nil
}

// ──────────────────── Helpers ────────────────────

// flattenVectors converts [][]float32 to a contiguous []float32 for C.
func flattenVectors(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	flat := make([]float32, len(vecs)*dim)
	for i, v := range vecs {
		copy(flat[i*dim:], v)
	}
	return flat
}

// loadFromBytes loads a FAISS index from byte slice via in-memory I/O.
func loadFromBytes(data []byte) (*C.FaissIndex, func(), error) {
	if len(data) == 0 {
		return nil, nil, fmt.Errorf("empty index data")
	}
	var index *C.FaissIndex
	rc := C.gleann_faiss_read_buf(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		&index,
	)
	if rc != 0 {
		return nil, nil, fmt.Errorf("gleann_faiss_read_buf failed: rc=%d", rc)
	}
	cleanup := func() {
		C.faiss_Index_free(index)
	}
	return index, cleanup, nil
}

// serializeIndex saves a FAISS index to bytes via in-memory I/O.
func serializeIndex(index *C.FaissIndex) ([]byte, error) {
	var outBuf *C.uint8_t
	var outSize C.size_t

	rc := C.gleann_faiss_write_buf(index, &outBuf, &outSize)
	if rc != 0 {
		return nil, fmt.Errorf("gleann_faiss_write_buf failed: rc=%d", rc)
	}
	defer C.free(unsafe.Pointer(outBuf))

	// Copy C-owned memory into a Go slice.
	data := C.GoBytes(unsafe.Pointer(outBuf), C.int(outSize))
	return data, nil
}

// BatchSearch performs search for multiple query vectors at once,
// leveraging FAISS's optimized batch operations.
func BatchSearch(index *C.FaissIndex, queries [][]float32, topK int) ([][]int64, [][]float32, error) {
	n := len(queries)
	if n == 0 {
		return nil, nil, nil
	}

	dim := len(queries[0])
	flat := make([]float32, n*dim)
	for i, q := range queries {
		copy(flat[i*dim:], q)
	}

	distances := make([]float32, n*topK)
	labels := make([]int64, n*topK)

	rc := C.faiss_Index_search(
		index,
		C.idx_t(n),
		(*C.float)(unsafe.Pointer(&flat[0])),
		C.idx_t(topK),
		(*C.float)(unsafe.Pointer(&distances[0])),
		(*C.idx_t)(unsafe.Pointer(&labels[0])),
	)
	if rc != 0 {
		return nil, nil, fmt.Errorf("faiss_Index_search batch failed: rc=%d", rc)
	}

	// Reshape results.
	allIDs := make([][]int64, n)
	allDists := make([][]float32, n)
	for i := 0; i < n; i++ {
		ids := make([]int64, 0, topK)
		dists := make([]float32, 0, topK)
		for j := 0; j < topK; j++ {
			idx := i*topK + j
			if labels[idx] >= 0 {
				ids = append(ids, labels[idx])
				dists = append(dists, distances[idx])
			}
		}
		allIDs[i] = ids
		allDists[i] = dists
	}

	return allIDs, allDists, nil
}
