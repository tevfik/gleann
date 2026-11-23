package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// inboundEdge is an edge from another file pointing at a symbol this file
// declares. It is read into memory before retraction so it can be restored
// when the symbol survives the delta (RFC 003 section 6.5 step 1, the port
// of kuzu IncomingEdges).
type inboundEdge struct {
	src, dst, kindID, confidence int64 // node and label ids
	dstExternal                  int
	kind, file                   string // kind label (counters) and owner file (the src's)
}

// ApplyFileDelta replaces one file's graph rows atomically, following RFC
// 003 section 6.5 exactly:
//
//  1. read the inbound edges to the file's old symbols (other files' edges);
//  2. delete those inbound edges, then the file's own edges, then its symbols
//     (the inbound delete must run before the symbols delete: it reads symbols);
//  3. insert the new symbols and edges;
//  4. re-insert each inbound edge whose dst still exists, otherwise leave it
//     deleted and set graph_dirty on the edge's own file so the next sync
//     re-resolves it;
//  5. commit; any failure rolls everything back, so files.hash does not
//     advance (AGENTS.md section 2.3).
//
// The file's manifest row advances graph_hash and clears graph_dirty in the
// same transaction. When the row does not exist yet (graph built before the
// passages), a minimal row is created; ApplySource later fills the manifest
// columns without touching the graph columns (internal/store upsertFile).
//
// It is begin + ApplyFileDeltaTx + commit. A sync that also writes the
// file's passages shares one transaction instead: store.BeginWrite,
// store.ApplySourceTx, ApplyFileDeltaTx, then a single commit, so files.hash
// advances only when both the passages and the graph rows succeed (section
// 6.5: "in one transaction together with the file's passages and its files
// row").
func (g *SQLite) ApplyFileDelta(ctx context.Context, d FileDelta) error {
	return g.ApplyFileDeltaWithMeta(ctx, d, nil)
}

// ApplyFileDeltaWithMeta replaces one file's graph rows and applies metadata mutations in ONE transaction.
func (g *SQLite) ApplyFileDeltaWithMeta(ctx context.Context, d FileDelta, metaMutations map[string]*string) error {
	tx, err := g.writer.BeginTx(ctx, nil) // _txlock=immediate makes this BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: beginning transaction: %w", d.File, err)
	}
	defer tx.Rollback()
	if err := g.ApplyFileDeltaTx(ctx, tx, d); err != nil {
		return err
	}
	if err := g.applyMetaMutationsTx(ctx, tx, metaMutations); err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}
	// The global key is recomputed from the transaction's own view: a delta
	// (and its lite marker) never commits without the matching
	// graph_treesitter value, and a failing key write rolls both back.
	if err := g.RecomputeTreeSitterMetaTx(ctx, tx); err != nil {
		return fmt.Errorf("applying graph delta for %q: syncing tree-sitter metadata: %w", d.File, err)
	}
	// Step 5: commit.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("applying graph delta for %q: committing: %w", d.File, err)
	}
	return nil
}

