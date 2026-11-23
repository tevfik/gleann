package graphstore

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tevfik/gleann/internal/store"
)

// ReplaceAll rebuilds the whole code graph from deltas in ONE transaction:
// every existing symbol and edge is retracted and replaced by the union of the
// deltas, and the files manifest's graph columns are reset to exactly what the
// deltas declare. It is the full-(re)build path (the port of kuzu's wipe + CSV
// COPY load), where ApplyFileDelta is the incremental one.
//
// It is fast because it does the work a fresh build actually needs, once:
//   - ids are assigned in Go — symbols in FQN order, then the external
//     targets in FQN order — and edge rows are integer tuples sorted and
//     deduplicated in Go, so symbols, graph_nodes and the graph_edges b-tree
//     all append in key order instead of splitting pages at random;
//   - rows go in through multi-row INSERT statements, a few rows per
//     statement execution instead of one: the per-execution cost of
//     database/sql and the driver dominated the load (see bulkBatch);
//   - the secondary indexes are dropped during the load and rebuilt afterwards
//     in one sorted pass each, instead of being maintained row by row;
//   - internal edge targets are resolved against the COMPLETE symbol set in
//     memory, so an edge to a symbol declared in a later file is kept on the
//     first pass — no dirty-file convergence pass is needed;
//   - the trigram FTS index is repopulated with one 'rebuild' at the end rather
//     than per-row triggers.
//
// The final rows are identical to applying the same deltas per file with
// ApplyFileDelta and converging the dirty files (TestReplaceAllMatchesPerFile):
// an edge whose src is not declared in its owning file is an extractor bug and
// refuses the whole rebuild; an internal dst that no delta declares is dropped
// and its owner marked graph_dirty, exactly as insertEdges does. Files not
// present in deltas keep their passage manifest but lose their graph columns.
//
// The transaction holds the single writer for the whole rebuild (RFC 003
// section 3.6); callers serialise rebuilds with the per-index build lock.
func (g *SQLite) ReplaceAll(ctx context.Context, deltas []FileDelta) error {
	return g.ReplaceAllWithMeta(ctx, deltas, nil)
}

