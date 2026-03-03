// Package hnsw provides a pure-Go HNSW (Hierarchical Navigable Small World)
// vector index. It is designed as a standalone, dependency-free module that can
// be used as a drop-in replacement for CGO-based vector libraries.
package hnsw

import (
	"context"
	"encoding/json"
	"time"
)

// DistanceMetric specifies the distance function for vector comparison.
type DistanceMetric string

const (
	DistanceL2     DistanceMetric = "l2"
	DistanceCosine DistanceMetric = "cosine"
	DistanceIP     DistanceMetric = "ip"
)

// HNSWConfig holds HNSW-specific build and search parameters.
type HNSWConfig struct {
	M                 int            `json:"m"`
	EfConstruction    int            `json:"ef_construction"`
	EfSearch          int            `json:"ef_search"`
	UseMmap           bool           `json:"use_mmap,omitempty"`
	MaxLevel          int            `json:"max_level,omitempty"`
	DistanceMetric    DistanceMetric `json:"distance_metric,omitempty"`
	UseHeuristic      bool           `json:"use_heuristic"`
	PruneEmbeddings   bool           `json:"prune_embeddings"`
	PruneKeepFraction float64        `json:"prune_keep_fraction"`
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

// MarshalJSON implements custom JSON marshaling for IndexMeta.
func (m IndexMeta) MarshalJSON() ([]byte, error) {
	type Alias IndexMeta
	return json.Marshal(&struct {
		Alias
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}{
		Alias:     (Alias)(m),
		CreatedAt: m.CreatedAt.Format(time.RFC3339),
		UpdatedAt: m.UpdatedAt.Format(time.RFC3339),
	})
}

// Config is a minimal config struct carrying HNSW settings.
// In the main gleann application this is the gleann.Config struct;
// here we define a local equivalent to keep gleann-hnsw dependency-free.
type Config struct {
	IndexDir   string     `json:"index_dir"`
	Backend    string     `json:"backend"`
	HNSWConfig HNSWConfig `json:"hnsw_config,omitempty"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		IndexDir: ".",
		Backend:  "hnsw",
		HNSWConfig: HNSWConfig{
			M:                 32,
			EfConstruction:    200,
			EfSearch:          128,
			UseMmap:           true,
			UseHeuristic:      true,
			PruneEmbeddings:   true,
			PruneKeepFraction: 0.0,
		},
	}
}

// EmbeddingRecomputer is a function that recomputes embeddings for given passage IDs.
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

// MmapBackendSearcher optionally supports zero-copy memory-mapped search.
type MmapBackendSearcher interface {
	BackendSearcher
	LoadFromFile(ctx context.Context, path string) error
}