// ApplyFileDeltaTx is ApplyFileDelta inside a caller-owned transaction: it
// runs steps 1-4 and the files-row upsert but does not commit, so the caller
// can commit the file's passages (store.ApplySourceTx) and its graph rows
// together, or roll both back (RFC 003 section 6.5). The transaction must
// come from the shared writer (store.BeginWrite): passages and graph live
// in the same database and use the same single writer (section 3.6).
func (g *SQLite) ApplyFileDeltaTx(ctx context.Context, tx *sql.Tx, d FileDelta) error {
	d.File = slashPath(d.File)
	if d.File == "" {
		return fmt.Errorf("applying graph delta: empty file path")
	}

	// Steps 1 and 2: read inbound edges, count what the file owns (the
	// counter debit), then retract everything the file owns.
	inbound, err := readInbound(ctx, tx, d.File)
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}
	delSyms, delOwn, err := fileRowCounts(ctx, tx, d.File)
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}
	previous, gcCands, err := fileNodes(ctx, tx, d.File)
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}
	ids, err := newIDSpace(ctx, tx) // before the deletes: see idSpace
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}
	defer ids.close()
	if err := deleteFileRows(ctx, tx, d.File); err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}

	// Referential integrity (AGENTS.md section 2.3). An edge's src must be a
	// declaration of this file: anything else is an extractor bug and the
	// delta is refused. An internal dst that is not declared (yet) is not
	// written, and the file is marked graph_dirty so the next sync resolves
	// it, the same rule step 4 applies to inbound edges. Refusing the delta
	// instead would make a fresh build depend on file order: a file calling
	// into a file not yet applied could never advance its hash.
	if err := checkSources(d); err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}

	// Step 3: insert the new declarations and references.
	symIDs, err := insertSymbols(ctx, tx, ids, previous, d)
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}
	inserted, unresolved, err := insertEdges(ctx, tx, ids, symIDs, d)
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}

	// Step 4: restore inbound edges whose symbol survived, mark the
	// referencing file dirty for the ones that did not.
	restored, err := restoreInbound(ctx, tx, inbound)
	if err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}
	if err := gcNodes(ctx, tx, gcCands); err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}

	// Keep the meta row counters exact in the same transaction: credits are
	// the rows inserted and restored, debits the rows retracted.
	edgeDelta := make(map[string]int64)
	addKind(edgeDelta, inserted)
	addKind(edgeDelta, restored)
	subKind(edgeDelta, inboundKindCounts(inbound))
	subKind(edgeDelta, delOwn)
	if err := bumpGraphCounts(ctx, tx, int64(len(d.Symbols))-delSyms, edgeDelta); err != nil {
		return fmt.Errorf("applying graph delta for %q: %w", d.File, err)
	}

	// The graph columns of the manifest row advance with the delta, never
	// separately (section 6.5: the flag clears only on a committed delta).
	var graphHash any
	if d.GraphHash != "" {
		graphHash = d.GraphHash
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO files (path, size, mtime_ns, hash, graph_hash, graph_dirty, indexed_at)
VALUES (?, 0, 0, '', ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET graph_hash = excluded.graph_hash, graph_dirty = excluded.graph_dirty`,
		d.File, graphHash, boolInt(unresolved > 0), time.Now().UnixNano()); err != nil {
		return fmt.Errorf("applying graph delta for %q: writing files row: %w", d.File, err)
	}
	return nil
}

// readInbound is step 1 of RFC 003 section 6.5 (port of IncomingEdges):
// edges whose dst is declared in file and whose owner (the src's file) is a
// different file. External targets have no symbols row, so they never appear
// here.
func readInbound(ctx context.Context, tx *sql.Tx, file string) ([]inboundEdge, error) {
	rows, err := tx.QueryContext(ctx, `SELECT e.src, e.dst, e.kind, e.dst_external, e.confidence, k.label, ss.file
FROM symbols ds
JOIN graph_edges e ON e.dst = ds.id
JOIN symbols ss ON ss.id = e.src
JOIN graph_labels k ON k.id = e.kind
WHERE ds.file = ? AND ss.file <> ?`, file, file)
	if err != nil {
		return nil, fmt.Errorf("reading inbound edges: %w", err)
	}
	defer rows.Close()
	var out []inboundEdge
	for rows.Next() {
		var e inboundEdge
		if err := rows.Scan(&e.src, &e.dst, &e.kindID, &e.dstExternal, &e.confidence, &e.kind, &e.file); err != nil {
			return nil, fmt.Errorf("reading inbound edges: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading inbound edges: %w", err)
	}
	return out, nil
}

// fileNodes reads what retraction is about to remove: the ids of the file's
// symbols by FQN (a re-declared symbol keeps its id, so restored inbound edges
// still point at it) and the graph_nodes targets of its external edges, which
// gcNodes deletes after the delta when nothing points at them any more.
func fileNodes(ctx context.Context, tx *sql.Tx, file string) (map[string]int64, []int64, error) {
	previous := make(map[string]int64)
	rows, err := tx.QueryContext(ctx, `SELECT fqn, id FROM symbols WHERE file = ?`, file)
	if err != nil {
		return nil, nil, fmt.Errorf("listing symbols of %q: %w", file, err)
	}
	for rows.Next() {
		var fqn string
		var id int64
		if err := rows.Scan(&fqn, &id); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("listing symbols of %q: %w", file, err)
		}
		previous[fqn] = id
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("listing symbols of %q: %w", file, err)
	}
	rows.Close()

	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT e.dst FROM graph_edges e
WHERE e.src IN (SELECT id FROM symbols WHERE file = ?) AND e.dst_external = 1
  AND EXISTS (SELECT 1 FROM graph_nodes n WHERE n.id = e.dst)`, file)
	if err != nil {
		return nil, nil, fmt.Errorf("listing external targets of %q: %w", file, err)
	}
	defer rows.Close()
	var targets []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, nil, fmt.Errorf("listing external targets of %q: %w", file, err)
		}
		targets = append(targets, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("listing external targets of %q: %w", file, err)
	}
	return previous, targets, nil
}

