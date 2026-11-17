package gleann

import (
	"fmt"
	"path/filepath"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// OpenGraphDB opens the code graph of a format-2 index for standalone graph
// queries (CLI `gleann graph ...`), without loading the passage searcher.
// The graph lives in the index's own SQLite database (RFC 003 sections 3.4
// and 3.7), so this works in every build including CGO_ENABLED=0. It returns
// (nil, nil) when the index is not format-2 or its graph tables hold no data;
// the caller may then fall back to the Kùzu sidecar via GraphDBOpener. The
// returned GraphDB owns its store: Close releases both.
func OpenGraphDB(indexDir, name string) (GraphDB, error) {
	base := filepath.Join(indexDir, name, name)
	if StoreFormatOf(base) != 2 {
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
