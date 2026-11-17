package graphstore

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// DocNode is one row of the document hierarchy (doc_nodes, RFC 003 section
// 3.4): a folder, document, heading or chunk with a parent pointer. The
// single parent column replaces Kùzu's six node and relation tables; the
// hierarchy reads of RFC 003 section 6.2 become indexed parent lookups.
type DocNode struct {
	ID        string
	Type      string // folder | document | heading | chunk
	Parent    string // "" when NULL (root)
	Name      string
	Level     int
	RPath     string // physical path on disk (documents)
	Hash      string
	Summary   string
	Text      string // chunk content
	StartChar int64
	EndChar   int64
}

// Heading is one node of a table of contents (mirrors
// gleann.DocumentHeadingItem).
type Heading struct {
	ID       string
	Title    string
	Level    int
	Children []Heading
}

// DocumentTOC is a document's metadata plus its heading tree (mirrors
// gleann.DocumentTOCInfo without VPath: the RFC 003 section 3.4 schema keeps
// only the physical rpath, so there is no virtual path to report).
type DocumentTOC struct {
	RPath      string
	Title      string
	Summary    string
	Folder     string
	Headings   []Heading
	TotalNodes int
}

// DocumentContext is the structural metadata and breadcrumb of a document
// (mirrors gleann.DocumentContextData without VPath, see DocumentTOC).
type DocumentContext struct {
	RPath      string
	Name       string
	Summary    string
	FolderName string
	Breadcrumb string
	Headings   []string
}

// DocumentInfo is one entry of ListDocuments (the flat part of
// gleann.DocumentTOCInfo, with the direct heading count).
type DocumentInfo struct {
	RPath        string
	Title        string
	Summary      string
	Folder       string
	HeadingCount int
}

// DocumentStore is the second small interface of RFC 003 section 3.2: the
// document hierarchy over doc_nodes and doc_explains, backed by plain
// tables instead of Kùzu's six node types.
type DocumentStore interface {
	DocumentTOC(path string) (*DocumentTOC, error)
	DocumentContext(path string) (*DocumentContext, error)
	ListDocuments() ([]DocumentInfo, error)
	FullDocument(path string) (string, error)
	DocumentSymbols(path string) ([]Symbol, error)
}

var _ DocumentStore = (*SQLite)(nil)

const docNodeCols = `id, type, parent, name, level, rpath, hash, summary, text, start_char, end_char`

func scanDocNode(sc scanner) (DocNode, error) {
	var n DocNode
	var parent, name, rpath, hash, summary, text sql.NullString
	var level, startChar, endChar sql.NullInt64
	if err := sc.Scan(&n.ID, &n.Type, &parent, &name, &level, &rpath, &hash, &summary, &text, &startChar, &endChar); err != nil {
		return DocNode{}, err
	}
	n.Parent, n.Name, n.RPath = parent.String, name.String, rpath.String
	n.Hash, n.Summary, n.Text = hash.String, summary.String, text.String
	n.Level, n.StartChar, n.EndChar = int(level.Int64), startChar.Int64, endChar.Int64
	return n, nil
}

// findDocument locates a document node by physical path or node ID. Kùzu
// matched vpath or rpath, both exactly and by suffix (ENDS WITH); the new
// schema has only rpath (section 3.4), so the ID doubles as the second exact
// key and the suffix fallback matches a bare filename such as "guide.md"
// against a document stored at "docs/guide.md".
func (g *SQLite) findDocument(path string) (DocNode, error) {
	path = slashPath(path)
	// Exact physical path or node ID first (index-driven, RFC 003 section
	// 3.4: doc_nodes_rpath and the id primary key).
	n, err := scanDocNode(g.reader.QueryRowContext(readCtx(),
		`SELECT `+docNodeCols+` FROM doc_nodes WHERE type = 'document' AND (rpath = ? OR id = ?) LIMIT 1`, path, path))
	if err == nil {
		return n, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DocNode{}, fmt.Errorf("looking up document %q: %w", path, err)
	}
	// Suffix match (kuzu's "rpath ENDS WITH"): a non-empty bare path resolves
	// a document stored under a longer physical path. substr(rpath, -length(?))
	// is the case-sensitive suffix check used for symbol resolution; the
	// length guard keeps an empty path from matching every document.
	n, err = scanDocNode(g.reader.QueryRowContext(readCtx(),
		`SELECT `+docNodeCols+` FROM doc_nodes
WHERE type = 'document' AND length(?) > 0 AND (substr(rpath, -length(?)) = ? OR substr(id, -length(?)) = ?)
ORDER BY rpath, id LIMIT 1`, path, path, path, path, path))
	if errors.Is(err, sql.ErrNoRows) {
		return DocNode{}, fmt.Errorf("no document found with path %q", path)
	}
	if err != nil {
		return DocNode{}, fmt.Errorf("looking up document %q: %w", path, err)
	}
	return n, nil
}

