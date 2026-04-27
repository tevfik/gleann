// Package diskann implements a pure Go DiskANN (Vamana) index for
// disk-resident approximate nearest neighbor search on billion-scale datasets.
//
// Key design: graph topology + PQ codes reside in RAM (~100 bytes/vector),
// while raw embeddings live on disk (mmap) and are fetched only for final
// reranking. This gives 8-16x RAM reduction compared to in-memory HNSW.
//
// Algorithm reference: Subramanya et al., "DiskANN: Fast Accurate Billion-point
// Nearest Neighbor Search on a Single Node", NeurIPS 2019.
package diskann

import (
	"context"
	"time"
)

// DistanceFunc computes distance between two vectors.
// Lower values = more similar.
type DistanceFunc func(a, b []float32) float32

// DistanceMetric selects the distance function.
type DistanceMetric string

const (
	DistanceL2     DistanceMetric = "l2"
	DistanceCosine DistanceMetric = "cosine"
)

// DiskANNConfig holds build and search parameters.
type DiskANNConfig struct {
	// R is the max out-degree per node (analogous to HNSW M).
	// Default: 64.
	R int `json:"r"`

	// L is the candidate list size during build (analogous to efConstruction).
	// Default: 100.
	L int `json:"l"`

	// Alpha controls the robust prune diversity parameter.
	// α=1.0 = pure distance, α>1.0 = more diversity. Default: 1.2.
	Alpha float64 `json:"alpha"`

	// PQDim is the number of PQ sub-quantizer dimensions.
	// Each sub-quantizer encodes (dims/PQDim) dimensions into 1 byte.
	// Default: 0 (auto = dims/4, clamped to [1, dims]).
	PQDim int `json:"pq_dim"`

	// PQCentroids is the number of centroids per sub-quantizer.
	// Must be ≤ 256 (stored as uint8). Default: 256.
	PQCentroids int `json:"pq_centroids"`

	// SearchL is the candidate list size during search.
	// Default: 100.
	SearchL int `json:"search_l"`

	// SearchPQRerank is how many PQ candidates to rerank with full vectors.
	// Default: 2 * TopK.
	SearchPQRerank int `json:"search_pq_rerank"`

	// DistanceMetric selects distance function. Default: "l2".
	DistanceMetric DistanceMetric `json:"distance_metric,omitempty"`

	// UseMmap enables memory-mapped search for raw vectors.
	UseMmap bool `json:"use_mmap,omitempty"`
}

// Defaults fills zero-valued fields with sensible defaults.
func (c *DiskANNConfig) Defaults(dims int) {
	if c.R <= 0 {
		c.R = 64
	}
	if c.L <= 0 {
		c.L = 100
	}
	if c.Alpha <= 0 {
		c.Alpha = 1.2
	}
	if c.PQDim <= 0 {
		c.PQDim = dims / 4
		if c.PQDim < 1 {
			c.PQDim = 1
		}
	}
	if c.PQDim > dims {
		c.PQDim = dims
	}
	if c.PQCentroids <= 0 || c.PQCentroids > 256 {
		c.PQCentroids = 256
	}
	if c.SearchL <= 0 {
		c.SearchL = 100
	}
}

// Config is a minimal standalone config (mirrors gleann.Config pattern).
type Config struct {
	IndexDir      string        `json:"index_dir"`
	Backend       string        `json:"backend"`
	DiskANNConfig DiskANNConfig `json:"diskann_config,omitempty"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		IndexDir: ".",
		Backend:  "diskann",
		DiskANNConfig: DiskANNConfig{
			R:              64,
			L:              100,
			Alpha:          1.2,
			PQCentroids:    256,
			SearchL:        100,
			DistanceMetric: DistanceL2,
			UseMmap:        true,
		},
	}
}

// IndexMeta stores metadata about a built index.
type IndexMeta struct {
	Name           string    `json:"name"`
	Backend        string    `json:"backend"`
	EmbeddingModel string    `json:"embedding_model"`
	Dimensions     int       `json:"dimensions"`
	NumPassages    int       `json:"num_passages"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Version        string    `json:"version"`
}

// EmbeddingRecomputer retrieves embeddings for given IDs from storage.
type EmbeddingRecomputer func(ctx context.Context, ids []int64) ([][]float32, error)

// BackendBuilder constructs and serializes an index.
type BackendBuilder interface {
	Build(ctx context.Context, embeddings [][]float32) ([]byte, error)
	AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error)
	RemoveVectors(ctx context.Context, indexData []byte, ids []int64) ([]byte, error)
}

// BackendSearcher loads and queries an index.
type BackendSearcher interface {
	Load(ctx context.Context, indexData []byte, meta IndexMeta) error
	Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error)
	SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error)
	Close() error
}

// Candidate is a (id, distance) pair.
type Candidate struct {
	ID       int64
	Distance float32
}