// deleteFileRows is step 2 of RFC 003 section 6.5. The inbound delete runs
// first because its subquery reads symbols; there are no foreign key
// cascades, so every retraction is explicit. The file's own edges are found by
// src (the owner invariant makes the owning file the src's file) over the
// graph_edges primary key and symbols_file. External graph_nodes are not
// deleted here: gcNodes drops the unreferenced ones once the delta is in.
func deleteFileRows(ctx context.Context, tx *sql.Tx, file string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_edges
WHERE dst IN (SELECT id FROM symbols WHERE file = ?1) AND src NOT IN (SELECT id FROM symbols WHERE file = ?1)`, file); err != nil {
		return fmt.Errorf("deleting inbound edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM graph_edges
WHERE src IN (SELECT id FROM symbols WHERE file = ?)`, file); err != nil {
		return fmt.Errorf("deleting edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM symbols WHERE file = ?`, file); err != nil {
		return fmt.Errorf("deleting symbols: %w", err)
	}
	return nil
}

// insertSymbols writes the delta's declarations, each under the id
// idSpace.symbolID gives it, and returns fqn -> id. A duplicate FQN within the
// delta, or one already declared in another file (build-tag variants, RFC
// 003 section 6.5), is an error rather than an ignored conflict: the
// extraction rule that avoids it belongs to RFC 001, and silently merging
// two declarations would break the one-anchor invariant.
func insertSymbols(ctx context.Context, tx *sql.Tx, idSpace *idSpace, previous map[string]int64, d FileDelta) (map[string]int64, error) {
	ids := make(map[string]int64, len(d.Symbols))
	ins, err := tx.PrepareContext(ctx, `INSERT INTO symbols
(id, fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("preparing symbol insert: %w", err)
	}
	defer ins.Close()
	for _, s := range d.Symbols {
		if s.FQN == "" {
			return nil, fmt.Errorf("inserting symbols: empty FQN")
		}
		if _, dup := ids[s.FQN]; dup {
			return nil, fmt.Errorf("inserting symbols: %q declared twice in the same delta", s.FQN)
		}
		file := slashPath(s.File)
		if file == "" {
			file = d.File
		}
		if file != d.File {
			return nil, fmt.Errorf("inserting symbols: %q is anchored in %q, not the delta's file %q", s.FQN, file, d.File)
		}
		weight := s.Weight
		if weight == 0 {
			weight = 1.0 // neutral default, as kuzu.UpsertSymbol used
		}
		isTest := 0
		if s.IsTest {
			isTest = 1
		}
		id, err := idSpace.symbolID(ctx, s.FQN, previous)
		if err != nil {
			return nil, fmt.Errorf("inserting symbols: %w", err)
		}
		ids[s.FQN] = id
		if _, err := ins.ExecContext(ctx, id, s.FQN, s.Name, s.Kind, file, s.StartLine, s.EndLine, s.StartCol, s.ByteOffset, isTest, weight, s.Doc); err != nil {
			// A key conflict means another file already declares this FQN;
			// name that file so the extractor can be fixed.
			var owner string
			qerr := tx.QueryRowContext(ctx, `SELECT file FROM symbols WHERE fqn = ?`, s.FQN).Scan(&owner)
			if qerr == nil && owner != d.File {
				return nil, fmt.Errorf("inserting symbols: %q is already declared in %q", s.FQN, owner)
			}
			return nil, fmt.Errorf("inserting symbol %q: %w", s.FQN, err)
		}
	}
	return ids, nil
}

// insertEdges writes the delta's references, owned by the delta's file, and
// returns the number actually inserted per kind (INSERT OR IGNORE skips
// duplicates, so the credit is the rows affected, not len(d.Edges)) and the
// number of references to internal targets with no declaration, which are not
// written (the caller marks the file graph_dirty). INSERT OR IGNORE on
// PRIMARY KEY (src, dst, kind) removes the Kùzu "duplicated primary key"
// failure mode for edges (RFC 003 section 6.5). checkSources has run, so
// every src is in symIDs.
func insertEdges(ctx context.Context, tx *sql.Tx, idSpace *idSpace, symIDs map[string]int64, d FileDelta) (map[string]int64, int64, error) {
	ins, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO graph_edges (src, dst, kind, dst_external, confidence)
VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, 0, fmt.Errorf("preparing edge insert: %w", err)
	}
	defer ins.Close()
	declared, err := tx.PrepareContext(ctx, `SELECT id FROM symbols WHERE fqn = ?`)
	if err != nil {
		return nil, 0, fmt.Errorf("preparing symbol lookup: %w", err)
	}
	defer declared.Close()
	labels := labelCache{}
	inserted := make(map[string]int64)
	var unresolved int64
	for _, e := range d.Edges {
		kind := e.Kind
		if kind == "" {
			kind = "CALLS"
		}
		confidence := e.Confidence
		if confidence == "" {
			confidence = "extracted"
		}
		external := 0
		var dst int64
		if e.DstExternal {
			external = 1
			if dst, err = idSpace.targetID(ctx, e.Dst); err != nil {
				return nil, 0, fmt.Errorf("inserting edge %q -> %q: %w", e.Src, e.Dst, err)
			}
		} else if id, ok := symIDs[e.Dst]; ok {
			dst = id
		} else {
			err := declared.QueryRowContext(ctx, e.Dst).Scan(&dst)
			if errors.Is(err, sql.ErrNoRows) {
				unresolved++ // unresolved internal target: not written, owner marked dirty
				continue
			}
			if err != nil {
				return nil, 0, fmt.Errorf("inserting edge %q -> %q: %w", e.Src, e.Dst, err)
			}
		}
		kindID, err := labels.id(ctx, tx, kind)
		if err != nil {
			return nil, 0, err
		}
		confID, err := labels.id(ctx, tx, confidence)
		if err != nil {
			return nil, 0, err
		}
		res, err := ins.ExecContext(ctx, symIDs[e.Src], dst, kindID, external, confID)
		if err != nil {
			return nil, 0, fmt.Errorf("inserting edge %q -> %q: %w", e.Src, e.Dst, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, 0, fmt.Errorf("inserting edge %q -> %q: %w", e.Src, e.Dst, err)
		}
		inserted[kind] += n
	}
	return inserted, unresolved, nil
}

// checkSources refuses a delta with an edge whose src is not declared in
// this file: the caller of a reference is always a declaration of the file
// itself, so a missing one — or one anchored in another file — is an
// extractor bug that re-resolution would not fix. The file's edges in the
// table are exactly the delta's (just inserted), so the check runs over the
// delta's own symbol set in Go, with no scan of edges by file (there is no
// edges_file index; the owner invariant makes edges.file == src's file).
func checkSources(d FileDelta) error {
	declared := make(map[string]bool, len(d.Symbols))
	for _, s := range d.Symbols {
		declared[s.FQN] = true
	}
	for _, e := range d.Edges {
		if !declared[e.Src] {
			return fmt.Errorf("edge %q -> %q starts at a symbol with no declaration in this file", e.Src, e.Dst)
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// restoreInbound is step 4 of RFC 003 section 6.5 (port of
// ExistingSymbolFQNs): an inbound edge whose dst survived the delta is
// re-inserted under its original owner (ids are stable across the delta: a
// re-declared symbol gets its old id back, and a new symbol never gets the id
// of a retracted one); one whose dst is gone stays deleted (step 2 removed it, so no
// dangling edge remains) and its owner is marked graph_dirty so the next sync
// re-resolves the reference. The src is checked too, although the RFC names
// only dst: a restored edge must not dangle on either end (AGENTS.md section
// 2.3).
func restoreInbound(ctx context.Context, tx *sql.Tx, inbound []inboundEdge) (map[string]int64, error) {
	restored := make(map[string]int64)
	if len(inbound) == 0 {
		return restored, nil
	}
	exists, err := tx.PrepareContext(ctx, `SELECT 1 FROM symbols WHERE id = ?`)
	if err != nil {
		return nil, fmt.Errorf("preparing symbol lookup: %w", err)
	}
	defer exists.Close()
	ins, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO graph_edges (src, dst, kind, dst_external, confidence)
VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("preparing inbound restore: %w", err)
	}
	defer ins.Close()
	// Cache the declared() answers for this call: many inbound edges share
	// endpoints, and the symbol set does not change while the loop runs (it
	// only inserts edges), so one lookup per node is enough.
	declaredCache := make(map[int64]bool)
	declared := func(id int64) (bool, error) {
		if ok, cached := declaredCache[id]; cached {
			return ok, nil
		}
		var one int
		err := exists.QueryRowContext(ctx, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			declaredCache[id] = false
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("looking up node %d: %w", id, err)
		}
		declaredCache[id] = true
		return true, nil
	}
	dirty := make(map[string]bool)
	for _, e := range inbound {
		dstOK, err := declared(e.dst)
		if err != nil {
			return nil, err
		}
		srcOK := false
		if dstOK {
			if srcOK, err = declared(e.src); err != nil {
				return nil, err
			}
		}
		if dstOK && srcOK {
			res, err := ins.ExecContext(ctx, e.src, e.dst, e.kindID, e.dstExternal, e.confidence)
			if err != nil {
				return nil, fmt.Errorf("restoring inbound edge %d -> %d: %w", e.src, e.dst, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return nil, fmt.Errorf("restoring inbound edge %d -> %d: %w", e.src, e.dst, err)
			}
			restored[e.kind] += n
			continue
		}
		dirty[e.file] = true
	}
	// Mark each referencing file dirty once, not once per broken edge: the
	// file needs re-extraction once however many of its references broke,
	// and a per-edge upsert would repeat the same write for every edge.
	files := make([]string, 0, len(dirty))
	for f := range dirty {
		files = append(files, f)
	}
	sort.Strings(files) // deterministic write order
	for _, f := range files {
		if err := markDirty(ctx, tx, f); err != nil {
			return nil, err
		}
	}
	return restored, nil
}

// markDirty sets graph_dirty on a referencing file. The upsert covers a file
// the graph knows only through its edges (no passages indexed yet): without
// a row the dirty flag would be lost and the next sync would never
// re-resolve the reference.
func markDirty(ctx context.Context, tx *sql.Tx, file string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO files (path, size, mtime_ns, hash, graph_hash, graph_dirty, indexed_at)
VALUES (?, 0, 0, '', NULL, 1, ?)
ON CONFLICT(path) DO UPDATE SET graph_dirty = 1`, file, time.Now().UnixNano()); err != nil {
		return fmt.Errorf("marking %q graph_dirty: %w", file, err)
	}
	return nil
}