// folderName returns the name of a node's parent folder, "" when absent.
func (g *SQLite) folderName(parent string) (string, error) {
	if parent == "" {
		return "", nil
	}
	var name sql.NullString
	err := g.reader.QueryRowContext(readCtx(), `SELECT name FROM doc_nodes WHERE id = ?`, parent).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("looking up folder %q: %w", parent, err)
	}
	return name.String, nil
}

// DocumentContext returns the document's summary, folder and heading
// breadcrumb (the port of kuzu.DocumentContext): folder > document >
// headings, headings ordered by level.
func (g *SQLite) DocumentContext(path string) (*DocumentContext, error) {
	doc, err := g.findDocument(path)
	if err != nil {
		return nil, fmt.Errorf("document context for %q: %w", path, err)
	}
	folder, err := g.folderName(doc.Parent)
	if err != nil {
		return nil, fmt.Errorf("document context for %q: %w", path, err)
	}
	// All headings under the document, at any nesting depth: in the
	// single-parent schema a nested heading's parent is another heading,
	// not the document (Kùzu attached every heading via HAS_HEADING).
	rows, err := g.reader.QueryContext(readCtx(), tocTreeSQL, doc.ID, maxDocTreeDepth)
	if err != nil {
		return nil, fmt.Errorf("document context for %q: listing headings: %w", path, err)
	}
	defer rows.Close()
	type heading struct {
		name  string
		level int
	}
	var hs []heading
	for rows.Next() {
		var id, parent string
		var name sql.NullString
		var level sql.NullInt64
		if err := rows.Scan(&id, &parent, &name, &level); err != nil {
			return nil, fmt.Errorf("document context for %q: listing headings: %w", path, err)
		}
		if name.String != "" {
			hs = append(hs, heading{name: name.String, level: int(level.Int64)})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("document context for %q: listing headings: %w", path, err)
	}
	sort.SliceStable(hs, func(i, j int) bool {
		if hs[i].level != hs[j].level {
			return hs[i].level < hs[j].level
		}
		return hs[i].name < hs[j].name
	})
	var headings []string
	for _, h := range hs {
		headings = append(headings, h.name)
	}

	name := doc.Name
	if name == "" {
		name = doc.RPath
	}
	var parts []string
	if folder != "" {
		parts = append(parts, folder)
	}
	if name != "" {
		parts = append(parts, name)
	}
	parts = append(parts, headings...)
	return &DocumentContext{
		RPath:      doc.RPath,
		Name:       doc.Name,
		Summary:    doc.Summary,
		FolderName: folder,
		Breadcrumb: strings.Join(parts, " > "),
		Headings:   headings,
	}, nil
}

// tocTreeSQL collects every heading under a document, at any nesting depth,
// with one recursive parent walk. UNION deduplicates rows so a bad parent
// cycle terminates instead of looping, and the depth cap (second argument,
// maxDocTreeDepth) bounds the walk as RFC 003 section 3.7 requires. The seed
// sits at depth 0, like callersSQL, so the cap keeps depths 0..maxDocTreeDepth.
const tocTreeSQL = `
WITH RECURSIVE tree(id, parent, name, level, depth) AS (
  SELECT id, parent, name, level, 0 FROM doc_nodes WHERE parent = ? AND type = 'heading'
  UNION
  SELECT n.id, n.parent, n.name, n.level, t.depth + 1
  FROM doc_nodes n JOIN tree t ON n.parent = t.id
  WHERE n.type = 'heading' AND t.depth < ?
)
SELECT id, parent, name, level FROM tree`

// DocumentTOC returns the document's heading tree (the port of
// kuzu.DocumentTOC): roots are the headings directly under the document,
// children follow the parent column, ordered by level then title.
func (g *SQLite) DocumentTOC(path string) (*DocumentTOC, error) {
	doc, err := g.findDocument(path)
	if err != nil {
		return nil, fmt.Errorf("document TOC for %q: %w", path, err)
	}
	folder, err := g.folderName(doc.Parent)
	if err != nil {
		return nil, fmt.Errorf("document TOC for %q: %w", path, err)
	}
	rows, err := g.reader.QueryContext(readCtx(), tocTreeSQL, doc.ID, maxDocTreeDepth)
	if err != nil {
		return nil, fmt.Errorf("document TOC for %q: listing headings: %w", path, err)
	}
	defer rows.Close()
	// Build with pointers first: map iteration order is random, so a child
	// may be attached to its parent before its own children arrive. Values
	// are only materialised bottom-up, once every subtree is complete.
	type tocNode struct {
		id   string
		h    Heading
		kids []*tocNode
	}
	nodes := make(map[string]*tocNode)
	parentOf := make(map[string]string)
	for rows.Next() {
		var id, parent string
		var name sql.NullString
		var level sql.NullInt64
		if err := rows.Scan(&id, &parent, &name, &level); err != nil {
			return nil, fmt.Errorf("document TOC for %q: listing headings: %w", path, err)
		}
		nodes[id] = &tocNode{id: id, h: Heading{ID: id, Title: name.String, Level: int(level.Int64)}}
		parentOf[id] = parent
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("document TOC for %q: listing headings: %w", path, err)
	}

	// A heading whose parent is not in the set hangs directly under the
	// document (its parent is the document itself).
	var roots []*tocNode
	for id, n := range nodes {
		if p, ok := nodes[parentOf[id]]; ok {
			p.kids = append(p.kids, n)
		} else {
			roots = append(roots, n)
		}
	}
	var build func(ns []*tocNode) []Heading
	build = func(ns []*tocNode) []Heading {
		sort.SliceStable(ns, func(i, j int) bool {
			if ns[i].h.Level != ns[j].h.Level {
				return ns[i].h.Level < ns[j].h.Level
			}
			if ns[i].h.Title != ns[j].h.Title {
				return ns[i].h.Title < ns[j].h.Title
			}
			return ns[i].id < ns[j].id
		})
		var out []Heading
		for _, n := range ns {
			h := n.h
			h.Children = build(n.kids)
			out = append(out, h)
		}
		return out
	}
	out := &DocumentTOC{
		RPath:      doc.RPath,
		Title:      doc.Name,
		Summary:    doc.Summary,
		Folder:     folder,
		TotalNodes: len(nodes),
		Headings:   build(roots),
	}
	if out.Title == "" {
		out.Title = doc.RPath
	}
	return out, nil
}

// listDocsSQL is the ListDocuments query. The heading count must equal what
// Kùzu returned: Kùzu attached every heading via (d)-[:HAS_HEADING]->(h), so
// count(h) counted nested headings too, not just the direct ones. The
// recursive subtree CTE walks every heading under each document (matching
// tocTreeSQL, including its depth cap), so ListDocuments and DocumentTOC stay
// consistent.
const listDocsSQL = `
WITH RECURSIVE subtree(root, id, depth) AS (
  SELECT d.id, h.id, 0
  FROM doc_nodes d JOIN doc_nodes h ON h.parent = d.id AND h.type = 'heading'
  WHERE d.type = 'document'
  UNION
  SELECT s.root, n.id, s.depth + 1
  FROM doc_nodes n JOIN subtree s ON n.parent = s.id
  WHERE n.type = 'heading' AND s.depth < ?
)
SELECT d.rpath, d.name, d.summary, coalesce(f.name, ''), count(s.id)
FROM doc_nodes d
LEFT JOIN doc_nodes f ON f.id = d.parent
LEFT JOIN subtree s ON s.root = d.id
WHERE d.type = 'document'
GROUP BY d.id
ORDER BY d.rpath, d.id`

// ListDocuments lists every indexed document with its folder, summary and
// heading count, ordered by physical path (the port of kuzu.ListDocuments).
func (g *SQLite) ListDocuments() ([]DocumentInfo, error) {
	rows, err := g.reader.QueryContext(readCtx(), listDocsSQL, maxDocTreeDepth)
	if err != nil {
		return nil, fmt.Errorf("listing documents: %w", err)
	}
	defer rows.Close()
	var out []DocumentInfo
	for rows.Next() {
		var d DocumentInfo
		var rpath, name, summary sql.NullString
		if err := rows.Scan(&rpath, &name, &summary, &d.Folder, &d.HeadingCount); err != nil {
			return nil, fmt.Errorf("listing documents: %w", err)
		}
		d.RPath, d.Summary = rpath.String, summary.String
		d.Title = name.String
		if d.Title == "" {
			d.Title = d.RPath
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing documents: %w", err)
	}
	return out, nil
}

// FullDocument returns the complete text of a document (the port of
// kuzu.FullDocument): the physical file when it is still on disk, otherwise
// the stored chunks in start_char order joined by blank lines.
func (g *SQLite) FullDocument(path string) (string, error) {
	path = slashPath(path)
	doc, docErr := g.findDocument(path)
	if docErr == nil && doc.RPath != "" {
		if data, err := os.ReadFile(doc.RPath); err == nil && len(data) > 0 {
			return string(data), nil
		}
	}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data), nil
	}
	if docErr != nil {
		return "", fmt.Errorf("full document %q: %w", path, docErr)
	}
	rows, err := g.reader.QueryContext(readCtx(), `SELECT text FROM doc_nodes
WHERE parent = ? AND type = 'chunk' ORDER BY start_char, id`, doc.ID)
	if err != nil {
		return "", fmt.Errorf("full document %q: listing chunks: %w", path, err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var text sql.NullString
		if err := rows.Scan(&text); err != nil {
			return "", fmt.Errorf("full document %q: listing chunks: %w", path, err)
		}
		if text.String != "" {
			parts = append(parts, text.String)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("full document %q: listing chunks: %w", path, err)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("full document %q: no content found", path)
	}
	return strings.Join(parts, "\n\n"), nil
}

// DocumentSymbols returns the first-party symbols explained by the
// document's chunks, via doc_explains (the port of kuzu.DocumentSymbols).
func (g *SQLite) DocumentSymbols(path string) ([]Symbol, error) {
	doc, err := g.findDocument(path)
	if err != nil {
		return nil, fmt.Errorf("document symbols for %q: %w", path, err)
	}
	rows, err := g.reader.QueryContext(readCtx(), `SELECT DISTINCT s.fqn, s.name, s.kind, s.file, s.start_line, s.end_line, s.start_col, s.byte_offset, s.is_test, s.weight, s.doc
FROM doc_explains x
JOIN symbols s ON s.fqn = x.fqn
WHERE x.chunk_id IN (SELECT id FROM doc_nodes WHERE parent = ? AND type = 'chunk')
ORDER BY s.fqn`, doc.ID)
	if err != nil {
		return nil, fmt.Errorf("document symbols for %q: %w", path, err)
	}
	defer rows.Close()
	var out []Symbol
	for rows.Next() {
		s, err := scanSymbol(rows)
		if err != nil {
			return nil, fmt.Errorf("document symbols for %q: %w", path, err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("document symbols for %q: %w", path, err)
	}
	return out, nil
}
