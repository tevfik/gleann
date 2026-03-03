package kuzu

import (
	"fmt"

	gokuzu "github.com/kuzudb/go-kuzu"
)

// Callee holds a single symbol FQN returned from a graph traversal.
type Callee struct {
	FQN  string
	Name string
	Kind string
}

// consumeCallees iterates a QueryResult and converts rows to Callee using GetAsMap.
func consumeCallees(res *gokuzu.QueryResult) ([]Callee, error) {
	var out []Callee
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, err
		}
		out = append(out, Callee{
			FQN:  fmt.Sprint(m["fqn"]),
			Name: fmt.Sprint(m["name"]),
			Kind: fmt.Sprint(m["kind"]),
		})
	}
	return out, nil
}

// Callees returns all symbols directly called by the given symbol FQN.
func (g *DB) Callees(callerFQN string) ([]Callee, error) {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q})-[:CALLS]->(b:Symbol)
         RETURN b.fqn AS fqn, b.name AS name, b.kind AS kind`,
		callerFQN,
	)
	res, err := g.conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	return consumeCallees(res)
}

// Callers returns all symbols that call the given FQN.
func (g *DB) Callers(calleeFQN string) ([]Callee, error) {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol)-[:CALLS]->(b:Symbol {fqn: %q})
         RETURN a.fqn AS fqn, a.name AS name, a.kind AS kind`,
		calleeFQN,
	)
	res, err := g.conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	return consumeCallees(res)
}

// SymbolsInFile returns all symbols declared in the given file path.
func (g *DB) SymbolsInFile(filePath string) ([]Callee, error) {
	cypher := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q})-[:DECLARES]->(s:Symbol)
         RETURN s.fqn AS fqn, s.name AS name, s.kind AS kind`,
		filePath,
	)
	res, err := g.conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	return consumeCallees(res)
}
