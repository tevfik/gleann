//go:build treesitter

package kuzu

import (
	"fmt"
	"os"
	"sort"
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

// Callees returns all symbols directly called by the given symbol FQN or name.
func (g *DB) Callees(callerFQN string) ([]gleann.Callee, error) {
	clean := strings.TrimSpace(callerFQN)
	parts := strings.FieldsFunc(clean, func(r rune) bool {
		return r == ':' || r == '.' || r == '/'
	})
	baseName := clean
	if len(parts) > 0 {
		baseName = parts[len(parts)-1]
	}

	cypher := fmt.Sprintf(
		`MATCH (a:Symbol)-[:CALLS]->(b:Symbol)
         WHERE a.fqn = %q OR a.name = %q OR a.name = %q OR a.fqn ENDS WITH %q OR a.fqn ENDS WITH %q
         RETURN DISTINCT b.fqn AS fqn, b.name AS name, b.kind AS kind`,
		clean, clean, baseName, "::"+clean, "."+clean,
	)
	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	return consumeCallees(res)
}

// Callers returns all symbols that call the given FQN or name.
func (g *DB) Callers(calleeFQN string) ([]gleann.Callee, error) {
	clean := strings.TrimSpace(calleeFQN)
	parts := strings.FieldsFunc(clean, func(r rune) bool {
		return r == ':' || r == '.' || r == '/'
	})
	baseName := clean
	if len(parts) > 0 {
		baseName = parts[len(parts)-1]
	}

	cypher := fmt.Sprintf(
		`MATCH (a:Symbol)-[:CALLS]->(b:Symbol)
         WHERE b.fqn = %q OR b.name = %q OR b.name = %q OR b.fqn ENDS WITH %q OR b.fqn ENDS WITH %q
         RETURN DISTINCT a.fqn AS fqn, a.name AS name, a.kind AS kind`,
		clean, clean, baseName, "::"+clean, "."+clean,
	)
	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
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
	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
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
	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
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
		MATCH (d:Document)
		WHERE d.vpath = "%s" OR d.rpath = "%s" OR d.vpath ENDS WITH "%s" OR d.rpath ENDS WITH "%s"
		MATCH (d)-[:HAS_CHUNK_DOC]->(c:Chunk)-[:EXPLAINS]->(sym:Symbol)
		RETURN DISTINCT sym.fqn AS fqn, sym.kind AS kind, sym.file AS file, sym.name AS name
	`, docPath, docPath, docPath, docPath)

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

	conn, err := g.NewConn()
	if err != nil {
		return nil, fmt.Errorf("error opening graph connection: %w", err)
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
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
	if hres, err := conn.Query(headingCypher); err == nil {
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

// DocumentTOC returns the hierarchical Table of Contents (TOC) tree of headings for a document.
func (g *DB) DocumentTOC(path string) (*gleann.DocumentTOCInfo, error) {
	// 1. Fetch Document info
	cypher := fmt.Sprintf(`
		MATCH (d:Document)
		WHERE d.vpath = "%s" OR d.rpath = "%s"
		OPTIONAL MATCH (f:Folder)-[:CONTAINS_DOC]->(d)
		RETURN d.vpath AS vpath, d.rpath AS rpath, d.name AS name,
		       coalesce(d.summary, "") AS summary, coalesce(f.name, "") AS folder
	`, path, path)

	conn, err := g.NewConn()
	if err != nil {
		return nil, fmt.Errorf("error opening graph connection: %w", err)
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
	if err != nil {
		return nil, fmt.Errorf("DocumentTOC document query: %w", err)
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

	vpath := strVal(m["vpath"])
	rpath := strVal(m["rpath"])
	name := strVal(m["name"])
	if name == "" {
		name = vpath
	}
	summary := strVal(m["summary"])
	folder := strVal(m["folder"])

	// 2. Fetch all headings directly attached to this document
	headingsCypher := fmt.Sprintf(`
		MATCH (d:Document)-[:HAS_HEADING]->(h:Heading)
		WHERE d.vpath = "%s" OR d.rpath = "%s"
		RETURN h.id AS id, h.name AS name, h.level AS level
	`, path, path)

	hres, err := conn.Query(headingsCypher)
	if err != nil {
		return nil, fmt.Errorf("DocumentTOC headings query: %w", err)
	}
	defer hres.Close()

	headingMap := make(map[string]*gleann.DocumentHeadingItem)
	totalNodes := 0

	for hres.HasNext() {
		hrow, err := hres.Next()
		if err != nil {
			continue
		}
		hm, err := hrow.GetAsMap()
		if err != nil {
			continue
		}
		id := strVal(hm["id"])
		hName := strVal(hm["name"])
		var level int
		if lvl, ok := hm["level"].(int64); ok {
			level = int(lvl)
		}
		if id != "" {
			headingMap[id] = &gleann.DocumentHeadingItem{
				ID:    id,
				Title: hName,
				Level: level,
			}
			totalNodes++
		}
	}

	// 3. Fetch child relationships among all headings
	childCypher := `
		MATCH (p:Heading)-[:CHILD_HEADING]->(c:Heading)
		RETURN p.id AS parent_id, c.id AS child_id
	`
	cres, err := conn.Query(childCypher)
	parentSet := make(map[string]bool)
	if err == nil {
		defer cres.Close()
		for cres.HasNext() {
			crow, err := cres.Next()
			if err != nil {
				continue
			}
			cm, err := crow.GetAsMap()
			if err != nil {
				continue
			}
			parentID := strVal(cm["parent_id"])
			childID := strVal(cm["child_id"])
			parentHeading, hasP := headingMap[parentID]
			childHeading, hasC := headingMap[childID]
			if hasP && hasC {
				parentHeading.Children = append(parentHeading.Children, *childHeading)
				parentSet[childID] = true
			}
		}
	}

	// 4. Assemble root headings
	var rootHeadings []gleann.DocumentHeadingItem
	for id, h := range headingMap {
		if !parentSet[id] {
			rootHeadings = append(rootHeadings, *h)
		}
	}

	sort.SliceStable(rootHeadings, func(i, j int) bool {
		return rootHeadings[i].Level < rootHeadings[j].Level
	})

	return &gleann.DocumentTOCInfo{
		VPath:      vpath,
		RPath:      rpath,
		Title:      name,
		Summary:    summary,
		Folder:     folder,
		Headings:   rootHeadings,
		TotalNodes: totalNodes,
	}, nil
}

// ListDocuments lists all indexed Document nodes with summaries and heading counts.
func (g *DB) ListDocuments() ([]gleann.DocumentTOCInfo, error) {
	cypher := `
		MATCH (d:Document)
		OPTIONAL MATCH (f:Folder)-[:CONTAINS_DOC]->(d)
		OPTIONAL MATCH (d)-[:HAS_HEADING]->(h:Heading)
		RETURN d.vpath AS vpath, d.rpath AS rpath, d.name AS name,
		       coalesce(d.summary, "") AS summary, coalesce(f.name, "") AS folder,
		       count(h) AS heading_count
		ORDER BY vpath ASC
	`
	conn, err := g.NewConn()
	if err != nil {
		return nil, fmt.Errorf("error opening graph connection: %w", err)
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
	if err != nil {
		return nil, fmt.Errorf("ListDocuments query: %w", err)
	}
	defer res.Close()

	var docs []gleann.DocumentTOCInfo
	for res.HasNext() {
		row, err := res.Next()
		if err != nil {
			continue
		}
		m, err := row.GetAsMap()
		if err != nil {
			continue
		}
		var hCount int
		if c, ok := m["heading_count"].(int64); ok {
			hCount = int(c)
		}
		vpath := strVal(m["vpath"])
		name := strVal(m["name"])
		if name == "" {
			name = vpath
		}
		docs = append(docs, gleann.DocumentTOCInfo{
			VPath:      vpath,
			RPath:      strVal(m["rpath"]),
			Title:      name,
			Summary:    strVal(m["summary"]),
			Folder:     strVal(m["folder"]),
			TotalNodes: hCount,
		})
	}
	return docs, nil
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

	conn, err := g.NewConn()
	if err != nil {
		return "", fmt.Errorf("error opening graph connection: %w", err)
	}
	defer conn.Close()

	res, err := conn.Query(cypher)
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
	conn, err := g.NewConn()
	if err != nil {
		return nil, fmt.Errorf("error opening graph connection: %w", err)
	}
	defer conn.Close()

	for _, sym := range allAffected {
		cypher := fmt.Sprintf(
			`MATCH (f:CodeFile)-[:DECLARES]->(s:Symbol {fqn: %q}) RETURN f.path AS path`,
			sym,
		)
		res, err := conn.Query(cypher)
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

	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var nextFrontier []string
		for _, node := range frontier {
			// Outgoing CALLS
			cypher := fmt.Sprintf(
				`MATCH (a:Symbol {fqn: %q})-[:CALLS]->(b:Symbol) RETURN b.fqn AS fqn, b.name AS name, b.kind AS kind`,
				node,
			)
			if res, err := conn.Query(cypher); err == nil {
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
			if res, err := conn.Query(cypher); err == nil {
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
			if res, err := conn.Query(cypher); err == nil {
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
			if res, err := conn.Query(cypher); err == nil {
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

	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

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
		if res, err := conn.Query(cypher); err == nil {
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
		if res, err := conn.Query(cypher); err == nil {
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
		if res, err := conn.Query(cypher); err == nil {
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
	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	res, err := conn.Query(cypher)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	return consumeCallees(res)
}

// Stats returns basic graph statistics.
func (g *DB) Stats() (*gleann.GraphStats, error) {
	stats := &gleann.GraphStats{}

	conn, err := g.NewConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Count files
	if res, err := conn.Query(`MATCH (f:CodeFile) RETURN count(f) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.Files = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count symbols
	if res, err := conn.Query(`MATCH (s:Symbol) RETURN count(s) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.Symbols = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count CALLS edges
	if res, err := conn.Query(`MATCH ()-[r:CALLS]->() RETURN count(r) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.CallEdges = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count DECLARES edges
	if res, err := conn.Query(`MATCH ()-[r:DECLARES]->() RETURN count(r) AS cnt`); err == nil {
		if res.HasNext() {
			row, _ := res.Next()
			m, _ := row.GetAsMap()
			stats.DeclareEdges = toInt(m["cnt"])
		}
		res.Close()
	}

	// Count IMPLEMENTS edges
	if res, err := conn.Query(`MATCH ()-[r:IMPLEMENTS]->() RETURN count(r) AS cnt`); err == nil {
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