// RemoveFile retracts everything a deleted file owns: its symbols, its edges
// (including dst_external edges, which are retracted only with their owner,
// RFC 003 section 6.5) and the inbound edges to its symbols, whose owners
// are marked graph_dirty. The manifest row itself belongs to the passage
// store (store.RemoveSource deletes it); here only graph_hash is cleared so
// the row no longer claims built graph rows.
func (g *SQLite) RemoveFile(ctx context.Context, file string) error {
	return g.RemoveFileWithMeta(ctx, file, nil)
}

// RemoveFileWithMeta retracts everything a deleted file owns and applies metadata mutations in ONE transaction.
func (g *SQLite) RemoveFileWithMeta(ctx context.Context, file string, metaMutations map[string]*string) error {
	file = slashPath(file)
	tx, err := g.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("removing graph rows of %q: beginning transaction: %w", file, err)
	}
	defer tx.Rollback()
	if err := g.RemoveFileTx(ctx, tx, file); err != nil {
		return err
	}
	if err := g.applyMetaMutationsTx(ctx, tx, metaMutations); err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	// Same invariant as ApplyFileDeltaWithMeta: the removal, its marker
	// deletion and the recomputed graph_treesitter key commit atomically.
	if err := g.RecomputeTreeSitterMetaTx(ctx, tx); err != nil {
		return fmt.Errorf("removing graph rows of %q: syncing tree-sitter metadata: %w", file, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("removing graph rows of %q: committing: %w", file, err)
	}
	return nil
}

