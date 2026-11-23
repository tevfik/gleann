package graphstore

import "fmt"

// FileEdges returns the edges of one kind (CALLS, IMPLEMENTS or REFERENCES)
// that file owns, i.e. whose source is declared in file, ordered by src and
// dst. It is how an incremental sync reads back what a file's last delta
// stored before replacing it: a partial type-check cannot re-derive every
// method-set IMPLEMENTS edge, so the ones it cannot judge are carried over.
func (g *SQLite) FileEdges(file, kind string) ([]Edge, error) {
	file = slashPath(file)
	rows, err := g.reader.QueryContext(readCtx(), `SELECT sn.fqn, coalesce(ds.fqn, dn.fqn), k.label, e.dst_external, c.label
FROM symbols sn
JOIN graph_edges e ON e.src = sn.id
LEFT JOIN symbols ds ON ds.id = e.dst
LEFT JOIN graph_nodes dn ON dn.id = e.dst
JOIN graph_labels k ON k.id = e.kind
JOIN graph_labels c ON c.id = e.confidence
WHERE sn.file = ? AND k.label = ?
ORDER BY 1, 2`, file, kind)
	if err != nil {
		return nil, fmt.Errorf("listing %s edges of %q: %w", kind, file, err)
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		var external int
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind, &external, &e.Confidence); err != nil {
			return nil, fmt.Errorf("listing %s edges of %q: %w", kind, file, err)
		}
		e.DstExternal = external != 0
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing %s edges of %q: %w", kind, file, err)
	}
	return out, nil
}
