// Package types holds the extractor output types shared by the code-graph
// extractors (internal/graph/indexer) and the graph sink (graphstore/SQLite).
// It is intentionally tag-free and dependency-free so the
// pure-Go extractors and the format-2 write path build with CGO_ENABLED=0.
package types

// FileNode represents a code file node for CSV export.
type FileNode struct {
	Path string
	Lang string
}

// SymbolNode represents any code symbol (function, type, struct, etc.).
// Weight is a language-aware importance score in [0, 10]: 1.0 is neutral;
// higher values indicate symbols that should rank first in RAG retrieval
// and risk analysis (e.g. Java interfaces, Rust traits, Python decorated
// functions). It is set at ingest time by heuristics.applyHeuristics
// and consumed by community.ComputeRiskScores and any future reranker.
type SymbolNode struct {
	FQN        string  // Fully Qualified Name, e.g. "github.com/foo/bar.MyFunc"
	Kind       string  // "function" | "method" | "type" | "struct" | "interface" | "const" | "var"
	File       string  // source file path
	Line       int64   // line number in source file (start line anchor)
	EndLine    int64   // end line anchor (0 when the extractor cannot determine it)
	StartCol   int64   // start column anchor (0 when not determined)
	ByteOffset int64   // byte offset anchor (0 when not determined)
	Name       string  // short name, e.g. "MyFunc"
	Doc        string  // documentation comment (optional)
	Weight     float64 // language-aware importance (default 1.0)
	IsTest     bool    // true if defined in a test file or is a test function
}

// EdgeDeclares represents a DECLARES relationship (FROM file TO Symbol).
type EdgeDeclares struct {
	FilePath  string
	SymbolFQN string
}

// EdgeCalls represents a CALLS relationship (FROM Symbol TO Symbol).
type EdgeCalls struct {
	CallerFQN  string
	CalleeFQN  string
	Confidence string // "extracted" (AST-resolved), "exact" (go/types), "inferred" (heuristic), "ambiguous"
}

// EdgeImplements represents an IMPLEMENTS relationship (FROM Symbol TO Symbol).
// e.g. MyStruct IMPLEMENTS io.Reader.
type EdgeImplements struct {
	ImplFQN    string // implementor (struct/type)
	IfaceFQN   string // interface
	Confidence string // "extracted" (AST embedding), "exact" (go/types method set); empty means "extracted"
}

// EdgeReferences represents a REFERENCES relationship (FROM Symbol TO Symbol).
// e.g. MyFunc REFERENCES SomeType (uses it as parameter, field, return, etc.)
type EdgeReferences struct {
	RefererFQN string // the symbol that references
	RefereeFQN string // the symbol being referenced
	Confidence string // "extracted", "inferred"
}

// ─── Hierarchical Graph Types ────────────────────────────────────────────────────

// FolderNode represents a structural folder.
type FolderNode struct {
	VPath string
	Name  string
}

// DocumentNode represents a document in the graph.
type DocumentNode struct {
	VPath   string
	RPath   string
	Name    string
	Hash    string
	Summary string
}

// HeadingNode represents a markdown heading section.
type HeadingNode struct {
	ID    string
	Name  string
	Level int64
}

// ChunkNode represents a chunk that links the vector index to the graph.
type ChunkNode struct {
	ID        string
	Text      string
	StartChar int64
	EndChar   int64
}

// EdgeContainsDoc represents CONTAINS_DOC (Folder→Document).
type EdgeContainsDoc struct {
	FolderVPath string
	DocVPath    string
}

// EdgeHasHeading represents HAS_HEADING (Document→Heading).
type EdgeHasHeading struct {
	DocVPath  string
	HeadingID string
}

// EdgeChildHeading represents CHILD_HEADING (Heading→Heading).
type EdgeChildHeading struct {
	ParentID string
	ChildID  string
}

// EdgeHasChunkHeading represents HAS_CHUNK_HEADING (Heading→Chunk).
type EdgeHasChunkHeading struct {
	HeadingID string
	ChunkID   string
}

// EdgeHasChunkDoc represents HAS_CHUNK_DOC (Document→Chunk).
type EdgeHasChunkDoc struct {
	DocVPath string
	ChunkID  string
}

// EdgeExplains represents EXPLAINS (Chunk→Symbol).
type EdgeExplains struct {
	ChunkID   string
	SymbolFQN string
}
