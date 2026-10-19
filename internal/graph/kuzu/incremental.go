//go:build treesitter

package kuzu

import (
	"fmt"
	"strings"
)

// IncomingEdges holds the edges that point into the symbols of some files from
// symbols outside those files.
type IncomingEdges struct {
	Calls      []EdgeCalls
	Implements []EdgeImplements
	References []EdgeReferences
}

// IncomingEdges returns the CALLS, IMPLEMENTS and REFERENCES edges whose target
// is declared in one of files and whose source is declared elsewhere. Removing
// a file's symbols deletes these edges; an incremental update has to put them
// back, because the files they start from did not change and will not be
// re-parsed.
func (g *DB) IncomingEdges(files []string) (IncomingEdges, error) {
	var out IncomingEdges
	if len(files) == 0 {
		return out, nil
	}
	conn, err := g.NewConn()
	if err != nil {
		return out, err
	}
	defer conn.Close()

	list := cypherList(files)

	// "Declared by one of files" goes through DECLARES, the relation that
	// RemoveFileSymbols deletes along: a symbol's own file property can be empty
	// when a caller's placeholder node was written before the real declaration.
	declared := make(map[string]bool)
	{
		res, err := conn.Query(fmt.Sprintf(`MATCH (f:CodeFile)-[:DECLARES]->(s:Symbol) WHERE f.path IN %s RETURN s.fqn AS fqn`, list))
		if err != nil {
			return out, fmt.Errorf("symbols declared by %d file(s): %w", len(files), err)
		}
		for res.HasNext() {
			row, err := res.Next()
			if err != nil {
				res.Close()
				return out, err
			}
			m, err := row.GetAsMap()
			if err != nil {
				res.Close()
				return out, err
			}
			declared[fmt.Sprint(m["fqn"])] = true
		}
		res.Close()
	}
	if len(declared) == 0 {
		return out, nil
	}

	run := func(rel string, each func(a, b string)) error {
		res, err := conn.Query(fmt.Sprintf(
			`MATCH (f:CodeFile)-[:DECLARES]->(b:Symbol)<-[:%s]-(a:Symbol) WHERE f.path IN %s RETURN a.fqn AS a, b.fqn AS b`,
			rel, list))
		if err != nil {
			return fmt.Errorf("incoming %s edges: %w", rel, err)
		}
		defer res.Close()
		for res.HasNext() {
			row, err := res.Next()
			if err != nil {
				return err
			}
			m, err := row.GetAsMap()
			if err != nil {
				return err
			}
			a, b := fmt.Sprint(m["a"]), fmt.Sprint(m["b"])
			if declared[a] {
				continue // starts in a changed file: re-derived by the re-parse
			}
			each(a, b)
		}
		return nil
	}
	if err := run("CALLS", func(a, b string) {
		out.Calls = append(out.Calls, EdgeCalls{CallerFQN: a, CalleeFQN: b})
	}); err != nil {
		return out, err
	}
	if err := run("IMPLEMENTS", func(a, b string) {
		out.Implements = append(out.Implements, EdgeImplements{ImplFQN: a, IfaceFQN: b})
	}); err != nil {
		return out, err
	}
	if err := run("REFERENCES", func(a, b string) {
		out.References = append(out.References, EdgeReferences{RefererFQN: a, RefereeFQN: b})
	}); err != nil {
		return out, err
	}
	return out, nil
}

// ExistingSymbolFQNs reports which of fqns are already Symbol nodes.
func (g *DB) ExistingSymbolFQNs(fqns []string) (map[string]bool, error) {
	found := make(map[string]bool)
	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	const chunk = 500
	for start := 0; start < len(fqns); start += chunk {
		end := start + chunk
		if end > len(fqns) {
			end = len(fqns)
		}
		res, err := conn.Query(fmt.Sprintf(`MATCH (s:Symbol) WHERE s.fqn IN %s RETURN s.fqn AS fqn`, cypherList(fqns[start:end])))
		if err != nil {
			return nil, fmt.Errorf("existing symbols: %w", err)
		}
		for res.HasNext() {
			row, err := res.Next()
			if err != nil {
				res.Close()
				return nil, err
			}
			m, err := row.GetAsMap()
			if err != nil {
				res.Close()
				return nil, err
			}
			found[fmt.Sprint(m["fqn"])] = true
		}
		res.Close()
	}
	return found, nil
}

func cypherList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(q, ",") + "]"
}
