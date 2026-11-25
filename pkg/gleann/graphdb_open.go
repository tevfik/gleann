package gleann

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// graphStorePresent reports whether <name>.db exists. OpenGraphDB and
// OpenGraphStore must check it before store.Open, which would create an empty
// database and hide a lost store from HasCodeGraph (and from Sync, which would
// then overwrite the index without the graph it used to hold).
func graphStorePresent(base string) (bool, error) {
	if _, err := os.Stat(base + ".db"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// OpenGraphDB opens the code graph of a format-2 index for standalone graph
// queries (CLI `gleann graph ...`), without loading the passage searcher.
// The graph lives in the index's own SQLite database (RFC 003 sections 3.4
// and 3.7), so this works in every build including CGO_ENABLED=0. It returns
// (nil, nil) when the index is not format-2 or its graph tables hold no data;
// the caller then reports NoCodeGraphError. The returned GraphDB owns its
// store: Close releases both.
func OpenGraphDB(indexDir, name string) (GraphDB, error) {
	base := filepath.Join(indexDir, name, name)
	if StoreFormatOf(base) != 2 {
		return nil, nil
	}
	present, err := graphStorePresent(base)
	if err != nil {
		return nil, fmt.Errorf("checking index store for graph queries: %w", err)
	}
	if !present {
		// Never let store.Open create an empty database: a missing store is
		// "no graph", not a new empty index.
		return nil, nil
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening index store for graph queries: %w", err)
	}
	gs := graphstore.New(st)
	has, err := gs.HasData()
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("checking graph data of index %q: %w", name, err)
	}
	if !has {
		st.Close()
		return nil, nil
	}
	return &ownedGraphDB{GraphDB: &sqliteGraphDB{g: gs}, st: st}, nil
}

// ownedGraphDB is a GraphDB whose SQLite store was opened just for it (as
// opposed to the searcher's adapter, which borrows the passage store's
// pools). Close closes the inner adapter first, then the store.
type ownedGraphDB struct {
	GraphDB
	st *store.Store
}

func (d *ownedGraphDB) Close() {
	d.GraphDB.Close()
	d.st.Close()
}

// HasTreeSitterEdges forwards the check to the underlying GraphDB implementation.
func (d *ownedGraphDB) HasTreeSitterEdges() bool {
	if checker, ok := d.GraphDB.(interface{ HasTreeSitterEdges() bool }); ok {
		return checker.HasTreeSitterEdges()
	}
	return false
}

// ResolveSymbolFiles forwards the symbol file resolution to the underlying GraphDB implementation.
func (d *ownedGraphDB) ResolveSymbolFiles(symbol string, dir int) ([]string, error) {
	if resolver, ok := d.GraphDB.(interface {
		ResolveSymbolFiles(string, int) ([]string, error)
	}); ok {
		return resolver.ResolveSymbolFiles(symbol, dir)
	}
	return nil, nil
}

// Unwrap returns the underlying GraphDB.
func (d *ownedGraphDB) Unwrap() GraphDB {
	return d.GraphDB
}

// GraphHealth reports how the index's code graph was built: which graph
// extractor wrote it, whether Go calls were resolved exactly (go/types) or
// syntactically, whether tree-sitter contributed non-Go symbols, when the
// graph was last built in full (an incremental sync leaves that timestamp
// alone), how many files are still marked graph_dirty and how the edges
// break down by confidence. It is the public mirror of
// graphstore.GraphHealth; see internal/graphstore/health.go for the metadata
// keys behind it. A graph written by an older gleann reports ExtractorVersion
// "" and Stale true, and VersionDriftHint names the rebuild command.
type GraphHealth = graphstore.GraphHealth

// GraphStore is the format-2 code graph of an index opened for analysis
// (communities, risk, repo map, REST and MCP tools): the graphstore handle
// plus the index database it borrows. Close releases the database.
type GraphStore struct {
	*graphstore.SQLite
	st *store.Store
}

// Close releases the index database.
func (g *GraphStore) Close() { g.st.Close() }

// GraphDB returns the GraphDB view of the same graph (callers, callees,
// symbols in a file, impact, document TOC). Its Close is a no-op; the
// GraphStore owns the database and closing it invalidates the view.
func (g *GraphStore) GraphDB() GraphDB { return &sqliteGraphDB{g: g.SQLite} }

// OpenGraphStore opens the SQLite code graph of a format-2 index (any build).
// Like OpenGraphDB it returns (nil, nil) when the index is not format-2 or
// its graph tables hold no data, so the caller can tell "no graph" from an
// error. The returned handle owns its store; callers must Close it.
func OpenGraphStore(indexDir, name string) (*GraphStore, error) {
	base := filepath.Join(indexDir, name, name)
	if StoreFormatOf(base) != 2 {
		return nil, nil
	}
	present, err := graphStorePresent(base)
	if err != nil {
		return nil, fmt.Errorf("checking index store for graph analysis: %w", err)
	}
	if !present {
		// A missing store must stay missing until the sync rebuilds it; see
		// OpenGraphDB.
		return nil, nil
	}
	st, err := store.Open(base+".db", store.Options{})
	if err != nil {
		return nil, fmt.Errorf("opening index store for graph analysis: %w", err)
	}
	gs := graphstore.New(st)
	has, err := gs.HasData()
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("checking graph data of index %q: %w", name, err)
	}
	if !has {
		st.Close()
		return nil, nil
	}
	return &GraphStore{SQLite: gs, st: st}, nil
}

// HasCodeGraph reports whether the index has a code graph to query: the
// graph data (symbols or documents) of the index in <name>.db. An old Kùzu
// <name>_graph directory is not read since Kùzu was removed (RFC 004 A3). It is the single probe behind
// the TUI agent scope, the update endpoint's graph_stale flag,
// `gleann benchmark` and sync.
func HasCodeGraph(indexDir, name string) bool {
	g, err := OpenGraphStore(indexDir, name)
	if err == nil && g != nil {
		g.Close()
		return true
	}
	return false
}

// NoCodeGraphError is the error every graph command (CLI, REST, MCP, A2A)
// returns for an index without a code graph. An index in the old store
// format gets the migration refusal instead (*OldFormatError).
func NoCodeGraphError(indexDir, name string) error {
	if err := CheckStoreFormat(indexDir, name); err != nil {
		return err
	}
	return fmt.Errorf("index %q has no code graph yet; build it with: gleann index build %s --docs <dir> --graph", name, name)
}