// ReplaceAllWithMeta rebuilds the whole code graph and applies metadata mutations in ONE transaction.
// A nil value in metaMutations causes the key (or prefix if ending with '%') to be deleted.
// Mutations are applied in a deterministic order (prefix deletions, exact
// deletions, then sets; see applyMetaMutationsTx), and graph_treesitter is
// recomputed from the rows and markers the transaction writes, exactly as a
// per-file delta does.
func (g *SQLite) ReplaceAllWithMeta(ctx context.Context, deltas []FileDelta, metaMutations map[string]*string) error {
	b, err := normalizeBulk(deltas)
	if err != nil {
		return err
	}

	tx, err := g.writer.BeginTx(ctx, nil) // _txlock=immediate: BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("replacing graph: beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Retract everything. Drop the FTS triggers first so the mass DELETE does
	// not fire one FTS delete per row, and the secondary indexes so the deletes
	// and the ordered inserts do not maintain them row by row. Labels stay:
	// their ids are stable for the life of the database.
	for _, q := range []string{
		`DROP TRIGGER IF EXISTS symbols_fts_insert`,
		`DROP TRIGGER IF EXISTS symbols_fts_delete`,
		`DROP TRIGGER IF EXISTS symbols_fts_update`,
		`DROP INDEX IF EXISTS symbols_file`,
		`DROP INDEX IF EXISTS symbols_name`,
		`DROP INDEX IF EXISTS graph_edges_dst`,
		`DELETE FROM graph_edges`,
		`DELETE FROM symbols`,
		`DELETE FROM graph_nodes`,
		`UPDATE files SET graph_hash = NULL, graph_dirty = 0`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("replacing graph: %s: %w", q, err)
		}
	}

	// Edge rows as integer tuples in primary-key order, deduplicated on
	// (src, dst, kind) keeping the first occurrence (INSERT OR IGNORE's
	// rule); the survivors per kind are the exact counter totals.
	labels := labelCache{}
	labelID := make([]int32, len(b.labels))
	for i, l := range b.labels {
		id, err := labels.id(ctx, tx, l)
		if err != nil {
			return fmt.Errorf("replacing graph: %w", err)
		}
		labelID[i] = int32(id)
	}
	edges := b.edges
	for i := range edges {
		edges[i].kind = labelID[edges[i].kind]
		edges[i].conf = labelID[edges[i].conf]
	}
	slices.SortStableFunc(edges, func(a, b bulkEdgeIDs) int {
		if c := cmp.Compare(a.src, b.src); c != 0 {
			return c
		}
		if c := cmp.Compare(a.dst, b.dst); c != 0 {
			return c
		}
		return cmp.Compare(a.kind, b.kind)
	})
	edges = slices.CompactFunc(edges, func(a, b bulkEdgeIDs) bool {
		return a.src == b.src && a.dst == b.dst && a.kind == b.kind
	})
	kindName := make(map[int32]string, len(labels))
	for name, id := range labels {
		kindName[int32(id)] = name
	}
	edgeCounts := make(map[string]int64)
	for _, e := range edges {
		edgeCounts[kindName[e.kind]]++
	}

	// Symbols, external nodes, then edges, each in key order.
	order, extFQNs := b.symOrder, b.ext
	if err := insertRows(ctx, tx, `INSERT INTO symbols
(id, fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc) VALUES `, 12, bulkBatchSymbols, len(order),
		func(i int, args []any) []any {
			s := &b.syms[order[i]]
			return append(args, int64(i+1), s.fqn, s.name, s.kind, s.file, s.startLine, s.endLine, s.startCol, s.byteOffset, s.isTest, s.weight, s.doc)
		}); err != nil {
		return fmt.Errorf("replacing graph: inserting symbols: %w", err)
	}
	if err := insertRows(ctx, tx, `INSERT INTO graph_nodes (id, fqn) VALUES `, 2, bulkBatchNodes, len(extFQNs),
		func(j int, args []any) []any { return append(args, int64(len(order)+1+j), extFQNs[j]) }); err != nil {
		return fmt.Errorf("replacing graph: inserting external nodes: %w", err)
	}
	if err := insertRows(ctx, tx, `INSERT INTO graph_edges (src, dst, kind, dst_external, confidence) VALUES `, 5, bulkBatchEdges, len(edges),
		func(i int, args []any) []any {
			e := &edges[i]
			return append(args, e.src, e.dst, e.kind, e.external, e.conf)
		}); err != nil {
		return fmt.Errorf("replacing graph: inserting edges: %w", err)
	}

	// Rebuild the secondary indexes (one sorted pass each) and the FTS index.
	for _, q := range []string{
		`CREATE INDEX symbols_file ON symbols(file)`,
		`CREATE INDEX symbols_name ON symbols(name)`,
		store.GraphEdgesDstIndex,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("replacing graph: %s: %w", q, err)
		}
	}
	if _, err := tx.ExecContext(ctx, store.SymbolsFTSTriggers); err != nil {
		return fmt.Errorf("replacing graph: recreating fts triggers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO symbols_fts(symbols_fts) VALUES('rebuild')`); err != nil {
		return fmt.Errorf("replacing graph: rebuilding symbols_fts: %w", err)
	}

	// The counters are exactly the rows just inserted (the wipe emptied them).
	if err := setGraphCounts(ctx, tx, int64(len(b.syms)), edgeCounts); err != nil {
		return fmt.Errorf("replacing graph: %w", err)
	}

	// Advance the manifest graph columns for every delta file, in file order.
	now := time.Now().UnixNano()
	fileUp, err := tx.PrepareContext(ctx, `INSERT INTO files (path, size, mtime_ns, hash, graph_hash, graph_dirty, indexed_at)
VALUES (?, 0, 0, '', ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET graph_hash = excluded.graph_hash, graph_dirty = excluded.graph_dirty`)
	if err != nil {
		return fmt.Errorf("replacing graph: preparing files upsert: %w", err)
	}
	defer fileUp.Close()
	for _, d := range deltas {
		file := slashPath(d.File)
		var graphHash any
		if d.GraphHash != "" {
			graphHash = d.GraphHash
		}
		if _, err := fileUp.ExecContext(ctx, file, graphHash, boolInt(b.dirty[file]), now); err != nil {
			return fmt.Errorf("replacing graph: writing files row %q: %w", file, err)
		}
	}

	if err := g.applyMetaMutationsTx(ctx, tx, metaMutations); err != nil {
		return fmt.Errorf("replacing graph: %w", err)
	}
	// Derive the global key from the rows and markers this transaction wrote,
	// exactly as a per-file delta does, so a rebuild cannot commit an explicit
	// value that contradicts the graph (no non-Go rows, lite markers, or
	// tree-sitter-extracted non-Go rows).
	if err := g.RecomputeTreeSitterMetaTx(ctx, tx); err != nil {
		return fmt.Errorf("replacing graph: syncing tree-sitter metadata: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replacing graph: committing: %w", err)
	}

	// Maintenance after commit (the data is already durable): reclaim free
	// pages when a rebuild left a noticeable share of the file free (a large
	// graph replaced by a smaller one), then flush the WAL back into the
	// database and truncate it. The order matters: in WAL mode VACUUM writes
	// the whole database through the WAL, so checkpointing first left a
	// database-sized WAL behind (181 MiB at 1M edges). A first load or a
	// same-size rebuild reuses the freed pages and leaves only a few behind
	// (FTS segment merges); rewriting the whole file for those cost a second
	// at 1M edges and gained nothing. Both steps are best-effort — a busy
	// reader must not fail a committed rebuild.
	var free, pages int64
	if err := g.writer.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err == nil && free > 0 {
		if err := g.writer.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err == nil && free*vacuumFreeShare > pages {
			_, _ = g.writer.ExecContext(ctx, `VACUUM`)
		}
	}
	_, _ = g.writer.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

const (
	// vacuumFreeShare: ReplaceAll VACUUMs when more than 1/vacuumFreeShare
	// of the database pages are free after the rebuild.
	vacuumFreeShare = 10

	// Rows per multi-row INSERT execution. The driver binds each parameter
	// by searching the argument list (modernc.org/sqlite conn.bind is
	// quadratic in the parameter count), so large statements lose what they
	// save in executions; at 1M edges 16/8/32 rows loaded in 4.1 s where 1
	// row per statement took 5.2 s and 1024 rows 20 s.
	bulkBatchEdges   = 16
	bulkBatchSymbols = 8
	bulkBatchNodes   = 32
)

// insertRows inserts n rows of ncols columns with multi-row INSERT statements
// of up to batch rows each: head is the statement up to and including VALUES,
// and row appends row i's values to args. One prepared statement serves every
// full batch; the tail gets its own.
func insertRows(ctx context.Context, tx *sql.Tx, head string, ncols, batch, n int, row func(i int, args []any) []any) error {
	if n == 0 {
		return nil
	}
	tuple := "(" + inList(ncols) + ")"
	prepare := func(rows int) (*sql.Stmt, error) {
		return tx.PrepareContext(ctx, head+strings.TrimSuffix(strings.Repeat(tuple+",", rows), ","))
	}
	full, err := prepare(min(batch, n))
	if err != nil {
		return err
	}
	defer full.Close()
	args := make([]any, 0, batch*ncols)
	for start := 0; start < n; start += batch {
		end := min(start+batch, n)
		args = args[:0]
		for i := start; i < end; i++ {
			args = row(i, args)
		}
		stmt := full
		if end-start < batch && start > 0 {
			tail, err := prepare(end - start)
			if err != nil {
				return err
			}
			defer tail.Close()
			stmt = tail
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// bulkSymbol is a normalised, insert-ready symbol row of a rebuild.
type bulkSymbol struct {
	fqn, name, kind, file                    string
	startLine, endLine, startCol, byteOffset int64
	isTest                                   int
	weight                                   float64
	doc                                      string
}

// bulkEdgeIDs is a normalised edge of a rebuild as it is stored: node ids
// for src and dst, and the kind and confidence label ids. normalizeBulk fills
// kind and conf with indexes into bulkGraph.labels, which ReplaceAll maps to
// graph_labels ids. 32 bytes per edge: the rebuild holds a million of them.
type bulkEdgeIDs struct {
	src, dst   int64
	kind, conf int32
	external   int32
}

// bulkGraph is the validated content of a rebuild with its ids assigned:
// symbols 1..S in FQN order, then the external targets no symbol declares
// S+1.. in FQN order.
type bulkGraph struct {
	syms     []bulkSymbol  // in delta order
	symOrder []int         // indexes into syms, by FQN (the row with id i+1 is syms[symOrder[i]])
	edges    []bulkEdgeIDs // surviving edges, in delta order
	ext      []string      // external target FQNs, by id
	labels   []string      // kind and confidence strings, by local index
	dirty    map[string]bool
}

// normalizeBulk validates and normalises every delta against the complete
// symbol set, mirroring insertSymbols / insertEdges / checkSources exactly,
// and assigns the ids: it returns the symbols, the surviving edges with
// resolved endpoints, the external targets, and the set of files whose
// unresolved internal targets mark them dirty.
func normalizeBulk(deltas []FileDelta) (*bulkGraph, error) {
	b := &bulkGraph{dirty: make(map[string]bool)}
	owner := make(map[string]int) // fqn -> index into b.syms (duplicate detection + resolution)

	for _, d := range deltas {
		file := slashPath(d.File)
		if file == "" {
			return nil, fmt.Errorf("replacing graph: empty file path")
		}
		for _, s := range d.Symbols {
			if s.FQN == "" {
				return nil, fmt.Errorf("replacing graph: inserting symbols: empty FQN")
			}
			if prev, ok := owner[s.FQN]; ok {
				if b.syms[prev].file == file {
					return nil, fmt.Errorf("replacing graph: inserting symbols: %q declared twice in the same delta", s.FQN)
				}
				return nil, fmt.Errorf("replacing graph: inserting symbols: %q is already declared in %q", s.FQN, b.syms[prev].file)
			}
			sFile := slashPath(s.File)
			if sFile == "" {
				sFile = file
			}
			if sFile != file {
				return nil, fmt.Errorf("replacing graph: inserting symbols: %q is anchored in %q, not the delta's file %q", s.FQN, sFile, file)
			}
			weight := s.Weight
			if weight == 0 {
				weight = 1.0
			}
			isTest := 0
			if s.IsTest {
				isTest = 1
			}
			owner[s.FQN] = len(b.syms)
			b.syms = append(b.syms, bulkSymbol{
				fqn: s.FQN, name: s.Name, kind: s.Kind, file: sFile,
				startLine: s.StartLine, endLine: s.EndLine, startCol: s.StartCol, byteOffset: s.ByteOffset,
				isTest: isTest, weight: weight, doc: s.Doc,
			})
		}
	}

	// Symbol ids follow FQN order.
	b.symOrder = make([]int, len(b.syms))
	for i := range b.symOrder {
		b.symOrder[i] = i
	}
	slices.SortFunc(b.symOrder, func(x, y int) int { return strings.Compare(b.syms[x].fqn, b.syms[y].fqn) })
	symID := make([]int64, len(b.syms))
	for rank, i := range b.symOrder {
		symID[i] = int64(rank + 1)
	}

	// Edges are resolved against the complete symbol set, so order never
	// matters: an internal dst declared by any delta is kept on this pass.
	// An external target that some delta declares is that symbol (an external
	// classification can be stale), as idSpace.targetID resolves it. Other
	// external targets get -1-j (j in first-seen order) until their ids are
	// known below.
	ext := make(map[string]int)
	labelIdx := make(map[string]int32)
	label := func(l string) int32 {
		i, ok := labelIdx[l]
		if !ok {
			i = int32(len(b.labels))
			labelIdx[l] = i
			b.labels = append(b.labels, l)
		}
		return i
	}
	for _, d := range deltas {
		file := slashPath(d.File)
		for _, e := range d.Edges {
			kind := e.Kind
			if kind == "" {
				kind = "CALLS"
			}
			confidence := e.Confidence
			if confidence == "" {
				confidence = "extracted"
			}
			src, ok := owner[e.Src]
			if !ok || b.syms[src].file != file {
				return nil, fmt.Errorf("replacing graph: edge %q -> %q starts at a symbol with no declaration in this file", e.Src, e.Dst)
			}
			var dst int64
			var external int32
			if di, declared := owner[e.Dst]; declared {
				dst = symID[di]
			} else if !e.DstExternal {
				b.dirty[file] = true // unresolved internal target: drop, mark owner dirty
				continue
			}
			if e.DstExternal {
				external = 1
				if dst == 0 {
					j, seen := ext[e.Dst]
					if !seen {
						j = len(b.ext)
						ext[e.Dst] = j
						b.ext = append(b.ext, e.Dst)
					}
					dst = int64(-1 - j)
				}
			}
			b.edges = append(b.edges, bulkEdgeIDs{src: symID[src], dst: dst, kind: label(kind), conf: label(confidence), external: external})
		}
	}

	// External ids follow FQN order after the symbols.
	extOrder := make([]int, len(b.ext))
	for j := range extOrder {
		extOrder[j] = j
	}
	slices.SortFunc(extOrder, func(x, y int) int { return strings.Compare(b.ext[x], b.ext[y]) })
	extID := make([]int64, len(b.ext))
	sorted := make([]string, len(b.ext))
	for r, j := range extOrder {
		extID[j] = int64(len(b.syms) + 1 + r)
		sorted[r] = b.ext[j]
	}
	b.ext = sorted
	for i := range b.edges {
		if d := b.edges[i].dst; d < 0 {
			b.edges[i].dst = extID[-1-d]
		}
	}
	return b, nil
}
