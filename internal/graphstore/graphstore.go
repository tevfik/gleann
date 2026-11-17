// Package graphstore implements the code graph of RFC 003 (docs/plans/
// 003-storage.md) over the symbols and edges tables of the format-2 index
// database: plain SQL and recursive CTEs, no in-memory CSR and no build
// tags, so graph queries work in every build including CGO_ENABLED=0 and
// windows (sections 3.7 and 5). The tables are created by internal/store's
// format-2 migration; this package shares its connection pools. The write
// side (section 8 step 7) is wired into internal/indexing: format-2 builds
// and syncs feed FileDelta/DocDelta here instead of opening a Kùzu database.
package graphstore

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/store"
)

// Symbol is one first-party declaration (the symbols table, RFC 003 section
// 3.4). It mirrors kuzu.SymbolNode plus the source anchors RFC 001 requires:
// every symbol has a concrete declaration site, and external symbols are
// never rows here (they live on edges as dst_external, AGENTS.md section 2).
// The types are tag-free on purpose: RFC 003 section 5 moves them out of the
// tagged kuzu package so the extractors can feed ApplyFileDelta in every
// build.
type Symbol struct {
	FQN    string  // fully qualified canonical name, e.g. "github.com/foo/bar.MyFunc"
	Kind   string  // "function" | "method" | "type" | "struct" | "interface" | "const" | "var"
	File   string  // declaration anchor; defaults to the delta's file when empty
	Name   string  // short name, e.g. "MyFunc"
	Doc    string  // documentation comment (optional)
	Weight float64 // language-aware importance; 0 is stored as the neutral 1.0
	IsTest bool    // declared in a test file or is a test function

	// Source anchors (RFC 001, AGENTS.md section 2.2). StartLine is the
	// kuzu.SymbolNode.Line field renamed to the schema's start_line.
	StartLine  int64
	EndLine    int64
	StartCol   int64
	ByteOffset int64
}

// Edge is one directed relation between symbols (the edges table). It
// mirrors kuzu.EdgeCalls generalised to the three relation kinds. The owning
// file is not a field: edges belong to the file of the FileDelta that
// carries them, which is what makes retraction per file possible (RFC 003
// section 6.5).
type Edge struct {
	Src         string // caller / implementer / referencer FQN; must be a declared symbol
	Dst         string // callee / interface / referee FQN
	Kind        string // CALLS | IMPLEMENTS | REFERENCES; defaults to CALLS when empty
	DstExternal bool   // true: dst is a third-party or standard-library symbol with no row in symbols
	Confidence  string // "extracted" | "inferred" | "ambiguous"; defaults to "extracted"
}

// FileDelta is the complete new graph state of one source file.
// ApplyFileDelta replaces everything the file owns with it in one
// transaction (RFC 003 section 6.5).
type FileDelta struct {
	File      string   // source file path; the retraction owner of every row below
	GraphHash string   // hash the graph rows were built from (files.graph_hash); "" stores NULL
	Symbols   []Symbol // declarations anchored in File
	Edges     []Edge   // references contained in File
}

// Hop is one symbol reached by a traversal, with its shortest distance in
// hops from the start symbol. It carries the kuzu Callee fields so the
// consumers of RFC 003 section 6.2 can port without reshaping results.
type Hop struct {
	FQN    string
	Name   string
	Kind   string
	File   string
	Line   int64 // start_line of the declaration anchor
	IsTest bool
	Depth  int // 1 = direct neighbour
}

// Step is one hop of a shortest path (mirrors gleann.PathStep). Relation is
// CALLS or IMPLEMENTS when the step follows an edge forward, CALLED_BY when
// it follows a CALLS edge backward, as in kuzu.ShortestPath.
type Step struct {
	From     string
	To       string
	Relation string
}

// GraphEdge is one directed relation returned by Neighbors (mirrors
// gleann.GraphEdge). TargetKind is the kind of the farther endpoint — the
// symbol discovered across the edge — matching kuzu.Neighbors, and
// Confidence is "extracted" like there.
type GraphEdge struct {
	From       string
	To         string
	Relation   string // CALLS | IMPLEMENTS | REFERENCES
	TargetKind string
	Confidence string
}

// ImpactResult is the blast radius of changing one symbol (mirrors
// gleann.ImpactResult). Transitive callers are reached through production
// callers only; test callers are reported but never expanded, matching
// kuzu.Impact.
type ImpactResult struct {
	Symbol            string
	Depth             int // max traversal depth used
	DirectCallers     []string
	TransitiveCallers []string
	TestCallers       []string
	AffectedFiles     []string
}

// Stats mirrors gleann.GraphStats. DeclareEdges counts symbols, because RFC
// 003 section 3.4 folds the DECLARES relation into the symbols.file anchor
// column (one declaration per symbol). Files counts the manifest rows.
type Stats struct {
	Files           int
	Symbols         int
	CallEdges       int
	DeclareEdges    int
	ImplementsEdges int
}

