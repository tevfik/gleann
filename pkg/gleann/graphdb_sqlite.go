package gleann

import (
	"sort"

	"github.com/tevfik/gleann/internal/graphstore"
)

// sqliteGraphDB adapts the format-2 code graph (internal/graphstore, RFC 003
// docs/plans/003-storage.md sections 3.4 and 3.7) to the GraphDB interface.
// It is untagged: graph queries work in every build, including CGO_ENABLED=0
// and windows, because the graph lives in the index's own SQLite database
// rather than a Kùzu sidecar (RFC 003 section 5). It shares the passage
// store's connection pools, so Close is a no-op: the store is owned by the
// passage store and must not be closed twice.
type sqliteGraphDB struct {
	g *graphstore.SQLite
}

var _ GraphDB = (*sqliteGraphDB)(nil)

// hopToCallee maps a traversal hop to the Callee consumers see. The kuzu
// Callee carries no depth, so the hop's Depth is dropped.
func hopToCallee(h graphstore.Hop) Callee {
	return Callee{FQN: h.FQN, Name: h.Name, Kind: h.Kind, File: h.File, Line: h.Line, IsTest: h.IsTest}
}

// symbolToCallee maps a declaration to a Callee, using the anchor's start
// line as the line number (kuzu's Symbol.line).
func symbolToCallee(s graphstore.Symbol) Callee {
	return Callee{FQN: s.FQN, Name: s.Name, Kind: s.Kind, File: s.File, Line: s.StartLine, IsTest: s.IsTest}
}

// symbolToInfo maps a declaration to the detailed SymbolInfo.
func symbolToInfo(s graphstore.Symbol) SymbolInfo {
	return SymbolInfo{FQN: s.FQN, Kind: s.Kind, File: s.File, Name: s.Name, Line: s.StartLine, Weight: s.Weight, IsTest: s.IsTest}
}

// headingToItem converts the graphstore heading tree (single-parent schema)
// to the DocumentHeadingItem tree consumers see.
func headingToItem(h graphstore.Heading) DocumentHeadingItem {
	item := DocumentHeadingItem{ID: h.ID, Title: h.Title, Level: h.Level}
	if len(h.Children) > 0 {
		item.Children = make([]DocumentHeadingItem, 0, len(h.Children))
		for _, c := range h.Children {
			item.Children = append(item.Children, headingToItem(c))
		}
	}
	return item
}

// resolvedHops resolves a user-supplied symbol reference (a short name, a
// Type.Method, or a full FQN) to its direct neighbours, using graphstore's
// three-step lookup and merging the hops of every resolved seed the way
// kuzu.Callees/Callers return the union of a base name that matches several
// symbols. The result is deduplicated by FQN and ordered production-first
// then FQN (depth 1, matching kuzu's single hop).
func (d *sqliteGraphDB) resolvedHops(q string, dir graphstore.Direction, exact func(string, int) ([]graphstore.Hop, error)) ([]graphstore.Hop, error) {
	seeds, err := d.g.ResolveSymbols(q, dir)
	if err != nil {
		return nil, err
	}
	if len(seeds) == 0 {
		return nil, nil
	}
	seen := make(map[string]graphstore.Hop)
	for _, seed := range seeds {
		hops, err := exact(seed, 1)
		if err != nil {
			return nil, err
		}
		for _, h := range hops {
			if _, ok := seen[h.FQN]; !ok {
				seen[h.FQN] = h
			}
		}
	}
	out := make([]graphstore.Hop, 0, len(seen))
	for _, h := range seen {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsTest != out[j].IsTest {
			return !out[i].IsTest
		}
		return out[i].FQN < out[j].FQN
	})
	return out, nil
}

// Callees returns the symbols directly called by callerFQN, production first
// then FQN. callerFQN is resolved the way kuzu.Callees resolves it: exact FQN,
// then a qualified suffix, then the base name (the union when it is ambiguous).
func (d *sqliteGraphDB) Callees(callerFQN string) ([]Callee, error) {
	hops, err := d.resolvedHops(callerFQN, graphstore.DirectionCallees, d.g.Callees)
	if err != nil {
		return nil, err
	}
	out := make([]Callee, 0, len(hops))
	for _, h := range hops {
		out = append(out, hopToCallee(h))
	}
	return out, nil
}

