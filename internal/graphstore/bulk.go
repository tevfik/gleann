package graphstore

import (
	"context"
	"fmt"
	"sort"
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
//   - all symbols are inserted first (sorted by fqn), then all edges (sorted by
//     primary key), so both WITHOUT ROWID b-trees and the rowid table append in
//     order instead of splitting pages at random;
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
// and its owner marked graph_dirty, exactly as dropUnresolved does. Files not
// present in deltas keep their passage manifest but lose their graph columns.
//
// The transaction holds the single writer for the whole rebuild (RFC 003
// section 3.6); callers serialise rebuilds with the per-index build lock.
func (g *SQLite) ReplaceAll(ctx context.Context, deltas []FileDelta) error {
	symRows, edgeRows, dirty, err := normalizeBulk(deltas)
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
	// and the ordered inserts do not maintain them row by row.
	for _, q := range []string{
		`DROP TRIGGER IF EXISTS symbols_fts_insert`,
		`DROP TRIGGER IF EXISTS symbols_fts_delete`,
		`DROP TRIGGER IF EXISTS symbols_fts_update`,
		`DROP INDEX IF EXISTS symbols_file`,
		`DROP INDEX IF EXISTS symbols_name`,
		`DROP INDEX IF EXISTS edges_dst`,
		`DROP INDEX IF EXISTS edges_file`,
		`DELETE FROM edges`,
		`DELETE FROM symbols`,
		`UPDATE files SET graph_hash = NULL, graph_dirty = 0`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("replacing graph: %s: %w", q, err)
		}
	}

	// All symbols first, in fqn order (the rowid table and its UNIQUE fqn
	// index append; ids follow lexical order).
	symIns, err := tx.PrepareContext(ctx, `INSERT INTO symbols
(fqn, name, kind, file, start_line, end_line, start_col, byte_offset, is_test, weight, doc)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replacing graph: preparing symbol insert: %w", err)
	}
	for _, s := range symRows {
		if _, err := symIns.ExecContext(ctx, s.fqn, s.name, s.kind, s.file, s.startLine, s.endLine, s.startCol, s.byteOffset, s.isTest, s.weight, s.doc); err != nil {
			symIns.Close()
			return fmt.Errorf("replacing graph: inserting symbol %q: %w", s.fqn, err)
		}
	}
	symIns.Close()

	// Then all edges, in primary-key order. INSERT OR IGNORE deduplicates
	// (src, dst, kind, file) exactly as the per-file path does; the rows
	// affected give the exact per-kind totals for the counters.
	edgeIns, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO edges (src, dst, kind, file, dst_external, confidence)
VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("replacing graph: preparing edge insert: %w", err)
	}
	edgeCounts := make(map[string]int64)
	for _, e := range edgeRows {
		res, err := edgeIns.ExecContext(ctx, e.src, e.dst, e.kind, e.file, e.external, e.confidence)
		if err != nil {
			edgeIns.Close()
			return fmt.Errorf("replacing graph: inserting edge %q -> %q: %w", e.src, e.dst, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			edgeIns.Close()
			return fmt.Errorf("replacing graph: inserting edge %q -> %q: %w", e.src, e.dst, err)
		}
		edgeCounts[e.kind] += n
	}
	edgeIns.Close()

	// Rebuild the secondary indexes (one sorted pass each) and the FTS index.
	// edges_file is intentionally not recreated: it is redundant with the
	// edges primary key under the owner invariant (store's edges_file_drop).
	for _, q := range []string{
		`CREATE INDEX symbols_file ON symbols(file)`,
		`CREATE INDEX symbols_name ON symbols(name)`,
		`CREATE INDEX edges_dst ON edges(dst, kind)`,
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
	if err := setGraphCounts(ctx, tx, int64(len(symRows)), edgeCounts); err != nil {
		return fmt.Errorf("replacing graph: %w", err)
	}

	// Advance the manifest graph columns for every delta file, in file order.
	now := time.Now().UnixNano()
	for _, d := range deltas {
		file := slashPath(d.File)
		var graphHash any
		if d.GraphHash != "" {
			graphHash = d.GraphHash
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO files (path, size, mtime_ns, hash, graph_hash, graph_dirty, indexed_at)
VALUES (?, 0, 0, '', ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET graph_hash = excluded.graph_hash, graph_dirty = excluded.graph_dirty`,
			file, graphHash, boolInt(dirty[file]), now); err != nil {
			return fmt.Errorf("replacing graph: writing files row %q: %w", file, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("replacing graph: committing: %w", err)
	}

	// Maintenance after commit (the data is already durable): reclaim free
	// pages left by a rebuild's wipe, then flush the WAL back into the
	// database and truncate it. The order matters: in WAL mode VACUUM writes
	// the whole database through the WAL, so checkpointing first left a
	// database-sized WAL behind (181 MiB at 1M edges). Both are best-effort —
	// a busy reader must not fail a committed rebuild.
	var free int64
	if err := g.writer.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err == nil && free > 0 {
		_, _ = g.writer.ExecContext(ctx, `VACUUM`)
	}
	_, _ = g.writer.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

// bulkSymbol and bulkEdge are the normalised, insert-ready rows of a rebuild.
type bulkSymbol struct {
	fqn, name, kind, file                    string
	startLine, endLine, startCol, byteOffset int64
	isTest                                   int
	weight                                   float64
	doc                                      string
}

type bulkEdge struct {
	src, dst, kind, file string
	external             int
	confidence           string
}

// normalizeBulk validates and normalises every delta against the complete
// symbol set, mirroring insertSymbols / insertEdges / checkSources /
// dropUnresolved exactly, and returns the rows sorted for ordered insertion
// plus the set of files whose unresolved internal targets mark them dirty.
func normalizeBulk(deltas []FileDelta) ([]bulkSymbol, []bulkEdge, map[string]bool, error) {
	owner := make(map[string]string) // fqn -> declaring file (duplicate detection + resolution)
	var symRows []bulkSymbol
	var edgeRows []bulkEdge
	dirty := make(map[string]bool)

	for _, d := range deltas {
		file := slashPath(d.File)
		if file == "" {
			return nil, nil, nil, fmt.Errorf("replacing graph: empty file path")
		}
		for _, s := range d.Symbols {
			if s.FQN == "" {
				return nil, nil, nil, fmt.Errorf("replacing graph: inserting symbols: empty FQN")
			}
			if prev, ok := owner[s.FQN]; ok {
				if prev == file {
					return nil, nil, nil, fmt.Errorf("replacing graph: inserting symbols: %q declared twice in the same delta", s.FQN)
				}
				return nil, nil, nil, fmt.Errorf("replacing graph: inserting symbols: %q is already declared in %q", s.FQN, prev)
			}
			sFile := slashPath(s.File)
			if sFile == "" {
				sFile = file
			}
			if sFile != file {
				return nil, nil, nil, fmt.Errorf("replacing graph: inserting symbols: %q is anchored in %q, not the delta's file %q", s.FQN, sFile, file)
			}
			weight := s.Weight
			if weight == 0 {
				weight = 1.0
			}
			isTest := 0
			if s.IsTest {
				isTest = 1
			}
			owner[s.FQN] = file
			symRows = append(symRows, bulkSymbol{
				fqn: s.FQN, name: s.Name, kind: s.Kind, file: sFile,
				startLine: s.StartLine, endLine: s.EndLine, startCol: s.StartCol, byteOffset: s.ByteOffset,
				isTest: isTest, weight: weight, doc: s.Doc,
			})
		}
	}

	// Edges are resolved against the complete symbol set, so order never
	// matters: an internal dst declared by any delta is kept on this pass.
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
			if srcFile, ok := owner[e.Src]; !ok || srcFile != file {
				return nil, nil, nil, fmt.Errorf("replacing graph: edge %q -> %q starts at a symbol with no declaration in this file", e.Src, e.Dst)
			}
			external := 0
			if e.DstExternal {
				external = 1
			}
			if external == 0 {
				if _, ok := owner[e.Dst]; !ok {
					dirty[file] = true // unresolved internal target: drop, mark owner dirty
					continue
				}
			}
			edgeRows = append(edgeRows, bulkEdge{src: e.Src, dst: e.Dst, kind: kind, file: file, external: external, confidence: confidence})
		}
	}

	sort.Slice(symRows, func(i, j int) bool { return symRows[i].fqn < symRows[j].fqn })
	sort.Slice(edgeRows, func(i, j int) bool {
		a, b := edgeRows[i], edgeRows[j]
		if a.src != b.src {
			return a.src < b.src
		}
		if a.dst != b.dst {
			return a.dst < b.dst
		}
		if a.kind != b.kind {
			return a.kind < b.kind
		}
		return a.file < b.file
	})
	return symRows, edgeRows, dirty, nil
}