// InDegree is one hub symbol of the CALLS graph: how many call edges point
// at it. The MCP risk tools aggregate exactly this (RFC 003 section 6.2,
// internal/mcp/tools_graph.go), as a GROUP BY on edges instead of Cypher.
type InDegree struct {
	FQN      string
	Name     string
	Kind     string
	File     string
	InDegree int
}

// Direction selects which side of the CALLS graph a resolved seed symbol
// sits on, for ResolveSymbols. It mirrors the two shapes of kuzu's three-step
// lookup: Callees resolves the caller side (the symbol that calls), Callers
// and Impact resolve the callee side (the symbol that is called).
type Direction int

const (
	// DirectionCallees resolves q to the caller symbols: the FQNs whose
	// outgoing CALLS edges are wanted (kuzu.Callees).
	DirectionCallees Direction = iota
	// DirectionCallers resolves q to the callee symbols: the FQNs whose
	// incoming CALLS edges are wanted (kuzu.Callers and Impact).
	DirectionCallers
)

// GraphStore is the code-graph interface of RFC 003 section 3.2. The read
// methods are answered by SQL; Callers, Callees, Impact and Path use
// recursive CTEs with UNION (so cycles terminate) and an explicit depth cap
// (section 3.7). ApplyFileDelta is atomic: either the file's old symbols and
// edges (and the inbound edges to its old symbols) are retracted and the new
// ones inserted, or nothing changes.
type GraphStore interface {
	Symbols(q string) ([]Symbol, error)
	// ResolveSymbols resolves a user-supplied symbol reference (a short name,
	// a Type.Method, or a full FQN) to the FQNs it denotes, using kuzu's
	// three-step lookup (exact FQN → qualified suffix → base name). It is the
	// resolution step Callers, Callees and Impact apply to their argument.
	ResolveSymbols(q string, dir Direction) ([]string, error)
	Callers(fqn string, depth int) ([]Hop, error)
	Callees(fqn string, depth int) ([]Hop, error)
	Impact(fqn string, depth int) (*ImpactResult, error)
	Path(from, to string) ([]Step, error)
	ApplyFileDelta(ctx context.Context, d FileDelta) error
	RemoveFile(ctx context.Context, file string) error
	Stats() (Stats, error)

	// InDegreeTop returns the n most-called symbols, most calls first.
	InDegreeTop(n int) ([]InDegree, error)
	// DirtyFiles lists the files whose graph rows need re-extraction
	// (files.graph_dirty = 1, RFC 003 section 6.5), served by the partial
	// index files_dirty.
	DirtyFiles() ([]string, error)
}

// SQLite answers GraphStore from the symbols and edges tables of one index
// database, sharing internal/store's connection pools so graph writes and
// passage writes use the same single writer (RFC 003 section 3.6).
type SQLite struct {
	writer *sql.DB
	reader *sql.DB
}

var _ GraphStore = (*SQLite)(nil)

// New opens the graph side of an already-open index store. The store keeps
// ownership of the database handles; closing it closes the graph store too.
func New(s *store.Store) *SQLite {
	return &SQLite{writer: s.WriterDB(), reader: s.ReaderDB()}
}

// Traversal depth caps. RFC 003 section 3.7 requires an explicit cap on
// every recursive walk; 10 matches the cap kuzu.Impact already used, so
// consumer behaviour stays comparable.
const (
	maxTraversalDepth = 10
	// maxPathDepth bounds the shortest-path BFS. kuzu.ShortestPath capped
	// explored nodes (1000) instead of depth; a depth cap is what section
	// 3.7 asks for and bounds the CTE work directly.
	maxPathDepth = 10
)

// walkDepth normalises a caller/callee traversal depth: non-positive means
// direct neighbours only, and the cap keeps a runaway query bounded.
func walkDepth(depth int) int {
	if depth <= 0 {
		return 1
	}
	if depth > maxTraversalDepth {
		return maxTraversalDepth
	}
	return depth
}

// impactDepth normalises the Impact depth exactly as kuzu.Impact did:
// non-positive or above the cap falls back to 5.
func impactDepth(depth int) int {
	if depth <= 0 || depth > maxTraversalDepth {
		return 5
	}
	return depth
}

// maxDocTreeDepth caps the document heading walk. RFC 003 section 3.7
// requires an explicit depth cap on every recursive CTE; real document
// outlines nest far shallower than this, so the cap only bounds corrupt or
// runaway parent chains.
const maxDocTreeDepth = 64

// slashPath normalises an incoming file or document path to the slash form
// the tables store. filepath.ToSlash converts the OS separator; backslashes
// are also replaced explicitly because a delta or lookup can carry a
// Windows-style path on any OS (a cross-platform extractor, or a caller
// passing a path received from another machine), and on linux ToSlash alone
// would leave them untouched.
func slashPath(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), `\`, `/`)
}
