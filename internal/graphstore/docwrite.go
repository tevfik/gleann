package graphstore

import (
	"context"
	"database/sql"
	"fmt"
)

// DocExplains is one EXPLAINS relation: a document chunk explains a code
// symbol (the doc_explains table, RFC 003 section 3.4).
type DocExplains struct {
	ChunkID string
	FQN     string
}

// DocDelta is the complete new document-graph state of one source document:
// its folder/document/heading/chunk nodes and EXPLAINS relations.
// ApplyDocDelta replaces everything the document owns (matched by RPath) in
// one transaction; folders are structural and shared, so they are upserted,
// never retracted. ReplaceDocs is the full-rebuild counterpart.
type DocDelta struct {
	RPath    string // physical document path; the retraction owner of the nodes below
	Nodes    []DocNode
	Explains []DocExplains
}

// ReplaceDocs rebuilds the whole document hierarchy from deltas in ONE
// transaction: every existing doc_nodes and doc_explains row is retracted and
// replaced by the union of the deltas. It is the full-(re)build path, the
// document counterpart of ReplaceAll. Duplicate node IDs across deltas
// (shared folders) keep the first occurrence, as INSERT OR IGNORE does.
func (g *SQLite) ReplaceDocs(ctx context.Context, deltas []DocDelta) error {
	tx, err := g.writer.BeginTx(ctx, nil) // _txlock=immediate makes this BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("replacing document graph: beginning transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_explains`); err != nil {
		return fmt.Errorf("replacing document graph: deleting explains: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_nodes`); err != nil {
		return fmt.Errorf("replacing document graph: deleting nodes: %w", err)
	}
	for _, d := range deltas {
		if err := insertDocDelta(ctx, tx, d); err != nil {
			return fmt.Errorf("replacing document graph: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replacing document graph: committing: %w", err)
	}
	return nil
}

// ApplyDocDelta replaces one document's graph rows atomically: its old nodes
// (everything carrying its RPath) and the EXPLAINS relations of its old
// chunks are retracted, then the new nodes and relations are inserted.
// Folders (RPath "") are shared structure and only upserted. Any failure
// rolls everything back.
func (g *SQLite) ApplyDocDelta(ctx context.Context, d DocDelta) error {
	tx, err := g.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("applying document delta for %q: beginning transaction: %w", d.RPath, err)
	}
	defer tx.Rollback()
	if err := retractDoc(ctx, tx, d.RPath); err != nil {
		return fmt.Errorf("applying document delta for %q: %w", d.RPath, err)
	}
	if err := insertDocDelta(ctx, tx, d); err != nil {
		return fmt.Errorf("applying document delta for %q: %w", d.RPath, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("applying document delta for %q: committing: %w", d.RPath, err)
	}
	return nil
}

// RemoveDoc retracts everything a deleted document owns: its nodes and the
// EXPLAINS relations of its chunks. Shared folders stay.
func (g *SQLite) RemoveDoc(ctx context.Context, rpath string) error {
	tx, err := g.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("removing document graph rows of %q: beginning transaction: %w", rpath, err)
	}
	defer tx.Rollback()
	if err := retractDoc(ctx, tx, rpath); err != nil {
		return fmt.Errorf("removing document graph rows of %q: %w", rpath, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("removing document graph rows of %q: committing: %w", rpath, err)
	}
	return nil
}

// retractDoc deletes the EXPLAINS relations of the document's chunks first
// (they reference node IDs), then every node carrying the document's RPath.
func retractDoc(ctx context.Context, tx *sql.Tx, rpath string) error {
	rpath = slashPath(rpath)
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_explains
WHERE chunk_id IN (SELECT id FROM doc_nodes WHERE rpath = ?)`, rpath); err != nil {
		return fmt.Errorf("deleting document explains: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_nodes WHERE rpath = ?`, rpath); err != nil {
		return fmt.Errorf("deleting document nodes: %w", err)
	}
	return nil
}

// insertDocDelta writes one delta's nodes and relations. Node IDs are unique
// across the hierarchy; INSERT OR IGNORE keeps the first occurrence of a
// shared folder, and INSERT OR IGNORE on doc_explains deduplicates relations.
func insertDocDelta(ctx context.Context, tx *sql.Tx, d DocDelta) error {
	ins, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO doc_nodes
(id, type, parent, name, level, rpath, hash, summary, text, start_char, end_char)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing document node insert: %w", err)
	}
	defer ins.Close()
	for _, n := range d.Nodes {
		if n.ID == "" {
			return fmt.Errorf("inserting document nodes: empty ID")
		}
		var parent, rpath any
		if n.Parent != "" {
			parent = n.Parent
		}
		if n.RPath != "" {
			rpath = slashPath(n.RPath)
		}
		if _, err := ins.ExecContext(ctx, n.ID, n.Type, parent, n.Name, n.Level, rpath, n.Hash, n.Summary, n.Text, n.StartChar, n.EndChar); err != nil {
			return fmt.Errorf("inserting document node %q: %w", n.ID, err)
		}
	}
	xins, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO doc_explains (chunk_id, fqn) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing document explains insert: %w", err)
	}
	defer xins.Close()
	for _, x := range d.Explains {
		if x.ChunkID == "" || x.FQN == "" {
			return fmt.Errorf("inserting document explains: empty chunk ID or FQN")
		}
		if _, err := xins.ExecContext(ctx, x.ChunkID, x.FQN); err != nil {
			return fmt.Errorf("inserting document explains %q -> %q: %w", x.ChunkID, x.FQN, err)
		}
	}
	return nil
}

// MarkFileDirty sets graph_dirty on a file outside a delta transaction. The
// format-2 write path uses it best-effort when a file's graph write failed,
// so the next sync re-extracts the file even though its hash did not advance.
func (g *SQLite) MarkFileDirty(ctx context.Context, file string) error {
	file = slashPath(file)
	tx, err := g.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("marking %q graph_dirty: beginning transaction: %w", file, err)
	}
	defer tx.Rollback()
	if err := markDirty(ctx, tx, file); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("marking %q graph_dirty: committing: %w", file, err)
	}
	return nil
}