// Callers returns the symbols that directly call calleeFQN, production first
// then FQN. calleeFQN is resolved the way kuzu.Callers resolves it: exact FQN,
// then a qualified suffix, then the base name (the union when it is ambiguous).
func (d *sqliteGraphDB) Callers(calleeFQN string) ([]Callee, error) {
	hops, err := d.resolvedHops(calleeFQN, graphstore.DirectionCallers, d.g.Callers)
	if err != nil {
		return nil, err
	}
	out := make([]Callee, 0, len(hops))
	for _, h := range hops {
		out = append(out, hopToCallee(h))
	}
	return out, nil
}

// SymbolsInFile returns every symbol declared in filePath, ordered by FQN.
func (d *sqliteGraphDB) SymbolsInFile(filePath string) ([]Callee, error) {
	syms, err := d.g.SymbolsInFile(filePath)
	if err != nil {
		return nil, err
	}
	out := make([]Callee, 0, len(syms))
	for _, s := range syms {
		out = append(out, symbolToCallee(s))
	}
	return out, nil
}

// DocumentSymbols returns the symbols explained by docPath's chunks.
func (d *sqliteGraphDB) DocumentSymbols(docPath string) ([]SymbolInfo, error) {
	syms, err := d.g.DocumentSymbols(docPath)
	if err != nil {
		return nil, err
	}
	out := make([]SymbolInfo, 0, len(syms))
	for _, s := range syms {
		out = append(out, symbolToInfo(s))
	}
	return out, nil
}

// DocumentContext returns the structural metadata and breadcrumb of a
// document. The format-2 schema stores only the physical rpath, so VPath is
// filled with it — the value kuzu's vpath would have shown for a document
// without an explicit virtual-path override.
func (d *sqliteGraphDB) DocumentContext(vpath string) (*DocumentContextData, error) {
	c, err := d.g.DocumentContext(vpath)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	return &DocumentContextData{
		VPath:      c.RPath,
		RPath:      c.RPath,
		Name:       c.Name,
		Summary:    c.Summary,
		FolderName: c.FolderName,
		Breadcrumb: c.Breadcrumb,
		Headings:   c.Headings,
	}, nil
}

// DocumentTOC returns the document's heading tree. VPath is filled with the
// physical rpath (the format-2 schema has no separate virtual path), matching
// the vpath kuzu would have shown without a virtual-path override.
func (d *sqliteGraphDB) DocumentTOC(vpath string) (*DocumentTOCInfo, error) {
	t, err := d.g.DocumentTOC(vpath)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	out := &DocumentTOCInfo{
		VPath:      t.RPath,
		RPath:      t.RPath,
		Title:      t.Title,
		Summary:    t.Summary,
		Folder:     t.Folder,
		TotalNodes: t.TotalNodes,
	}
	if len(t.Headings) > 0 {
		out.Headings = make([]DocumentHeadingItem, 0, len(t.Headings))
		for _, h := range t.Headings {
			out.Headings = append(out.Headings, headingToItem(h))
		}
	}
	return out, nil
}

// ListDocuments lists every indexed document with its folder, summary and
// direct heading count, ordered by physical path. VPath is filled with the
// physical rpath, as DocumentTOC does.
func (d *sqliteGraphDB) ListDocuments() ([]DocumentTOCInfo, error) {
	docs, err := d.g.ListDocuments()
	if err != nil {
		return nil, err
	}
	out := make([]DocumentTOCInfo, 0, len(docs))
	for _, doc := range docs {
		out = append(out, DocumentTOCInfo{
			VPath:      doc.RPath,
			RPath:      doc.RPath,
			Title:      doc.Title,
			Summary:    doc.Summary,
			Folder:     doc.Folder,
			TotalNodes: doc.HeadingCount,
		})
	}
	return out, nil
}

// FullDocument returns the complete text of a document.
func (d *sqliteGraphDB) FullDocument(vpath string) (string, error) {
	return d.g.FullDocument(vpath)
}

