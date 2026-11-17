package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"
)

// SourceFate is what became of a source that has no passages after a rebuild.
type SourceFate int

const (
	// SourceGone: the file was deleted from the source tree.
	SourceGone SourceFate = iota
	// SourceKept: the file still exists; its graph rows are still right.
	SourceKept
	// SourceUnknown: the tree cannot be consulted; the file's graph rows are
	// kept and the file is marked graph_dirty, so the next graph pass either
	// re-extracts it or finds it gone and retracts it.
	SourceUnknown
)

// RebuildHooks are the decisions a full rebuild of the passages leaves to the
// caller, because they depend on the source tree and on the graph store (which
// imports this package and so cannot be called from here).
type RebuildHooks struct {
	// Fate says what happened to a source that no longer has passages. A nil
	// Fate treats every such source as gone.
	Fate func(source string) SourceFate

	// Retract removes the graph rows (code symbols and edges, document nodes)
	// a gone source owned, inside the rebuild's transaction. It is not called
	// for a source that still exists: its graph rows stay.
	Retract func(ctx context.Context, tx *sql.Tx, source string) error
}

// ReplacePassagesTx replaces the passages, the lexical rows and the manifest
// of the whole index inside the caller's transaction (from BeginWrite) WITHOUT
// touching the code graph tables (symbols, edges, doc_nodes, doc_explains) or
// the graph columns of the files rows (graph_hash, graph_dirty). It is what a
// lexical-only sync or an `index build` without --graph does to an index that
// already has a graph: before, the whole database file was deleted, and the
// graph went with it.
//
//   - every delta replaces its source (ApplySourceTx; the passages come out
//     slotless unless the delta gives slots);
//   - a source of the old passages that is not in deltas lost its passages;
//     when hooks.Fate says it was deleted from the tree, its graph rows are
//     retracted (hooks.Retract) and its files row removed, otherwise it only
//     loses its manifest columns and keeps its graph rows and graph_hash (and
//     is marked graph_dirty when the tree could not be consulted);
//   - a source that stays and whose content hash changed while it has graph
//     rows is marked graph_dirty, so the next graph pass re-extracts it: its
//     graph rows describe the old content until then.
//
// It returns the internal passage IDs per delta, in delta order.
func (s *Store) ReplacePassagesTx(ctx context.Context, tx *sql.Tx, deltas []SourceDelta, hooks RebuildHooks) ([][]PassageID, error) {
	old, err := manifestRowsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	oldSources, err := passageSourcesTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	fresh := make(map[string]bool, len(deltas))
	for _, d := range deltas {
		fresh[d.Source] = true
	}

	for _, src := range oldSources {
		if fresh[src] {
			continue
		}
		if err := deleteSourcePassages(ctx, tx, src); err != nil {
			return nil, fmt.Errorf("dropping passages of %q: %w", src, err)
		}
		fate := SourceGone
		if hooks.Fate != nil {
			fate = hooks.Fate(src)
		}
		if fate == SourceGone {
			if hooks.Retract != nil {
				if err := hooks.Retract(ctx, tx, src); err != nil {
					return nil, fmt.Errorf("retracting graph rows of %q: %w", src, err)
				}
			}
			if err := deleteFile(ctx, tx, src); err != nil {
				return nil, err
			}
			continue
		}
		dirty := `graph_dirty`
		if fate == SourceUnknown {
			dirty = `CASE WHEN graph_hash IS NOT NULL THEN 1 ELSE graph_dirty END`
		}
		if _, err := tx.ExecContext(ctx, `UPDATE files SET size = 0, mtime_ns = 0, hash = '', graph_dirty = `+dirty+` WHERE path = ?`, src); err != nil {
			return nil, fmt.Errorf("clearing the manifest row of %q: %w", src, err)
		}
	}

	ids := make([][]PassageID, 0, len(deltas))
	for _, d := range deltas {
		got, err := s.ApplySourceTx(ctx, tx, d)
		if err != nil {
			return nil, err
		}
		ids = append(ids, got)
		if prev, ok := old[d.Source]; ok && prev.GraphHash != "" && prev.Hash != "" && d.File.Hash != "" && prev.Hash != d.File.Hash {
			if _, err := tx.ExecContext(ctx, `UPDATE files SET graph_dirty = 1 WHERE path = ?`, d.Source); err != nil {
				return nil, fmt.Errorf("marking %q graph_dirty: %w", d.Source, err)
			}
		}
	}
	return ids, nil
}

// manifestRowsTx reads the files rows inside the transaction.
func manifestRowsTx(ctx context.Context, tx *sql.Tx) (map[string]FileRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+fileCols+` FROM files`)
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}
	defer rows.Close()
	out := make(map[string]FileRow)
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, fmt.Errorf("listing files: %w", err)
		}
		out[f.Path] = f
	}
	return out, rows.Err()
}

// passageSourcesTx lists the distinct sources that have passages.
func passageSourcesTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT source FROM passages ORDER BY source`)
	if err != nil {
		return nil, fmt.Errorf("listing passage sources: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("listing passage sources: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// IsCorrupt reports whether err says the database file is damaged or is not a
// database (SQLITE_CORRUPT, SQLITE_NOTADB). A caller that can rebuild the
// file from the sources falls back to recreating it.
func IsCorrupt(err error) bool {
	var se *sqlite.Error
	if errors.As(err, &se) {
		const sqliteCorrupt, sqliteNotADB = 11, 26
		primary := se.Code() & 0xFF
		return primary == sqliteCorrupt || primary == sqliteNotADB
	}
	return false
}
