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
	CallerFQN string
	CalleeFQN string
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
// This ensures true "update" behavior (no ghost symbols left behind).
func DeleteFileSymbolsQuery(path string) string {
	return fmt.Sprintf(`MATCH (s:Symbol {file: %q}) DETACH DELETE s`, path)
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
func ExecCopyCSV(conn *gokuzu.Connection, tableName, csvPath string) error {
	cypher := fmt.Sprintf(`COPY %s FROM %q (HEADER=true)`, tableName, csvPath)
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

// ─── Document Graph Types ────────────────────────────────────────────────────

// DocumentNode represents a document in the graph.
type DocumentNode struct {
	Path      string
	Title     string
	Format    string
	Summary   string
	WordCount int64
	PageCount int64
}

// SectionNode represents a heading-delimited section in the graph.
type SectionNode struct {
	ID      string
	Heading string
	Level   int64
	Content string
	Summary string
	DocPath string
}

// DocChunkNode represents a chunk that links the vector index to the graph.
type DocChunkNode struct {
	ID         string
	Text       string
	ChunkIndex int64
	SectionID  string
	PassageID  int64
}

// EdgeHasSection represents a HAS_SECTION edge (Document→Section).
type EdgeHasSection struct {
	DocPath   string
	SectionID string
}

// EdgeHasSubsection represents a HAS_SUBSECTION edge (Section→Section).
type EdgeHasSubsection struct {
	ParentID string
	ChildID  string
}

// EdgeHasChunk represents a HAS_CHUNK edge (Section→DocChunk).
type EdgeHasChunk struct {
	SectionID string
	ChunkID   string
}

// WriteDocumentNodesCSV writes Document nodes to a CSV file.
func WriteDocumentNodesCSV(path string, docs []DocumentNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"path", "title", "format", "summary", "word_count", "page_count"}); err != nil {
		return err
	}
	for _, d := range docs {
		if err := w.Write([]string{
			d.Path, d.Title, d.Format, d.Summary,
			fmt.Sprintf("%d", d.WordCount),
			fmt.Sprintf("%d", d.PageCount),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteSectionNodesCSV writes Section nodes to a CSV file.
func WriteSectionNodesCSV(path string, sections []SectionNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"id", "heading", "level", "content", "summary", "doc_path"}); err != nil {
		return err
	}
	for _, s := range sections {
		if err := w.Write([]string{
			s.ID, s.Heading, fmt.Sprintf("%d", s.Level),
			s.Content, s.Summary, s.DocPath,
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteDocChunkNodesCSV writes DocChunk nodes to a CSV file.
func WriteDocChunkNodesCSV(path string, chunks []DocChunkNode) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{"id", "text", "chunk_index", "section_id", "passage_id"}); err != nil {
		return err
	}
	for _, c := range chunks {
		if err := w.Write([]string{
			c.ID, c.Text, fmt.Sprintf("%d", c.ChunkIndex),
			c.SectionID, fmt.Sprintf("%d", c.PassageID),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteHasSectionCSV writes HAS_SECTION edges to a CSV file.
func WriteHasSectionCSV(path string, edges []EdgeHasSection) error {
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
		if err := w.Write([]string{e.DocPath, e.SectionID}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// WriteHasSubsectionCSV writes HAS_SUBSECTION edges to a CSV file.
func WriteHasSubsectionCSV(path string, edges []EdgeHasSubsection) error {
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

// WriteHasChunkCSV writes HAS_CHUNK edges to a CSV file.
func WriteHasChunkCSV(path string, edges []EdgeHasChunk) error {
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
		if err := w.Write([]string{e.SectionID, e.ChunkID}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// DeleteDocumentSectionsQuery returns Cypher to delete all Sections belonging to a document.
func DeleteDocumentSectionsQuery(docPath string) string {
	return fmt.Sprintf(`MATCH (s:Section {doc_path: %q}) DETACH DELETE s`, docPath)
}

// DeleteDocumentChunksQuery returns Cypher to delete all DocChunks referencing sections of a doc.
func DeleteDocumentChunksQuery(docPath string) string {
	return fmt.Sprintf(
		`MATCH (s:Section {doc_path: %q})-[:HAS_CHUNK]->(c:DocChunk) DETACH DELETE c`,
		docPath,
	)
}

// DeleteDocumentQuery returns Cypher to delete a Document node.
func DeleteDocumentQuery(docPath string) string {
	return fmt.Sprintf(`MATCH (d:Document {path: %q}) DETACH DELETE d`, docPath)
}
