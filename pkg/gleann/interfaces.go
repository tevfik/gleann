package gleann

import (
	"context"

	"github.com/tevfik/gleann/modules/chunking"
)

// BackendBuilder is the interface that backend builders must implement.
// It mirrors Python LEANN's BackendBuilder ABC.
type BackendBuilder interface {
	// Build constructs the index from the given embeddings and returns
	// the serialized index data.
	Build(ctx context.Context, embeddings [][]float32) ([]byte, error)

	// AddVectors adds new vectors to an existing index.
	AddVectors(ctx context.Context, indexData []byte, embeddings [][]float32, startID int64) ([]byte, error)

	// RemoveVectors removes vectors by their IDs from the index.
	RemoveVectors(ctx context.Context, indexData []byte, ids []int64) ([]byte, error)
}

// BackendSearcher is the interface that backend searchers must implement.
// It mirrors Python LEANN's BackendSearcher ABC.
type BackendSearcher interface {
	// Load loads the index from serialized data.
	Load(ctx context.Context, indexData []byte, meta IndexMeta) error

	// Search performs a vector search and returns (ids, distances).
	Search(ctx context.Context, query []float32, topK int) ([]int64, []float32, error)

	// SearchWithRecompute performs search with on-the-fly embedding recomputation.
	// This is LEANN's core innovation — instead of storing all embeddings,
	// only recompute those needed during the graph traversal.
	SearchWithRecompute(ctx context.Context, query []float32, topK int, recompute EmbeddingRecomputer) ([]int64, []float32, error)

	// Close releases any resources held by the searcher.
	Close() error
}

// MmapBackendSearcher is an optional interface for searchers that support
// zero-copy memory mapping directly from a file path.
type MmapBackendSearcher interface {
	BackendSearcher

	// LoadFromFile memory maps the index file directly, avoiding loading bytes into RAM.
	LoadFromFile(ctx context.Context, path string) error
}

// EmbeddingRecomputer is a function that recomputes embeddings for given passage IDs.
// This is the core of LEANN's storage optimization — embeddings are not stored
// but recomputed on-demand during search.
type EmbeddingRecomputer func(ctx context.Context, ids []int64) ([][]float32, error)

// BackendFactory creates BackendBuilder and BackendSearcher instances.
type BackendFactory interface {
	// Name returns the backend name (e.g., "hnsw", "ivf").
	Name() string

	// NewBuilder creates a new BackendBuilder with the given config.
	NewBuilder(config Config) BackendBuilder

	// NewSearcher creates a new BackendSearcher with the given config.
	NewSearcher(config Config) BackendSearcher
}

// EmbeddingComputer computes embeddings for text passages.
type EmbeddingComputer interface {
	// Compute computes embeddings for the given texts.
	Compute(ctx context.Context, texts []string) ([][]float32, error)

	// ComputeSingle computes embedding for a single text.
	ComputeSingle(ctx context.Context, text string) ([]float32, error)

	// Dimensions returns the embedding dimensions.
	Dimensions() int

	// ModelName returns the model name.
	ModelName() string
}

// EmbeddingServer manages an in-process embedding computation service.
// In Python LEANN, this is done via ZMQ subprocess.
// In gleann-go, this is done via goroutines and channels.
type EmbeddingServer interface {
	// Start starts the embedding server.
	Start(ctx context.Context) error

	// Stop stops the embedding server.
	Stop() error

	// ComputeEmbeddings sends a request to compute embeddings for given IDs.
	ComputeEmbeddings(ctx context.Context, ids []int64) ([][]float32, error)

	// IsRunning returns whether the server is running.
	IsRunning() bool
}

// Chunker splits text into smaller chunks for indexing.
type Chunker interface {
	// Chunk splits the given text into chunks.
	Chunk(text string) []string

	ChunkWithMetadata(text string, metadata map[string]any) []chunking.Chunk
}

// Scorer provides scoring for search results (e.g., BM25).
type Scorer interface {
	// Score scores the query against the given passages.
	Score(query string, passages []Passage) []float32

	// AddDocuments adds documents to the scorer's index.
	AddDocuments(passages []Passage)
}

// Reranker re-scores search results using a cross-encoder or similar model
// for higher-quality ranking than bi-encoder embeddings alone.
type Reranker interface {
	// Rerank takes a query and candidate results, returns them re-scored.
	// The returned results are sorted by the new score (descending).
	Rerank(ctx context.Context, query string, results []SearchResult, topN int) ([]SearchResult, error)
}

// Callee holds a single symbol FQN returned from a graph traversal.
type Callee struct {
	FQN  string
	Name string
	Kind string
}

// SymbolInfo holds detailed information for a symbol.
type SymbolInfo struct {
	FQN  string
	Kind string
	File string
	Name string
}

// ImpactResult holds the blast radius analysis for a symbol change.
type ImpactResult struct {
	Symbol            string   `json:"symbol"`             // The symbol being changed
	DirectCallers     []string `json:"direct_callers"`     // Symbols that directly call this
	TransitiveCallers []string `json:"transitive_callers"` // Symbols reachable via transitive callers
	AffectedFiles     []string `json:"affected_files"`     // Files containing affected symbols
	Depth             int      `json:"depth"`              // Max traversal depth used
}

// GraphEdge represents a single edge in a graph traversal result.
type GraphEdge struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Relation   string `json:"relation"`    // CALLS, IMPLEMENTS, REFERENCES
	TargetKind string `json:"target_kind"` // function, method, type, etc.
	Confidence string `json:"confidence"`  // extracted, inferred, ambiguous
}

// PathStep represents one hop in a shortest-path result.
type PathStep struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation"`
}

// GraphStats holds basic graph statistics.
type GraphStats struct {
	Files           int `json:"files"`
	Symbols         int `json:"symbols"`
	CallEdges       int `json:"call_edges"`
	DeclareEdges    int `json:"declare_edges"`
	ImplementsEdges int `json:"implements_edges"`
}

// GraphDB represents a graph database backend capable of querying AST and document relationships.
type GraphDB interface {
	Callees(callerFQN string) ([]Callee, error)
	Callers(calleeFQN string) ([]Callee, error)
	SymbolsInFile(filePath string) ([]Callee, error)
	DocumentSymbols(docPath string) ([]SymbolInfo, error)
	DocumentContext(vpath string) (*DocumentContextData, error)
	FullDocument(vpath string) (string, error)
	Impact(fqn string, maxDepth int) (*ImpactResult, error)
	Neighbors(fqn string, maxDepth int) ([]GraphEdge, error)
	ShortestPath(fromFQN, toFQN string) ([]PathStep, error)
	SymbolSearch(pattern string) ([]Callee, error)
	Stats() (*GraphStats, error)
	Close()
}
