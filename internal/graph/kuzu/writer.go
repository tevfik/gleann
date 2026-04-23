//go:build treesitter

package kuzu

import (
	"encoding/csv"
	"fmt"
	"os"

	gokuzu "github.com/kuzudb/go-kuzu"
)

// FileNode represents a code file node for CSV export.
type FileNode struct {
	Path string
	Lang string
}

// SymbolNode represents any code symbol (function, type, struct, etc.).
type SymbolNode struct {
	FQN  string // Fully Qualified Name, e.g. "github.com/foo/bar.MyFunc"
	Kind string // "function" | "method" | "type" | "struct" | "interface" | "const" | "var"
	File string // source file path
	Line int64  // line number in source file
	Name string // short name, e.g. "MyFunc"
	Doc  string // documentation comment (optional)
}

// EdgeDeclares represents a DECLARES relationship matching KuzuDB schema (FROM CodeFile TO Symbol).
type EdgeDeclares struct {
	FilePath  string
	SymbolFQN string
}

// EdgeCalls represents a CALLS relationship (FROM Symbol TO Symbol).
type EdgeCalls struct {
	CallerFQN  string
	CalleeFQN  string
	Confidence string // "extracted" (AST-resolved), "inferred" (heuristic), "ambiguous"
}

// UpsertFile inserts or ignores a CodeFile node.
func (g *DB) UpsertFile(path, lang string) error {
	cypher := fmt.Sprintf(
		`MERGE (f:CodeFile {path: %q}) ON CREATE SET f.lang = %q`,
		path, lang,
	)
	return g.exec(cypher)
}

// UpsertSymbol inserts or ignores a Symbol node.
func (g *DB) UpsertSymbol(s SymbolNode) error {
	cypher := fmt.Sprintf(
		`MERGE (sym:Symbol {fqn: %q})
         ON CREATE SET sym.kind=%q, sym.file=%q, sym.line=%d, sym.name=%q, sym.doc=%q`,
		s.FQN, s.Kind, s.File, s.Line, s.Name, s.Doc,
	)
	return g.exec(cypher)
}

// AddDeclares records that a CodeFile declares a Symbol.
func (g *DB) AddDeclares(filePath, symbolFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q}), (s:Symbol {fqn: %q})
         MERGE (f)-[:DECLARES]->(s)`,
		filePath, symbolFQN,
	)
	return g.exec(cypher)
}

// AddCalls records that caller (by FQN) calls callee (by FQN).
func (g *DB) AddCalls(callerFQN, calleeFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q})
         MERGE (a)-[:CALLS]->(b)`,
		callerFQN, calleeFQN,
	)
	return g.exec(cypher)
}

// AddImplements records that implFQN implements ifaceFQN.
func (g *DB) AddImplements(implFQN, ifaceFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q})
         MERGE (a)-[:IMPLEMENTS]->(b)`,
		implFQN, ifaceFQN,
	)
	return g.exec(cypher)
}

// AddReferences records that referer (by FQN) references referee (by FQN).
func (g *DB) AddReferences(refererFQN, refereeFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q})
         MERGE (a)-[:REFERENCES]->(b)`,
		refererFQN, refereeFQN,
	)
	return g.exec(cypher)
}

// ─── Query-builder helpers (return Cypher strings for batch use) ──────────────

// UpsertFileQuery returns a Cypher string that upserts a CodeFile node.
func UpsertFileQuery(path, lang string) string {
	return fmt.Sprintf(`MERGE (f:CodeFile {path: %q}) ON CREATE SET f.lang = %q`, path, lang)
}

// DeleteFileSymbolsQuery returns a Cypher string that deletes all existing
// symbols (and their edges such as CALLS) that belong to the given file.
// Use DeleteFileQueries for a full re-index (also removes the CodeFile node).
func DeleteFileSymbolsQuery(path string) string {
	return fmt.Sprintf(`MATCH (s:Symbol {file: %q}) DETACH DELETE s`, path)
}

// DeleteFileQueries returns Cypher queries that delete both the symbols
// and the CodeFile node for the given file path. This ensures clean
// re-indexing without duplicate primary key violations.
func DeleteFileQueries(path string) []string {
	return []string{
		fmt.Sprintf(`MATCH (s:Symbol {file: %q}) DETACH DELETE s`, path),
		fmt.Sprintf(`MATCH (f:CodeFile {path: %q}) DETACH DELETE f`, path),
	}
}

