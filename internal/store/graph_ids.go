package store

import (
	"database/sql"
	"fmt"
)

// Label ids the graph_ids step seeds into graph_labels. internal/graphstore
// writes them as literals into its queries (e.kind = 1), so they are fixed for
// the life of the schema; any other kind or confidence string gets the next
// free id on first use.
const (
	GraphLabelCalls      = 1
	GraphLabelImplements = 2
	GraphLabelReferences = 3
)

// schemaGraphIDs is the integer-keyed code graph (RFC 004 item E). An edge
// endpoint is a node id: a declared symbol's symbols.id, or, for a target no
// symbol declares (third-party, standard library), the id of its graph_nodes
// row. The two tables share one id space and an FQN lives in exactly one of
// them, so an edge row is four small integers plus a flag instead of two FQNs,
// a kind, a file path and a confidence string. The owning file is not stored:
// the owner invariant (graphstore.checkSources) makes it the file of the src
// symbol, so the primary key (src, dst, kind) is the old (src, dst, kind,
// file) key. Kind and confidence strings are interned in graph_labels.
//
// The view edges keeps the old text shape for ad-hoc SQL and tests; the
// graphstore queries read graph_edges directly.
const schemaGraphIDs = `
-- edge targets with no symbols row; ids disjoint from symbols.id
CREATE TABLE IF NOT EXISTS graph_nodes (
  id  INTEGER PRIMARY KEY,
  fqn TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS graph_labels (
  id    INTEGER PRIMARY KEY,
  label TEXT NOT NULL UNIQUE
);
INSERT OR IGNORE INTO graph_labels (id, label) VALUES (1, 'CALLS'), (2, 'IMPLEMENTS'), (3, 'REFERENCES');
CREATE TABLE IF NOT EXISTS graph_edges (
  src          INTEGER NOT NULL,           -- symbols.id of the caller
  dst          INTEGER NOT NULL,           -- symbols.id, or graph_nodes.id for an undeclared target
  kind         INTEGER NOT NULL,           -- graph_labels.id: CALLS | IMPLEMENTS | REFERENCES
  dst_external INTEGER NOT NULL DEFAULT 0, -- 1: third-party or standard-library target
  confidence   INTEGER NOT NULL,           -- graph_labels.id
  PRIMARY KEY (src, dst, kind)
) WITHOUT ROWID;`

// GraphEdgesDstIndex is the reverse-walk index of graph_edges (callers,
// impact, inbound edges). graphstore's bulk rebuild drops it while loading and
// recreates it from this definition.
const GraphEdgesDstIndex = `CREATE INDEX IF NOT EXISTS graph_edges_dst ON graph_edges(dst, kind)`

// graphEdgesView is the old text-keyed edges table as a read-only view.
const graphEdgesView = `
CREATE VIEW IF NOT EXISTS edges (src, dst, kind, file, dst_external, confidence) AS
SELECT s.fqn, coalesce(ds.fqn, dn.fqn), k.label, s.file, e.dst_external, c.label
FROM graph_edges e
JOIN symbols s ON s.id = e.src
LEFT JOIN symbols ds ON ds.id = e.dst
LEFT JOIN graph_nodes dn ON dn.id = e.dst
JOIN graph_labels k ON k.id = e.kind
JOIN graph_labels c ON c.id = e.confidence;`

// migrateGraphIDs converts the text-keyed edges table to graph_edges in place
// (no re-extraction): symbols keep their ids; edge targets without a symbols
// row (external ones) get graph_nodes ids above every symbol id; the rows are
// copied in primary-key order and the edges table is replaced by the view.
// The counters are re-seeded from the converted rows. An edge whose src has
// no declaration cannot exist under the owner invariant and is dropped. The
// pages the old table and its index held stay in the file as free pages
// until the next full rebuild (graphstore.ReplaceAll VACUUMs when a tenth of
// the file is free); a migration does not VACUUM, it runs inside the open.
func migrateGraphIDs(tx *sql.Tx) error {
	if _, err := tx.Exec(schemaGraphIDs); err != nil {
		return fmt.Errorf("creating graph_edges: %w", err)
	}
	isTable, err := tableExists(tx, "edges")
	if err != nil {
		return err
	}
	if isTable {
		for _, q := range []string{
			`INSERT INTO graph_nodes (id, fqn)
SELECT (SELECT coalesce(max(id), 0) FROM symbols) + row_number() OVER (ORDER BY dst), dst
FROM (SELECT DISTINCT dst FROM edges WHERE dst NOT IN (SELECT fqn FROM symbols))`,
			`INSERT OR IGNORE INTO graph_labels (label) SELECT DISTINCT kind FROM edges ORDER BY kind`,
			`INSERT OR IGNORE INTO graph_labels (label) SELECT DISTINCT confidence FROM edges ORDER BY confidence`,
			`INSERT OR IGNORE INTO graph_edges (src, dst, kind, dst_external, confidence)
SELECT s.id, coalesce(ds.id, dn.id), k.id, e.dst_external, c.id
FROM edges e
JOIN symbols s ON s.fqn = e.src
LEFT JOIN symbols ds ON ds.fqn = e.dst
LEFT JOIN graph_nodes dn ON dn.fqn = e.dst
JOIN graph_labels k ON k.label = e.kind
JOIN graph_labels c ON c.label = e.confidence
ORDER BY 1, 2, 3`,
			`DROP TABLE edges`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return fmt.Errorf("converting edges: %w", err)
			}
		}
	}
	if _, err := tx.Exec(GraphEdgesDstIndex); err != nil {
		return fmt.Errorf("creating graph_edges_dst: %w", err)
	}
	if _, err := tx.Exec(graphEdgesView); err != nil {
		return fmt.Errorf("creating edges view: %w", err)
	}
	for kind, id := range map[string]int{"CALLS": GraphLabelCalls, "IMPLEMENTS": GraphLabelImplements, "REFERENCES": GraphLabelReferences} {
		var n int64
		if err := tx.QueryRow(`SELECT count(*) FROM graph_edges WHERE kind = ?`, id).Scan(&n); err != nil {
			return fmt.Errorf("counting %s edges: %w", kind, err)
		}
		if err := upsertMeta(tx, MetaGraphEdgesPrefix+kind, n); err != nil {
			return err
		}
	}
	return nil
}
