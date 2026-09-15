//go:build treesitter

package kuzu

import (
	"fmt"
	"os"
	"strings"

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

// SymbolsInFileDetailed returns all symbols declared in the given file
// path, including the language-aware weight assigned at ingest time.
// Used by the heuristics tests and by any future UI surface that wants
// to display "this interface is more important than that helper".
func (g *DB) SymbolsInFileDetailed(filePath string) ([]gleann.SymbolInfo, error) {
	cypher := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q})-[:DECLARES]->(s:Symbol)
         RETURN s.fqn AS fqn, s.kind AS kind, s.file AS file,
                s.name AS name, s.line AS line, s.weight AS weight`,
		filePath,
	)
	res, err := g.conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	var out []gleann.SymbolInfo
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return nil, err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return nil, err
		}
		// weight may come back as a numeric type or string depending on
		// Kuzu version; we round-trip through fmt to be safe.
		weightStr := fmt.Sprint(m["weight"])
		var weight float64
		if _, err := fmt.Sscanf(weightStr, "%f", &weight); err != nil {
			weight = 1.0
		}
		out = append(out, gleann.SymbolInfo{
			FQN:    fmt.Sprint(m["fqn"]),
			Kind:   fmt.Sprint(m["kind"]),
			File:   fmt.Sprint(m["file"]),
			Name:   fmt.Sprint(m["name"]),
			Line:   0, // Kuzu's INT64 conversion to interface{} needs care; left as 0
			Weight: weight,
		})
	}
	return out, nil
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

// DocumentContext fetches hierarchical structural information and summary for a document by vpath or rpath.
func (g *DB) DocumentContext(path string) (*gleann.DocumentContextData, error) {
	cypher := fmt.Sprintf(`
		MATCH (d:Document)
		WHERE d.vpath = "%s" OR d.rpath = "%s"
		OPTIONAL MATCH (f:Folder)-[:CONTAINS_DOC]->(d)
		RETURN d.vpath AS vpath, d.rpath AS rpath, d.name AS name,
		       coalesce(d.summary, "") AS summary, coalesce(f.name, "") AS folder
	`, path, path)

	res, err := g.conn.Query(cypher)
	if err != nil {
		return nil, fmt.Errorf("DocumentContext query: %w", err)
	}
	defer res.Close()

	if !res.HasNext() {
		return nil, fmt.Errorf("no Document found with path: %s", path)
	}

	row, err := res.Next()
	if err != nil {
		return nil, err
	}

	m, err := row.GetAsMap()
	if err != nil {
		return nil, err
	}

	// Fetch associated headings to build hierarchical breadcrumb
	headingCypher := fmt.Sprintf(`
		MATCH (d:Document)-[:HAS_HEADING]->(h:Heading)
		WHERE d.vpath = "%s" OR d.rpath = "%s"
		RETURN h.name AS name, h.level AS level
		ORDER BY h.level ASC
	`, path, path)

	var headings []string
	if hres, err := g.conn.Query(headingCypher); err == nil {
		defer hres.Close()
		for hres.HasNext() {
			if hrow, err := hres.Next(); err == nil {
				if hm, err := hrow.GetAsMap(); err == nil {
					hName := strVal(hm["name"])
					if hName != "" {
						headings = append(headings, hName)
					}
				}
			}
		}
	}

	folder := strVal(m["folder"])
	docName := strVal(m["name"])
	if docName == "" {
		docName = strVal(m["vpath"])
	}

	var bParts []string
	if folder != "" {
		bParts = append(bParts, folder)
	}
	if docName != "" {
		bParts = append(bParts, docName)
	}
	if len(headings) > 0 {
		bParts = append(bParts, headings...)
	}
	breadcrumb := strings.Join(bParts, " > ")

	return &gleann.DocumentContextData{
		VPath:      strVal(m["vpath"]),
		RPath:      strVal(m["rpath"]),
		Name:       strVal(m["name"]),
		Summary:    strVal(m["summary"]),
		FolderName: folder,
		Breadcrumb: breadcrumb,
		Headings:   headings,
	}, nil
}

// strVal safely converts an interface{} from a KuzuDB map to string, returning "" for nil.
func strVal(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// FullDocument retrieves the complete document content by querying KuzuDB for its
// on-disk physical path (rpath) or reconstructing it from the graph chunks.
func (g *DB) FullDocument(path string) (string, error) {
	// 1. Fast path: If physical file exists on disk, read it directly
	docCtx, err := g.DocumentContext(path)
	if err == nil && docCtx != nil && docCtx.RPath != "" {
		if data, err := os.ReadFile(docCtx.RPath); err == nil && len(data) > 0 {
			return string(data), nil
		}
	}

	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data), nil
	}

	// 2. Graph reconstruction fallback: Retrieve all chunks for this document
	cypher := fmt.Sprintf(`
		MATCH (d:Document)-[:HAS_CHUNK_DOC]->(c:Chunk)
		WHERE d.vpath = "%s" OR d.rpath = "%s"
		RETURN c.text AS content
		ORDER BY c.start_char ASC
	`, path, path)

	res, err := g.conn.Query(cypher)
	if err != nil {
		return "", fmt.Errorf("FullDocument query: %w", err)
	}
	defer res.Close()

	var parts []string
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			return "", err
		}
		m, err := row.GetAsMap()
		if err != nil {
			return "", err
		}
		if content, ok := m["content"]; ok {
			parts = append(parts, fmt.Sprint(content))
		}
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no content found for document: %s", path)
	}

	return strings.Join(parts, "\n\n"), nil
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

// Neighbors returns all symbols directly connected to the given FQN (both directions, all edge types).
func (g *DB) Neighbors(fqn string, maxDepth int) ([]gleann.GraphEdge, error) {
	if maxDepth <= 0 {
		maxDepth = 1
	}
	if maxDepth > 5 {
		maxDepth = 5
	}

	var edges []gleann.GraphEdge
	visited := map[string]bool{fqn: true}
	frontier := []string{fqn}

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var nextFrontier []string
		for _, node := range frontier {
			// Outgoing CALLS
			cypher := fmt.Sprintf(
				`MATCH (a:Symbol {fqn: %q})-[:CALLS]->(b:Symbol) RETURN b.fqn AS fqn, b.name AS name, b.kind AS kind`,
				node,
			)
			if res, err := g.conn.Query(cypher); err == nil {
				for res.HasNext() {
					row, _ := res.Next()
					m, _ := row.GetAsMap()
					target := fmt.Sprint(m["fqn"])
					edges = append(edges, gleann.GraphEdge{
						From: node, To: target, Relation: "CALLS",
						TargetKind: fmt.Sprint(m["kind"]), Confidence: "extracted",
					})
					if !visited[target] {
						visited[target] = true
						nextFrontier = append(nextFrontier, target)
					}
				}
				res.Close()
			}

			// Incoming CALLS
			cypher = fmt.Sprintf(
				`MATCH (a:Symbol)-[:CALLS]->(b:Symbol {fqn: %q}) RETURN a.fqn AS fqn, a.name AS name, a.kind AS kind`,
				node,
			)
			if res, err := g.conn.Query(cypher); err == nil {
				for res.HasNext() {
					row, _ := res.Next()
					m, _ := row.GetAsMap()
					source := fmt.Sprint(m["fqn"])
					edges = append(edges, gleann.GraphEdge{
						From: source, To: node, Relation: "CALLS",
						TargetKind: fmt.Sprint(m["kind"]), Confidence: "extracted",
					})
					if !visited[source] {
						visited[source] = true
						nextFrontier = append(nextFrontier, source)
					}
				}
				res.Close()
			}

			// IMPLEMENTS
			cypher = fmt.Sprintf(
				`MATCH (a:Symbol {fqn: %q})-[:IMPLEMENTS]->(b:Symbol) RETURN b.fqn AS fqn, b.name AS name, b.kind AS kind`,
				node,
			)
			if res, err := g.conn.Query(cypher); err == nil {
				for res.HasNext() {
					row, _ := res.Next()
					m, _ := row.GetAsMap()
					target := fmt.Sprint(m["fqn"])
					edges = append(edges, gleann.GraphEdge{
						From: node, To: target, Relation: "IMPLEMENTS",
						TargetKind: fmt.Sprint(m["kind"]), Confidence: "extracted",
					})
					if !visited[target] {
						visited[target] = true
						nextFrontier = append(nextFrontier, target)
					}
				}
				res.Close()
			}

			// REFERENCES
			cypher = fmt.Sprintf(
				`MATCH (a:Symbol {fqn: %q})-[:REFERENCES]->(b:Symbol) RETURN b.fqn AS fqn, b.name AS name, b.kind AS kind`,
				node,
			)
			if res, err := g.conn.Query(cypher); err == nil {
				for res.HasNext() {
					row, _ := res.Next()
					m, _ := row.GetAsMap()
					target := fmt.Sprint(m["fqn"])
					edges = append(edges, gleann.GraphEdge{
						From: node, To: target, Relation: "REFERENCES",
						TargetKind: fmt.Sprint(m["kind"]), Confidence: "extracted",
					})
					if !visited[target] {
						visited[target] = true
						nextFrontier = append(nextFrontier, target)
					}
				}
				res.Close()
			}
		}
		frontier = nextFrontier
	}

	return edges, nil
}

// ShortestPath finds the shortest path between two symbols using BFS over CALLS edges.
func (g *DB) ShortestPath(fromFQN, toFQN string) ([]gleann.PathStep, error) {
	type bfsNode struct {
		fqn    string
		parent string
		edge   string
	}

	visited := map[string]bool{fromFQN: true}
	queue := []bfsNode{{fqn: fromFQN}}
	parents := map[string]bfsNode{fromFQN: {fqn: fromFQN}}

	for len(queue) > 0 && len(visited) < 1000 {
		current := queue[0]
		queue = queue[1:]

		if current.fqn == toFQN {
			// Reconstruct path
			var path []gleann.PathStep
			node := current.fqn
			for node != fromFQN {
				p := parents[node]
				path = append([]gleann.PathStep{{
					From: p.parent, To: node, Relation: p.edge,
				}}, path...)
				node = p.parent
			}
			return path, nil
		}

		// Expand outgoing CALLS
		cypher := fmt.Sprintf(
			`MATCH (a:Symbol {fqn: %q})-[:CALLS]->(b:Symbol) RETURN b.fqn AS fqn`,
			current.fqn,
		)
		if res, err := g.conn.Query(cypher); err == nil {
			for res.HasNext() {
				row, _ := res.Next()
				m, _ := row.GetAsMap()
				next := fmt.Sprint(m["fqn"])
				if !visited[next] {
					visited[next] = true
					parents[next] = bfsNode{fqn: next, parent: current.fqn, edge: "CALLS"}
					queue = append(queue, bfsNode{fqn: next})
				}
			}
			res.Close()
		}

		// Expand incoming CALLS
		cypher = fmt.Sprintf(
			`MATCH (a:Symbol)-[:CALLS]->(b:Symbol {fqn: %q}) RETURN a.fqn AS fqn`,
			current.fqn,
		)
		if res, err := g.conn.Query(cypher); err == nil {
			for res.HasNext() {
				row, _ := res.Next()
				m, _ := row.GetAsMap()
				next := fmt.Sprint(m["fqn"])
				if !visited[next] {
					visited[next] = true
					parents[next] = bfsNode{fqn: next, parent: current.fqn, edge: "CALLED_BY"}
					queue = append(queue, bfsNode{fqn: next})
				}
			}
			res.Close()
		}

		// Expand IMPLEMENTS
		cypher = fmt.Sprintf(
			`MATCH (a:Symbol {fqn: %q})-[:IMPLEMENTS]->(b:Symbol) RETURN b.fqn AS fqn`,
			current.fqn,
		)
		if res, err := g.conn.Query(cypher); err == nil {
			for res.HasNext() {
				row, _ := res.Next()
				m, _ := row.GetAsMap()
				next := fmt.Sprint(m["fqn"])
				if !visited[next] {
					visited[next] = true
					parents[next] = bfsNode{fqn: next, parent: current.fqn, edge: "IMPLEMENTS"}
					queue = append(queue, bfsNode{fqn: next})
				}
			}
			res.Close()
		}
	}

	return nil, fmt.Errorf("no path found between %q and %q (explored %d nodes)", fromFQN, toFQN, len(visited))
}

// SymbolSearch searches for symbols whose FQN or name contains the given substring (case-insensitive).
func (g *DB) SymbolSearch(pattern string) ([]gleann.Callee, error) {
	cypher := fmt.Sprintf(
		`MATCH (s:Symbol)
		 WHERE lower(s.fqn) CONTAINS %q OR lower(s.name) CONTAINS %q
		 RETURN s.fqn AS fqn, s.name AS name, s.kind AS kind
		 LIMIT 50`,
		strings.ToLower(pattern), strings.ToLower(pattern),
	)
	res, err := g.conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	return consumeCallees(res)
}

// Stats returns basic graph statistics.
func (g *DB) Stats() (*gleann.GraphStats, error) {
	stats := &gleann.GraphStats{}

	// Count files
	if res, err := g.conn.Query(`MATCH (f:CodeFile) RETURN count(f) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.Files = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count symbols
	if res, err := g.conn.Query(`MATCH (s:Symbol) RETURN count(s) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.Symbols = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count CALLS edges
	if res, err := g.conn.Query(`MATCH ()-[r:CALLS]->() RETURN count(r) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.CallEdges = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count DECLARES edges
	if res, err := g.conn.Query(`MATCH ()-[r:DECLARES]->() RETURN count(r) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.DeclareEdges = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count IMPLEMENTS edges
	if res, err := g.conn.Query(`MATCH ()-[r:IMPLEMENTS]->() RETURN count(r) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.ImplementsEdges = toInt(m["cnt"])
		}
		res.Close()
	}

	return stats, nil
}

func toInt(v any) int {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