// DeleteAllCodeData returns queries that wipe ALL CodeFile nodes, Symbol nodes,
// and their edges (DECLARES, CALLS). Use this before a full IndexDir re-index
// to avoid stale callee-stub symbols causing duplicate PK violations.
func DeleteAllCodeData() []string {
	return []string{
		`MATCH (s:Symbol) DETACH DELETE s`,
		`MATCH (f:CodeFile) DETACH DELETE f`,
	}
}

// UpsertSymbolQuery returns a Cypher string that upserts a Symbol node.
func UpsertSymbolQuery(s SymbolNode) string {
	return fmt.Sprintf(
		`MERGE (sym:Symbol {fqn: %q}) ON CREATE SET sym.kind=%q, sym.file=%q, sym.line=%d, sym.name=%q, sym.doc=%q`,
		s.FQN, s.Kind, s.File, s.Line, s.Name, s.Doc,
	)
}

// AddDeclaresQuery returns a Cypher string that adds a DECLARES edge.
func AddDeclaresQuery(filePath, symbolFQN string) string {
	return fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q}), (s:Symbol {fqn: %q}) MERGE (f)-[:DECLARES]->(s)`,
		filePath, symbolFQN,
	)
}

// AddCallsQuery returns a Cypher string that adds a CALLS edge.
func AddCallsQuery(callerFQN, calleeFQN string) string {
	return fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q}) MERGE (a)-[:CALLS]->(b)`,
		callerFQN, calleeFQN,
	)
}

// ─── Connection-parameterised variants (for concurrent workers) ──────────────

// UpsertFileOn upserts a CodeFile node on an explicit connection.
func UpsertFileOn(conn *gokuzu.Connection, path, lang string) error {
	cypher := fmt.Sprintf(
		`MERGE (f:CodeFile {path: %q}) ON CREATE SET f.lang = %q`,
		path, lang,
	)
	return ExecOn(conn, cypher)
}

// UpsertSymbolOn upserts a Symbol node on an explicit connection.
func UpsertSymbolOn(conn *gokuzu.Connection, s SymbolNode) error {
	cypher := fmt.Sprintf(
		`MERGE (sym:Symbol {fqn: %q})
         ON CREATE SET sym.kind=%q, sym.file=%q, sym.line=%d, sym.name=%q, sym.doc=%q`,
		s.FQN, s.Kind, s.File, s.Line, s.Name, s.Doc,
	)
	return ExecOn(conn, cypher)
}

