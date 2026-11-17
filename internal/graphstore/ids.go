package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// The code graph is integer-keyed (store's graph_ids step). An edge endpoint
// is a node id: a declared symbol's symbols.id, or, for a target no symbol
// declares (an external one), a graph_nodes.id. The two tables share one id
// space and an FQN lives in exactly one of them: declaring a symbol whose FQN
// is a graph_nodes row moves the row (and its id) into symbols. Edge kinds and
// confidences are interned in graph_labels; the fixed kind ids are written
// into the queries as literals and TestGraphLabelIDs checks them against the
// store's seeds.
const (
	labelCalls      = "1" // store.GraphLabelCalls
	labelImplements = "2" // store.GraphLabelImplements
	labelReferences = "3" // store.GraphLabelReferences
)

// nodeIDOf is the id of the node named ?1, declared or external (at most one
// row: an FQN lives in one of the two tables).
const nodeIDOf = `SELECT id FROM symbols WHERE fqn = ?1 UNION ALL SELECT id FROM graph_nodes WHERE fqn = ?1`

// labelCache resolves kind and confidence strings to graph_labels ids inside
// one write transaction, creating a label on first use. Labels are never
// deleted, so an id stays valid for the life of the database.
type labelCache map[string]int64

func (c labelCache) id(ctx context.Context, tx *sql.Tx, label string) (int64, error) {
	if id, ok := c[label]; ok {
		return id, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM graph_labels WHERE label = ?`, label).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, ierr := tx.ExecContext(ctx, `INSERT INTO graph_labels (label) VALUES (?)`, label)
		if ierr != nil {
			return 0, fmt.Errorf("interning label %q: %w", label, ierr)
		}
		id, err = res.LastInsertId()
	}
	if err != nil {
		return 0, fmt.Errorf("interning label %q: %w", label, err)
	}
	c[label] = id
	return id, nil
}

// idSpace hands out node ids inside one write transaction. It reads the high
// water mark of both tables when it is created — before the delta retracts
// anything — so a new id never equals the id of a symbol the delta is about
// to delete: an inbound edge to that old id must not be restored onto an
// unrelated new symbol.
type idSpace struct {
	next                    int64
	symSel, nodeSel, nodeIn *sql.Stmt
	nodeDel                 *sql.Stmt
}

func newIDSpace(ctx context.Context, tx *sql.Tx) (*idSpace, error) {
	s := &idSpace{}
	if err := tx.QueryRowContext(ctx, `SELECT max(coalesce((SELECT max(id) FROM symbols), 0), coalesce((SELECT max(id) FROM graph_nodes), 0)) + 1`).Scan(&s.next); err != nil {
		return nil, fmt.Errorf("reading the node id high-water mark: %w", err)
	}
	for _, p := range []struct {
		dst **sql.Stmt
		q   string
	}{
		{&s.symSel, `SELECT id FROM symbols WHERE fqn = ?`},
		{&s.nodeSel, `SELECT id FROM graph_nodes WHERE fqn = ?`},
		{&s.nodeIn, `INSERT INTO graph_nodes (id, fqn) VALUES (?, ?)`},
		{&s.nodeDel, `DELETE FROM graph_nodes WHERE id = ?`},
	} {
		st, err := tx.PrepareContext(ctx, p.q)
		if err != nil {
			s.close()
			return nil, fmt.Errorf("preparing node statements: %w", err)
		}
		*p.dst = st
	}
	return s, nil
}

func (s *idSpace) close() {
	for _, st := range []*sql.Stmt{s.symSel, s.nodeSel, s.nodeIn, s.nodeDel} {
		if st != nil {
			st.Close()
		}
	}
}

func lookupID(ctx context.Context, st *sql.Stmt, fqn string) (int64, bool, error) {
	var id int64
	err := st.QueryRowContext(ctx, fqn).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// symbolID returns the id a symbol being declared gets: its old id when the
// delta re-declares one of the file's previous symbols (so restored inbound
// edges still point at it), the id of its graph_nodes row when external edges
// already point at the FQN (the row moves into symbols), or a fresh id.
func (s *idSpace) symbolID(ctx context.Context, fqn string, previous map[string]int64) (int64, error) {
	if id, ok := previous[fqn]; ok {
		return id, nil
	}
	id, ok, err := lookupID(ctx, s.nodeSel, fqn)
	if err != nil {
		return 0, fmt.Errorf("looking up node %q: %w", fqn, err)
	}
	if ok {
		if _, err := s.nodeDel.ExecContext(ctx, id); err != nil {
			return 0, fmt.Errorf("moving node %q into symbols: %w", fqn, err)
		}
		return id, nil
	}
	id = s.next
	s.next++
	return id, nil
}

// targetID returns the node id of an external edge target: the declaring
// symbol's id when the FQN is declared (an external classification can be
// stale), else its graph_nodes id, creating the row on first use.
func (s *idSpace) targetID(ctx context.Context, fqn string) (int64, error) {
	if id, ok, err := lookupID(ctx, s.symSel, fqn); err != nil || ok {
		if err != nil {
			return 0, fmt.Errorf("looking up %q: %w", fqn, err)
		}
		return id, nil
	}
	if id, ok, err := lookupID(ctx, s.nodeSel, fqn); err != nil || ok {
		if err != nil {
			return 0, fmt.Errorf("looking up node %q: %w", fqn, err)
		}
		return id, nil
	}
	id := s.next
	if _, err := s.nodeIn.ExecContext(ctx, id, fqn); err != nil {
		return 0, fmt.Errorf("interning %q: %w", fqn, err)
	}
	s.next++
	return id, nil
}

// gcNodes deletes the candidate graph_nodes rows no edge points at any more
// (a node is only ever an edge target). Called after a file's rows are
// replaced or removed, with the external targets of its old edges.
func gcNodes(ctx context.Context, tx *sql.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	del, err := tx.PrepareContext(ctx, `DELETE FROM graph_nodes WHERE id = ?1
  AND NOT EXISTS (SELECT 1 FROM graph_edges WHERE dst = ?1)`)
	if err != nil {
		return fmt.Errorf("preparing node cleanup: %w", err)
	}
	defer del.Close()
	for _, id := range ids {
		if _, err := del.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("cleaning up node %d: %w", id, err)
		}
	}
	return nil
}

// stmtCache keeps the reader statements of the traversal queries prepared
// for the life of a SQLite value, so a depth-1 lookup does not pay for
// parsing the walk's recursive CTE on every call. database/sql prepares a
// cached statement once per pooled connection and re-prepares it on its own
// when a connection is replaced; closing the store's pools invalidates them.
type stmtCache struct {
	mu sync.Mutex
	m  map[string]*sql.Stmt
}

// queryRead runs one of the fixed traversal queries on the reader pool
// through the statement cache (or unprepared on a SQLite without one).
func (g *SQLite) queryRead(query string, args ...any) (*sql.Rows, error) {
	if g.stmts == nil {
		return g.reader.QueryContext(readCtx(), query, args...)
	}
	g.stmts.mu.Lock()
	st, ok := g.stmts.m[query]
	if !ok {
		var err error
		if st, err = g.reader.PrepareContext(readCtx(), query); err != nil {
			g.stmts.mu.Unlock()
			return nil, err
		}
		if g.stmts.m == nil {
			g.stmts.m = make(map[string]*sql.Stmt)
		}
		g.stmts.m[query] = st
	}
	g.stmts.mu.Unlock()
	return st.QueryContext(readCtx(), args...)
}

// inList returns n comma-separated placeholders.
func inList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
