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

// Impact performs a transitive caller analysis for the given symbol FQN.
// It traverses the CALLS graph backwards up to maxDepth hops to find all
// symbols and files that would be affected by a change to the given symbol.
func (g *DB) Impact(fqn string, maxDepth int) (*gleann.ImpactResult, error) {
	if maxDepth <= 0 || maxDepth > 10 {
		maxDepth = 5
	}

	result := &gleann.ImpactResult{
		Symbol: fqn,
		Depth:  maxDepth,
	}

	// Step 1: Direct callers.
	directCallers, err := g.Callers(fqn)
	if err != nil {
		return nil, fmt.Errorf("direct callers query: %w", err)
	}
	for _, c := range directCallers {
		result.DirectCallers = append(result.DirectCallers, c.FQN)
	}

	// Step 2: Transitive callers via BFS.
	visited := make(map[string]bool)
	visited[fqn] = true
	queue := make([]string, 0, len(directCallers))
	for _, c := range directCallers {
		if !visited[c.FQN] {
			visited[c.FQN] = true
			queue = append(queue, c.FQN)
		}
	}

	for depth := 1; depth < maxDepth && len(queue) > 0; depth++ {
		nextQueue := []string{}
		for _, sym := range queue {
			callers, err := g.Callers(sym)
			if err != nil {
				continue // graceful degradation
			}
			for _, c := range callers {
				if !visited[c.FQN] {
					visited[c.FQN] = true
					nextQueue = append(nextQueue, c.FQN)
					result.TransitiveCallers = append(result.TransitiveCallers, c.FQN)
				}
			}
		}
		queue = nextQueue
	}

	// Step 3: Collect affected files from all affected symbols.
	fileSet := make(map[string]bool)
	allAffected := append(result.DirectCallers, result.TransitiveCallers...)
	for _, sym := range allAffected {
		cypher := fmt.Sprintf(
			`MATCH (f:CodeFile)-[:DECLARES]->(s:Symbol {fqn: %q}) RETURN f.path AS path`,
			sym,
		)
		res, err := g.conn.Query(cypher)
		if err != nil {
			continue
		}
		for res.HasNext() {
			row, err := res.Next()
			if err != nil {
				break
			}
			m, err := row.GetAsMap()
			if err != nil {
				break
			}
			if path, ok := m["path"].(string); ok {
				fileSet[path] = true
			}
		}
		res.Close()
	}

	for f := range fileSet {
		result.AffectedFiles = append(result.AffectedFiles, f)
	}

	return result, nil
}