// Impact reports the blast radius of changing fqn. fqn is resolved the way
// kuzu.Impact resolves it (through Callers' three-step lookup), and the blast
// radius of every resolved symbol is merged, so a base name that matches
// several symbols reports their union.
func (d *sqliteGraphDB) Impact(fqn string, maxDepth int) (*ImpactResult, error) {
	seeds, err := d.g.ResolveSymbols(fqn, graphstore.DirectionCallers)
	if err != nil {
		return nil, err
	}
	// Normalise the depth the way kuzu.Impact does (graphstore normalises the
	// same way, so each seed's result agrees).
	depth := maxDepth
	if depth <= 0 || depth > 10 {
		depth = 5
	}
	res := &ImpactResult{Symbol: fqn, Depth: depth}
	if len(seeds) == 0 {
		return res, nil
	}
	// Two passes over the seeds mirror the former kuzu.Impact:
	// kuzu.Callers(fqn) returns the union of every resolved seed's direct
	// callers, all of which are enqueued in `visited` before the BFS expands.
	// A symbol that is a direct caller of any seed therefore never reappears
	// in TransitiveCallers, even when it is also a transitive caller of
	// another seed. The first pass collects every seed's direct (and test)
	// callers; the second adds a transitive caller only when it is not already
	// direct.
	type seedResult struct {
		direct, transitive, test, files []string
	}
	rs := make([]seedResult, 0, len(seeds))
	for _, seed := range seeds {
		r, err := d.g.Impact(seed, maxDepth)
		if err != nil {
			return nil, err
		}
		rs = append(rs, seedResult{
			direct: r.DirectCallers, transitive: r.TransitiveCallers,
			test: r.TestCallers, files: r.AffectedFiles,
		})
	}
	directSet := make(map[string]bool)
	testSet := make(map[string]bool)
	transSet := make(map[string]bool)
	fileSet := make(map[string]bool)
	for _, r := range rs {
		for _, c := range r.direct {
			if !directSet[c] {
				directSet[c] = true
				res.DirectCallers = append(res.DirectCallers, c)
			}
		}
		for _, c := range r.test {
			if !testSet[c] {
				testSet[c] = true
				res.TestCallers = append(res.TestCallers, c)
			}
		}
	}
	for _, r := range rs {
		for _, c := range r.transitive {
			if !directSet[c] && !transSet[c] {
				transSet[c] = true
				res.TransitiveCallers = append(res.TransitiveCallers, c)
			}
		}
		// AffectedFiles already holds each seed's direct+transitive callers'
		// files (a dropped transitive caller is still a direct caller of some
		// other seed, so its file is covered by that seed's direct set).
		for _, f := range r.files {
			fileSet[f] = true
		}
	}
	for f := range fileSet {
		res.AffectedFiles = append(res.AffectedFiles, f)
	}
	sort.Strings(res.DirectCallers)
	sort.Strings(res.TransitiveCallers)
	sort.Strings(res.TestCallers)
	sort.Strings(res.AffectedFiles)
	return res, nil
}

// Neighbors returns the edges around fqn within maxDepth hops.
func (d *sqliteGraphDB) Neighbors(fqn string, maxDepth int) ([]GraphEdge, error) {
	es, err := d.g.Neighbors(fqn, maxDepth)
	if err != nil {
		return nil, err
	}
	out := make([]GraphEdge, 0, len(es))
	for _, e := range es {
		out = append(out, GraphEdge{
			From:       e.From,
			To:         e.To,
			Relation:   e.Relation,
			TargetKind: e.TargetKind,
			Confidence: e.Confidence,
		})
	}
	return out, nil
}

// ShortestPath returns a shortest path between two symbols.
func (d *sqliteGraphDB) ShortestPath(fromFQN, toFQN string) ([]PathStep, error) {
	steps, err := d.g.Path(fromFQN, toFQN)
	if err != nil {
		return nil, err
	}
	out := make([]PathStep, 0, len(steps))
	for _, s := range steps {
		out = append(out, PathStep{From: s.From, To: s.To, Relation: s.Relation})
	}
	return out, nil
}

// SymbolSearch finds symbols whose FQN or name contains pattern
// (case-insensitive substring), capped at 50.
func (d *sqliteGraphDB) SymbolSearch(pattern string) ([]Callee, error) {
	syms, err := d.g.Symbols(pattern)
	if err != nil {
		return nil, err
	}
	out := make([]Callee, 0, len(syms))
	for _, s := range syms {
		out = append(out, symbolToCallee(s))
	}
	return out, nil
}

// Stats returns basic graph statistics.
func (d *sqliteGraphDB) Stats() (*GraphStats, error) {
	st, err := d.g.Stats()
	if err != nil {
		return nil, err
	}
	return &GraphStats{
		Files:           st.Files,
		Symbols:         st.Symbols,
		CallEdges:       st.CallEdges,
		DeclareEdges:    st.DeclareEdges,
		ImplementsEdges: st.ImplementsEdges,
	}, nil
}

// Close is a no-op: the underlying store is owned by the passage store and is
// closed with it (RFC 003 section 8 step 7: Close must not double-close the
// shared store).
func (d *sqliteGraphDB) Close() {}