// AddDeclaresOn records a DECLARES edge on an explicit connection.
func AddDeclaresOn(conn *gokuzu.Connection, filePath, symbolFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q}), (s:Symbol {fqn: %q})
         MERGE (f)-[:DECLARES]->(s)`,
		filePath, symbolFQN,
	)
	return ExecOn(conn, cypher)
}

// AddCallsOn records a CALLS edge on an explicit connection.
func AddCallsOn(conn *gokuzu.Connection, callerFQN, calleeFQN string) error {
	cypher := fmt.Sprintf(
		`MATCH (a:Symbol {fqn: %q}), (b:Symbol {fqn: %q})
         MERGE (a)-[:CALLS]->(b)`,
		callerFQN, calleeFQN,
	)
	return ExecOn(conn, cypher)
}

// ExecTxOn runs all queries in a single manual transaction on the given connection.
// On any error, ROLLBACK is issued and the error is returned.
func ExecTxOn(conn *gokuzu.Connection, queries []string) error {
	if len(queries) == 0 {
		return nil
	}
	if err := ExecOn(conn, "BEGIN TRANSACTION"); err != nil {
		return fmt.Errorf("BEGIN TRANSACTION: %w", err)
	}
	for _, q := range queries {
		if err := ExecOn(conn, q); err != nil {
			_ = ExecOn(conn, "ROLLBACK")
			preview := q
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			return fmt.Errorf("tx exec %q: %w", preview, err)
		}
	}
	if err := ExecOn(conn, "COMMIT"); err != nil {
		_ = ExecOn(conn, "ROLLBACK")
		return fmt.Errorf("COMMIT: %w", err)
	}
	return nil
}

// ─── CSV Bulk Load Helpers ───────────────────────────────────────────────────

// ExecCopyCSV executes a COPY statement to load bulk data from a CSV file.
// Uses PARALLEL=FALSE to support quoted newlines in content fields.
func ExecCopyCSV(conn *gokuzu.Connection, tableName, csvPath string) error {
	cypher := fmt.Sprintf(`COPY %s FROM %q (HEADER=true, PARALLEL=false)`, tableName, csvPath)
	return ExecOn(conn, cypher)
}

// WriteFileNodesCSV writes a list of FileNode to a CSV file.
func WriteFileNodesCSV(path string, files []FileNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"path", "lang"}); err != nil {
		return err
	}
	for _, fn := range files {
		if err := w.Write([]string{fn.Path, fn.Lang}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteSymbolNodesCSV writes a list of SymbolNode to a CSV file.
func WriteSymbolNodesCSV(path string, symbols []SymbolNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"fqn", "kind", "file", "line", "name", "doc"}); err != nil {
		return err
	}
	for _, sym := range symbols {
		if err := w.Write([]string{
			sym.FQN, sym.Kind, sym.File, fmt.Sprintf("%d", sym.Line), sym.Name, sym.Doc,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteDeclaresCSV writes a list of EdgeDeclares to a CSV file.
func WriteDeclaresCSV(path string, edges []EdgeDeclares) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"from", "to"}); err != nil {
		return err
	}
	for _, e := range edges {
		if err := w.Write([]string{e.FilePath, e.SymbolFQN}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteCallsCSV writes a list of EdgeCalls to a CSV file.
func WriteCallsCSV(path string, edges []EdgeCalls) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"from", "to"}); err != nil {
		return err
	}
	for _, e := range edges {
		if err := w.Write([]string{e.CallerFQN, e.CalleeFQN}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// ─── Hierarchical Graph Types ────────────────────────────────────────────────────

// FolderNode represents a structural folder.
type FolderNode struct {
	VPath string
	Name  string
}

// DocumentNode represents a document in the graph.
type DocumentNode struct {
	VPath   string
	RPath   string
	Name    string
	Hash    string
	Summary string
}

// HeadingNode represents a markdown heading section.
type HeadingNode struct {
	ID    string
	Name  string
	Level int64
}

// ChunkNode represents a chunk that links the vector index to the graph.
type ChunkNode struct {
	ID        string
	Text      string
	StartChar int64
	EndChar   int64
}

// EdgeContainsDoc represents CONTAINS_DOC (Folder→Document).
type EdgeContainsDoc struct {
	FolderVPath string
	DocVPath    string
}

// EdgeHasHeading represents HAS_HEADING (Document→Heading).
type EdgeHasHeading struct {
	DocVPath  string
	HeadingID string
}

// EdgeChildHeading represents CHILD_HEADING (Heading→Heading).
type EdgeChildHeading struct {
	ParentID string
	ChildID  string
}

// EdgeHasChunkHeading represents HAS_CHUNK_HEADING (Heading→Chunk).
type EdgeHasChunkHeading struct {
	HeadingID string
	ChunkID   string
}

// EdgeHasChunkDoc represents HAS_CHUNK_DOC (Document→Chunk).
type EdgeHasChunkDoc struct {
	DocVPath string
	ChunkID  string
}

// EdgeExplains represents EXPLAINS (Chunk→Symbol).
type EdgeExplains struct {
	ChunkID   string
	SymbolFQN string
}

// WriteFolderNodesCSV writes Folder nodes to a CSV file.
func WriteFolderNodesCSV(path string, folders []FolderNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"vpath", "name"}); err != nil {
		return err
	}
	for _, f := range folders {
		if err := w.Write([]string{f.VPath, f.Name}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteDocumentNodesCSV writes Document nodes to a CSV file.
func WriteDocumentNodesCSV(path string, docs []DocumentNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"vpath", "rpath", "name", "hash", "summary"}); err != nil {
		return err
	}
	for _, d := range docs {
		if err := w.Write([]string{d.VPath, d.RPath, d.Name, d.Hash, d.Summary}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteHeadingNodesCSV writes Heading nodes to a CSV file.
func WriteHeadingNodesCSV(path string, headings []HeadingNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"id", "name", "level"}); err != nil {
		return err
	}
	for _, h := range headings {
		if err := w.Write([]string{
			h.ID, h.Name, fmt.Sprintf("%d", h.Level),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteChunkNodesCSV writes Chunk nodes to a CSV file.
func WriteChunkNodesCSV(path string, chunks []ChunkNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"id", "text", "start_char", "end_char"}); err != nil {
		return err
	}
	for _, c := range chunks {
		if err := w.Write([]string{
			c.ID, c.Text, fmt.Sprintf("%d", c.StartChar), fmt.Sprintf("%d", c.EndChar),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteContainsDocCSV writes CONTAINS_DOC edges to a CSV file.
func WriteContainsDocCSV(path string, edges []EdgeContainsDoc) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"from", "to"}); err != nil {
		return err
	}
	for _, e := range edges {
		if err := w.Write([]string{e.FolderVPath, e.DocVPath}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteHasHeadingCSV writes HAS_HEADING edges to a CSV file.
func WriteHasHeadingCSV(path string, edges []EdgeHasHeading) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"from", "to"}); err != nil {
		return err
	}
	for _, e := range edges {
		if err := w.Write([]string{e.DocVPath, e.HeadingID}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteChildHeadingCSV writes CHILD_HEADING edges to a CSV file.
func WriteChildHeadingCSV(path string, edges []EdgeChildHeading) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"from", "to"}); err != nil {
		return err
	}
	for _, e := range edges {
		if err := w.Write([]string{e.ParentID, e.ChildID}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// DeleteDocumentSectionsQuery returns Cypher to delete all Headings belonging to a document.
func DeleteDocumentSectionsQuery(docVPath string) string {
	return fmt.Sprintf(`MATCH (d:Document {vpath: %q})-[:HAS_HEADING]->(h:Heading) DETACH DELETE h`, docVPath)
}

// DeleteDocumentChunksQuery returns Cypher to delete all Chunks referencing headings or doc.
func DeleteDocumentChunksQuery(docVPath string) string {
	return fmt.Sprintf(
		`MATCH (d:Document {vpath: %q})-[:HAS_HEADING]->(:Heading)-[:HAS_CHUNK_HEADING]->(c:Chunk) DETACH DELETE c`,
		docVPath,
	)
}

// DeleteDocumentQuery returns Cypher to delete a Document node.
func DeleteDocumentQuery(docVPath string) string {
	return fmt.Sprintf(`MATCH (d:Document {vpath: %q}) DETACH DELETE d`, docVPath)
}

// RemoveFileSymbols deletes all symbols declared by a file and their edges,
// then deletes the file node itself. Used for incremental graph updates:
// remove old data for changed files before re-indexing.
func (g *DB) RemoveFileSymbols(filePath string) error {
	// 1. Delete edges FROM symbols declared by this file.
	cypher1 := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q})-[:DECLARES]->(s:Symbol)-[r]->() DELETE r`,
		filePath,
	)
	if err := g.exec(cypher1); err != nil {
		// Ignore if no matches.
		_ = err
	}

	// 2. Delete edges TO symbols declared by this file.
	cypher2 := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q})-[:DECLARES]->(s:Symbol)<-[r]-() DELETE r`,
		filePath,
	)
	if err := g.exec(cypher2); err != nil {
		_ = err
	}

	// 3. Delete DECLARES edges from this file.
	cypher3 := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q})-[r:DECLARES]->() DELETE r`,
		filePath,
	)
	if err := g.exec(cypher3); err != nil {
		_ = err
	}

	// 4. Delete the symbols themselves.
	cypher4 := fmt.Sprintf(
		`MATCH (s:Symbol {file: %q}) DELETE s`,
		filePath,
	)
	if err := g.exec(cypher4); err != nil {
		_ = err
	}

	// 5. Delete the file node.
	cypher5 := fmt.Sprintf(
		`MATCH (f:CodeFile {path: %q}) DELETE f`,
		filePath,
	)
	return g.exec(cypher5)
}
