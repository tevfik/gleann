// Package gleann provides a lightweight vector database with graph-based
// selective recomputation for 97% storage reduction compared to traditional
// vector databases.
package gleann

import (
	"encoding/json"
	"time"
)

// SearchResult represents a single search result with score and metadata.
type SearchResult struct {
	ID       int64              `json:"id"`
	Text     string             `json:"text"`
	Score    float32            `json:"score"`
	Metadata map[string]any     `json:"metadata,omitempty"`
}

// Item represents a text item to be indexed.
type Item struct {
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Passage represents a stored text passage with its offset information.
type Passage struct {
	ID       int64          `json:"id"`
	Text     string         `json:"text"`
	Metadata map[string]any `json:"metadata,omitempty"`
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

// Config holds configuration for building and searching indexes.
type Config struct {
	// IndexDir is the directory where indexes are stored.
	IndexDir string `json:"index_dir"`

	// Backend is the backend to use (e.g., "hnsw", "ivf").
	Backend string `json:"backend"`

	// EmbeddingModel is the model to use for embedding computation.
	EmbeddingModel string `json:"embedding_model"`

	// EmbeddingProvider specifies the embedding provider ("ollama", "openai", "sentence-transformers").
	EmbeddingProvider string `json:"embedding_provider"`

	// OllamaHost is the Ollama server address.
	OllamaHost string `json:"ollama_host,omitempty"`

	// OpenAIBaseURL is the OpenAI API base URL.
	OpenAIBaseURL string `json:"openai_base_url,omitempty"`

	// OpenAIAPIKey is the OpenAI API key.
	OpenAIAPIKey string `json:"openai_api_key,omitempty"`

	// BuildPromptTemplate is prepended to text during indexing (e.g., "passage: ").
	BuildPromptTemplate string `json:"build_prompt_template,omitempty"`

	// QueryPromptTemplate is prepended to queries during search (e.g., "query: ").
	QueryPromptTemplate string `json:"query_prompt_template,omitempty"`

	// HNSW-specific parameters
	HNSWConfig HNSWConfig `json:"hnsw_config,omitempty"`

	// Search parameters
	SearchConfig SearchConfig `json:"search_config,omitempty"`

	// Chunk parameters
	ChunkConfig ChunkConfig `json:"chunk_config,omitempty"`
}

// DistanceMetric specifies the distance function for vector comparison.
type DistanceMetric string

const (
	// DistanceL2 is squared Euclidean distance (default).
	DistanceL2 DistanceMetric = "l2"
	// DistanceCosine is cosine distance (1 - cosine_similarity).
	DistanceCosine DistanceMetric = "cosine"
	// DistanceIP is inner product distance (negative dot product).
	DistanceIP DistanceMetric = "ip"
)

// HNSWConfig holds HNSW-specific parameters.
type HNSWConfig struct {
	// M is the number of connections per node (default: 32).
	M int `json:"m"`

	// EfConstruction is the size of the dynamic candidate list during construction (default: 200).
	EfConstruction int `json:"ef_construction"`

	// EfSearch is the size of the dynamic candidate list during search (default: 128).
	EfSearch int `json:"ef_search"`

	// MaxLevel is the maximum level of the HNSW graph (auto-computed if 0).
	MaxLevel int `json:"max_level,omitempty"`

	// DistanceMetric is the distance function ("l2", "cosine", "ip"). Default: "l2".
	DistanceMetric DistanceMetric `json:"distance_metric,omitempty"`

	// UseHeuristic enables heuristic neighbor selection (Algorithm 4) for better
	// diversity in neighbor connections. Default: true.
	UseHeuristic bool `json:"use_heuristic"`

	// PruneEmbeddings enables storage-saving embedding pruning (default: true).
	PruneEmbeddings bool `json:"prune_embeddings"`

	// PruneKeepFraction is the fraction of embeddings to keep (default: 0.0 = keep none).
	PruneKeepFraction float64 `json:"prune_keep_fraction"`
}

// SearchConfig holds search-specific parameters.
type SearchConfig struct {
	// TopK is the number of results to return (default: 10).
	TopK int `json:"top_k"`

	// UseReranker enables reranking of results.
	UseReranker bool `json:"use_reranker"`

	// RerankerConfig configures the reranker when UseReranker is true.
	RerankerConfig RerankerConfig `json:"reranker_config,omitempty"`

	// HybridAlpha is the weight for vector vs BM25 (0.0 = BM25 only, 1.0 = vector only).
	HybridAlpha float32 `json:"hybrid_alpha"`

	// MinScore is the minimum score threshold for results.
	MinScore float32 `json:"min_score"`

	// MetadataFilters is a list of metadata filter conditions.
	MetadataFilters []MetadataFilter `json:"metadata_filters,omitempty"`

	// FilterLogic is "and" or "or" for combining filters (default: "and").
	FilterLogic string `json:"filter_logic,omitempty"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		IndexDir:          ".",
		Backend:           "hnsw",
		EmbeddingModel:    "bge-m3",
		EmbeddingProvider: "ollama",
		OllamaHost:       "http://localhost:11434",
		HNSWConfig: HNSWConfig{
			M:                 32,
			EfConstruction:    200,
			EfSearch:          128,
			UseHeuristic:      true,
			PruneEmbeddings:   true,
			PruneKeepFraction: 0.0,
		},
		SearchConfig: SearchConfig{
			TopK:        10,
			HybridAlpha: 0.7,
		},
		ChunkConfig: DefaultChunkConfig(),
	}
}

// ChunkConfig holds text chunking parameters.
type ChunkConfig struct {
	// ChunkSize is the maximum number of tokens per chunk.
	ChunkSize int `json:"chunk_size"`

	// ChunkOverlap is the number of overlapping tokens between chunks.
	ChunkOverlap int `json:"chunk_overlap"`

	// SplitByParagraph enables paragraph-level splitting.
	SplitByParagraph bool `json:"split_by_paragraph"`
}

// DefaultChunkConfig returns default chunking parameters.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{
		ChunkSize:    512,
		ChunkOverlap: 50,
	}
}

// EmbeddingBatch represents a batch of embeddings with their IDs.
type EmbeddingBatch struct {
	IDs        []int64     `json:"ids"`
	Embeddings [][]float32 `json:"embeddings"`
}
