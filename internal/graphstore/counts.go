package graphstore

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"

	"github.com/tevfik/gleann/internal/store"
)

// graphEdgeKinds are the relation kinds the edges table holds and the meta
// counters track. Stats reads the CALLS and IMPLEMENTS counters; REFERENCES
// is maintained too so the counters always sum to the edge total.
var graphEdgeKinds = []string{"CALLS", "IMPLEMENTS", "REFERENCES"}

// fileRowCounts counts the rows a file owns before retraction: its symbols
// and its edges grouped by kind. Both queries are served by indexed lookups
// (symbols_file, and the edges primary key via the file's src symbols), so the
// accounting costs one indexed probe per file, not a table scan.
func fileRowCounts(ctx context.Context, tx *sql.Tx, file string) (int64, map[string]int64, error) {
	var syms int64
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM symbols WHERE file = ?`, file).Scan(&syms); err != nil {
		return 0, nil, fmt.Errorf("counting symbols of %q: %w", file, err)
	}
	edges := make(map[string]int64)
	rows, err := tx.QueryContext(ctx, `SELECT kind, count(*) FROM edges
WHERE src IN (SELECT fqn FROM symbols WHERE file = ?) GROUP BY kind`, file)
	if err != nil {
		return 0, nil, fmt.Errorf("counting edges of %q: %w", file, err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			return 0, nil, fmt.Errorf("counting edges of %q: %w", file, err)
		}
		edges[kind] = n
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("counting edges of %q: %w", file, err)
	}
	return syms, edges, nil
}

// inboundKindCounts groups the inbound edges read before retraction by kind:
// deleteFileRows deletes exactly these rows, so they are the inbound debit.
func inboundKindCounts(inbound []inboundEdge) map[string]int64 {
	out := make(map[string]int64)
	for _, e := range inbound {
		out[e.kind]++
	}
	return out
}

// bumpGraphCounts applies signed row deltas to the meta counters inside the
// write transaction, so a rollback reverts them with the rows. A missing
// counter means the graph_counts migration has not run; that is a schema
// error, not a silent drift.
func bumpGraphCounts(ctx context.Context, tx *sql.Tx, symDelta int64, edgeDeltas map[string]int64) error {
	if symDelta != 0 {
		if err := bumpCounter(ctx, tx, store.MetaGraphSymbols, symDelta); err != nil {
			return err
		}
	}
	kinds := make([]string, 0, len(edgeDeltas))
	for k := range edgeDeltas {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds) // deterministic write order
	for _, k := range kinds {
		if edgeDeltas[k] != 0 {
			if err := bumpCounter(ctx, tx, store.MetaGraphEdgesPrefix+k, edgeDeltas[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

func bumpCounter(ctx context.Context, tx *sql.Tx, key string, delta int64) error {
	res, err := tx.ExecContext(ctx, `UPDATE meta SET value = CAST(value AS INTEGER) + ? WHERE key = ?`, delta, key)
	if err != nil {
		return fmt.Errorf("updating graph counter %s: %w", key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("updating graph counter %s: %w", key, err)
	}
	if n != 1 {
		return fmt.Errorf("graph counter %s missing; schema not migrated", key)
	}
	return nil
}

// setGraphCounts writes absolute counter values (the bulk rebuild knows the
// exact totals it inserted, so it sets rather than bumps).
func setGraphCounts(ctx context.Context, tx *sql.Tx, syms int64, edges map[string]int64) error {
	if err := setCounter(ctx, tx, store.MetaGraphSymbols, syms); err != nil {
		return err
	}
	for _, k := range graphEdgeKinds {
		if err := setCounter(ctx, tx, store.MetaGraphEdgesPrefix+k, edges[k]); err != nil {
			return err
		}
	}
	return nil
}

func setCounter(ctx context.Context, tx *sql.Tx, key string, n int64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
  ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, strconv.FormatInt(n, 10)); err != nil {
		return fmt.Errorf("setting graph counter %s: %w", key, err)
	}
	return nil
}

// addKind merges src into dst (used to accumulate the per-kind credits and
// debits of one delta).
func addKind(dst, src map[string]int64) {
	for k, n := range src {
		dst[k] += n
	}
}

// subKind subtracts src from dst.
func subKind(dst, src map[string]int64) {
	for k, n := range src {
		dst[k] -= n
	}
}
