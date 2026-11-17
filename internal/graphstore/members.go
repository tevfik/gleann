package graphstore

import (
	"fmt"
	"sort"
	"strings"
)

// PackageMembers lists the member declarations ("Type::member" names, the
// naming of the tree-sitter languages) whose FQN starts with prefix+".".
// prefix is a package FQN prefix; "" selects every member. It is the lookup the
// incremental write path uses to resolve a call to a method declared in
// another file of the same package.
func (g *SQLite) PackageMembers(prefix string) ([]Symbol, error) {
	if prefix == "" {
		return g.symbolsQuery(`SELECT ` + symbolCols + ` FROM symbols WHERE instr(name, '::') > 0 ORDER BY fqn`)
	}
	p := prefix + "."
	return g.symbolsQuery(`SELECT `+symbolCols+` FROM symbols WHERE substr(fqn, 1, length(?)) = ? AND instr(name, '::') > 0 ORDER BY fqn`, p, p)
}

// FilesWithExternalCalls returns the files (sorted) that hold an external
// CALLS edge to one of dsts. A declaration that appears later turns such an
// edge internal, so the files it lists are the ones to re-extract.
func (g *SQLite) FilesWithExternalCalls(dsts []string) ([]string, error) {
	seen := make(map[string]bool)
	const chunk = 512
	for start := 0; start < len(dsts); start += chunk {
		end := min(start+chunk, len(dsts))
		ph := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, end-start)
		for i, d := range dsts[start:end] {
			args[i] = d
		}
		rows, err := g.reader.QueryContext(readCtx(), `SELECT DISTINCT s.file FROM graph_edges e
JOIN symbols s ON s.id = e.src
WHERE e.dst IN (SELECT id FROM symbols WHERE fqn IN (`+ph+`) UNION ALL SELECT id FROM graph_nodes WHERE fqn IN (`+ph+`))
  AND e.kind = `+labelCalls+` AND e.dst_external = 1`, append(append(make([]any, 0, 2*len(args)), args...), args...)...)
		if err != nil {
			return nil, fmt.Errorf("listing files with external calls: %w", err)
		}
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err != nil {
				rows.Close()
				return nil, fmt.Errorf("listing files with external calls: %w", err)
			}
			seen[f] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("listing files with external calls: %w", err)
		}
		rows.Close()
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}