// RemoveFileTx is RemoveFile inside a caller-owned transaction (from
// store.BeginWrite), so a rebuild that drops a deleted source's passages can
// retract the source's graph rows in the same commit.
func (g *SQLite) RemoveFileTx(ctx context.Context, tx *sql.Tx, file string) error {
	file = slashPath(file)
	inbound, err := readInbound(ctx, tx, file)
	if err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	delSyms, delOwn, err := fileRowCounts(ctx, tx, file)
	if err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	_, gcCands, err := fileNodes(ctx, tx, file)
	if err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	if err := deleteFileRows(ctx, tx, file); err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	// Nothing is re-inserted, so every inbound edge's dst is gone and its
	// owner becomes dirty; restoreInbound applies the same rule as a delta.
	restored, err := restoreInbound(ctx, tx, inbound)
	if err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	if err := gcNodes(ctx, tx, gcCands); err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	edgeDelta := make(map[string]int64)
	addKind(edgeDelta, restored)
	subKind(edgeDelta, inboundKindCounts(inbound))
	subKind(edgeDelta, delOwn)
	if err := bumpGraphCounts(ctx, tx, -delSyms, edgeDelta); err != nil {
		return fmt.Errorf("removing graph rows of %q: %w", file, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE files SET graph_hash = NULL, graph_dirty = 0 WHERE path = ?`, file); err != nil {
		return fmt.Errorf("removing graph rows of %q: clearing graph_hash: %w", file, err)
	}
	return nil
}

// RetractSourceTx removes everything the graph holds for a deleted source
// file inside the caller's transaction: its code rows (RemoveFileTx) and its
// document nodes and EXPLAINS relations (an empty set for a source that is not
// a document). The files row itself is the caller's.
func (g *SQLite) RetractSourceTx(ctx context.Context, tx *sql.Tx, source string) error {
	if err := g.RemoveFileTx(ctx, tx, source); err != nil {
		return err
	}
	if err := retractDoc(ctx, tx, source); err != nil {
		return fmt.Errorf("removing document graph rows of %q: %w", source, err)
	}
	return nil
}
