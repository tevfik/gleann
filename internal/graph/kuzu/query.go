package kuzu

import (
	"fmt"

	gokuzu "github.com/kuzudb/go-kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// consumeCallees iterates a QueryResult and converts rows to Callee using GetAsMap.
func consumeCallees(res *gokuzu.QueryResult) ([]gleann.Callee, error) {
	var out []gleann.Callee
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, err
		}
		out = append(out, gleann.Callee{
			FQN:  fmt.Sprint(m["fqn"]),
			Name: fmt.Sprint(m["name"]),
			Kind: fmt.Sprint(m["kind"]),
		})
	}
	return out, nil
}

// Callees returns all symbols directly called by the given symbol FQN.
func (g *DB) Callees(callerFQN string) ([]gleann.Callee, error) {
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
func (g *DB) Callers(calleeFQN string) ([]gleann.Callee, error) {
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
func (g *DB) SymbolsInFile(filePath string) ([]gleann.Callee, error) {
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

// DocumentSymbols returns all symbols explained or directly referenced by the given document path.
func (g *DB) DocumentSymbols(docPath string) ([]gleann.SymbolInfo, error) {
	// We use the raw connection to run a custom query for Document -> Section -> Chunk -> Symbol EXPLAINS
	// KuzuDB connection is not goroutine safe, so we get a new connection.
	conn, err := g.NewConn()
	if err != nil {
		return nil, fmt.Errorf("error opening graph connection: %w", err)
	}
	defer conn.Close()

	cypher := fmt.Sprintf(`
		MATCH (d:Document {path: "%s"})-[:HAS_SECTION]->(sec:Section)-[:HAS_CHUNK]->(c:DocChunk)-[:EXPLAINS]->(sym:Symbol)
		RETURN sym.fqn AS fqn, sym.kind AS kind, sym.file AS file, sym.name AS name
	`, docPath)

	res, err := conn.Query(cypher)
	if err != nil {
		return nil, fmt.Errorf("error executing graph query: %w", err)
	}
	defer res.Close()

	var out []gleann.SymbolInfo
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, fmt.Errorf("error reading graph result: %w", err)
		}

		m, err := row.GetAsMap()
		if err != nil {
			continue
		}

		out = append(out, gleann.SymbolInfo{
			FQN:  fmt.Sprint(m["fqn"]),
			Kind: fmt.Sprint(m["kind"]),
			File: fmt.Sprint(m["file"]),
			Name: fmt.Sprint(m["name"]),
		})
	}
	return out, nil
}
