// Package graphdb is a pluggable benchmark harness for graph databases. It
// measures a candidate store against gleann's SQLite code graph
// (internal/graphstore, the "sqlite" reference) on the same deterministic
// synthetic graph: load time, query latency (p50/p95) per query type, a
// one-file update, disk footprint, memory, and result parity.
//
// A candidate implements Backend in its own sub-package of adapters/ and
// registers itself by name with Register; see README.md. The harness lives in
// a nested Go module so candidate dependencies (and CGo) never reach gleann's
// root go.mod.
package graphdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Symbol is one declaration of the benchmark graph.
type Symbol struct {
	FQN  string
	Name string
	Kind string
	File string // declaring file
	Line int64
}

// Edge is one directed CALLS relation, Src calls Dst. Both are declared
// symbols of the graph.
type Edge struct {
	Src, Dst string
}

// FileDelta is the complete graph state a file owns: the symbols declared in
// it and the edges whose Src is declared in it.
type FileDelta struct {
	File    string
	Symbols []Symbol
	Edges   []Edge
}

// Stats are the graph row counts.
type Stats struct {
	Files     int
	Symbols   int
	CallEdges int
}

// ImpactResult is the blast radius of a symbol: its transitive callers up to
// the depth and the files declaring them. Both sets are sorted and exclude
// the start symbol.
type ImpactResult struct {
	Callers []string
	Files   []string
}

// NoPath is what ShortestPath returns when the store finds no path.
const NoPath = -1

// Backend is what gleann needs from a graph store. Result sets are
// normalised: sorted FQN slices without the start symbol and without
// duplicates, so the runner can check parity against the reference by plain
// comparison. A fresh Backend is created per run by its registered Factory.
type Backend interface {
	// Name is the registered name.
	Name() string

	// Open creates an empty database in dir (an existing, empty directory
	// owned by the backend until Close).
	Open(dir string) error

	// BulkLoad loads the whole graph into the empty database. This is the
	// full-build path and is timed as "load".
	BulkLoad(ctx context.Context, deltas []FileDelta) error

	// ApplyFileDelta replaces everything the file owns (its symbols and the
	// edges owned by it) with d. Edges owned by OTHER files that point at a
	// symbol of the file survive when that symbol is still declared in d and
	// are dropped when it is not; no edge may dangle afterwards.
	ApplyFileDelta(ctx context.Context, d FileDelta) error

	// RemoveFile deletes the file's symbols and every edge touching them. Whether
	// Stats.Files still counts the file afterwards is backend-defined.
	RemoveFile(ctx context.Context, file string) error

	// Callers returns the symbols that reach fqn through CALLS edges within
	// depth hops; Callees the symbols reached from fqn.
	Callers(ctx context.Context, fqn string, depth int) ([]string, error)
	Callees(ctx context.Context, fqn string, depth int) ([]string, error)

	// Impact is Callers plus the files declaring them.
	Impact(ctx context.Context, fqn string, depth int) (ImpactResult, error)

	// SymbolSearch returns up to limit FQNs whose FQN or name contains
	// pattern case-insensitively, the first limit in FQN order.
	SymbolSearch(ctx context.Context, pattern string, limit int) ([]string, error)

	// ShortestPath returns the hop count of a shortest path from from to
	// to, walking CALLS edges in either direction, at most maxDepth hops
	// (backends may apply their own exploration caps), or NoPath.
	ShortestPath(ctx context.Context, from, to string, maxDepth int) (int, error)

	Stats(ctx context.Context) (Stats, error)

	// DiskBytes is the size of the database on disk. The runner calls it
	// after Close, so everything must be flushed by then.
	DiskBytes() (int64, error)

	// Close flushes and releases the database.
	Close() error
}

// Caveater is optionally implemented by a Backend to list known semantic
// differences (for example a traversal cap) that explain parity mismatches;
// the runner prints them under that backend's results.
type Caveater interface {
	Caveats() []string
}

// Factory builds a fresh, unopened Backend.
type Factory func() Backend

var (
	regMu    sync.Mutex
	registry = map[string]Factory{}
)

// Register makes a backend available under name. Adapters call it from init.
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("graphdb: backend registered twice: " + name)
	}
	registry[name] = f
}

// New builds the backend registered under name.
func New(name string) (Backend, error) {
	regMu.Lock()
	f, ok := registry[name]
	regMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("unknown backend %q (available: %v; CGo adapters need their build tag, see README.md)", name, Names())
	}
	return f(), nil
}

// Names lists the registered backends, sorted.
func Names() []string {
	regMu.Lock()
	defer regMu.Unlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DirSize sums the sizes of all regular files below path (a single file is
// its own size). Adapters use it for DiskBytes.
func DirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}
