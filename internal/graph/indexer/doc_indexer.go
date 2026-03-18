//go:build treesitter

// Package indexer provides graph indexers for both code (AST) and documents.
//
// DocIndexer ingests structured plugin responses (Document + Section nodes,
// HAS_SECTION + HAS_SUBSECTION edges) into KuzuDB, following the same CSV
// COPY FROM bulk-load pattern used by the AST code indexer.
//
// The flow mirrors the code indexer:
//
//	Plugin /convert → {nodes, edges}
//	              ↓
//	DocIndexer.IndexDocument()
//	   → CSV bulk load → KuzuDB (Document, Section, DocChunk)
//	   → MarkdownChunker → []chunking.MarkdownChunk (for HNSW embedding)
package indexer

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/modules/chunking"
	gleann "github.com/tevfik/gleann/pkg/gleann"
)

// DocIndexer indexes structured documents into KuzuDB.
type DocIndexer struct {
	db      *kuzu.DB
	chunker *chunking.MarkdownChunker
	writeMu sync.Mutex // serialize KuzuDB writes (connection is not thread-safe)
}

// NewDocIndexer creates a new document indexer.
func NewDocIndexer(db *kuzu.DB, chunkSize, chunkOverlap int) *DocIndexer {
	return &DocIndexer{
		db:      db,
		chunker: chunking.NewMarkdownChunker(chunkSize, chunkOverlap),
	}
}

// DocIndexResult holds the output of document indexing — chunks ready for HNSW embedding.
type DocIndexResult struct {
	Chunks []chunking.MarkdownChunk
	Items  []gleann.Item // ready for builder.Build()
}

// IndexDocument ingests a plugin result into KuzuDB and returns chunks for vector indexing.
//
// Steps:
//  1. Parse PluginResult into typed nodes/edges
//  2. Delete old data for this document (idempotent re-index)
//  3. CSV bulk load Document, Section nodes
//  4. Create edges via Cypher transactions
//  5. Chunk section content via MarkdownChunker → return Items for HNSW
func (di *DocIndexer) IndexDocument(result *gleann.PluginResult, sourcePath string) (*DocIndexResult, error) {
	di.writeMu.Lock()
	defer di.writeMu.Unlock()

	start := time.Now()

	// 1. Extract typed data from plugin result
	folders, docs, headings, containsEdges, hasHeadingEdges, childHeadingEdges := di.extractFromPlugin(result, sourcePath)

	if len(docs) == 0 {
		return nil, fmt.Errorf("no Document node in plugin result for %s", sourcePath)
	}

	// 2. Delete old document data (idempotent)
	deleteQueries := []string{
		kuzu.DeleteDocumentChunksQuery(sourcePath),
		kuzu.DeleteDocumentSectionsQuery(sourcePath),
		kuzu.DeleteDocumentQuery(sourcePath),
	}
	if err := kuzu.ExecTxOn(di.db.Conn(), deleteQueries); err != nil {
		// Tables may not have data yet — log and continue
		log.Printf("[WARN] doc delete old data for %s: %v", sourcePath, err)
	}

	// 3. CSV bulk load nodes
	doCopy := func(tableName string, writeFunc func(p string) error) error {
		tmp, err := os.CreateTemp("", "kuzu_doc_"+tableName+"_*.csv")
		if err != nil {
			return err
		}
		csvPath := tmp.Name()
		tmp.Close()
		defer os.Remove(csvPath)

		if err := writeFunc(csvPath); err != nil {
			return fmt.Errorf("write %s: %w", tableName, err)
		}
		if err := kuzu.ExecCopyCSV(di.db.Conn(), tableName, csvPath); err != nil {
			return fmt.Errorf("copy %s: %w", tableName, err)
		}
		return nil
	}

	if len(folders) > 0 {
		if err := doCopy("Folder", func(p string) error { return kuzu.WriteFolderNodesCSV(p, folders) }); err != nil {
			return nil, err
		}
	}
	if err := doCopy("Document", func(p string) error { return kuzu.WriteDocumentNodesCSV(p, docs) }); err != nil {
		return nil, err
	}
	if len(headings) > 0 {
		if err := doCopy("Heading", func(p string) error { return kuzu.WriteHeadingNodesCSV(p, headings) }); err != nil {
			return nil, err
		}
	}

	// 4. Create edges via transaction
	var edgeQueries []string
	for _, e := range containsEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (f:Folder {vpath: %q}), (d:Document {vpath: %q}) MERGE (f)-[:CONTAINS_DOC]->(d)`,
			e.FolderVPath, e.DocVPath,
		))
	}
	for _, e := range hasHeadingEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (d:Document {vpath: %q}), (h:Heading {id: %q}) MERGE (d)-[:HAS_HEADING]->(h)`,
			e.DocVPath, e.HeadingID,
		))
	}
	for _, e := range childHeadingEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (p:Heading {id: %q}), (c:Heading {id: %q}) MERGE (p)-[:CHILD_HEADING]->(c)`,
			e.ParentID, e.ChildID,
		))
	}
	if len(edgeQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), edgeQueries); err != nil {
			return nil, fmt.Errorf("create doc edges: %w", err)
		}
	}

	// 5. Chunk section content for vector indexing
	// Note: buildStructuredDocument still needs the original Section/Heading data
	doc := di.buildStructuredDocument(docs[0], headings, result)
	mdChunks := di.chunker.ChunkDocument(doc)

	// Add source metadata to all chunks
	for i := range mdChunks {
		mdChunks[i].Metadata["source"] = sourcePath
	}

	// Convert to gleann.Item for the builder pipeline
	items := make([]gleann.Item, len(mdChunks))
	for i, ch := range mdChunks {
		items[i] = gleann.Item{
			Text:     ch.Text,
			Metadata: ch.Metadata,
		}
	}

	elapsed := time.Since(start)
	log.Printf("[INFO] DocIndexer: indexed %s (%d headings, %d chunks) in %v",
		sourcePath, len(headings), len(mdChunks), elapsed)

	return &DocIndexResult{
		Chunks: mdChunks,
		Items:  items,
	}, nil
}

// WriteGraph writes only the graph nodes/edges to KuzuDB without chunking.
// Used by the build pipeline: readDocuments() handles chunking for HNSW,
// then buildGraphIndex() calls WriteGraph() for KuzuDB persistence.
func (di *DocIndexer) WriteGraph(result *gleann.PluginResult, sourcePath string) error {
	di.writeMu.Lock()
	defer di.writeMu.Unlock()

	start := time.Now()

	folders, docs, headings, containsEdges, hasHeadingEdges, childHeadingEdges := di.extractFromPlugin(result, sourcePath)
	if len(docs) == 0 {
		return fmt.Errorf("no Document node in plugin result for %s", sourcePath)
	}

	// Delete old document data (idempotent re-index).
	deleteQueries := []string{
		kuzu.DeleteDocumentChunksQuery(sourcePath),
		kuzu.DeleteDocumentSectionsQuery(sourcePath),
		kuzu.DeleteDocumentQuery(sourcePath),
	}
	if err := kuzu.ExecTxOn(di.db.Conn(), deleteQueries); err != nil {
		log.Printf("[WARN] doc delete old data for %s: %v", sourcePath, err)
	}

	// CSV bulk load nodes.
	doCopy := func(tableName string, writeFunc func(p string) error) error {
		tmp, err := os.CreateTemp("", "kuzu_doc_"+tableName+"_*.csv")
		if err != nil {
			return err
		}
		csvPath := tmp.Name()
		tmp.Close()
		defer os.Remove(csvPath)

		if err := writeFunc(csvPath); err != nil {
			return fmt.Errorf("write %s: %w", tableName, err)
		}
		if err := kuzu.ExecCopyCSV(di.db.Conn(), tableName, csvPath); err != nil {
			return fmt.Errorf("copy %s: %w", tableName, err)
		}
		return nil
	}

	if len(folders) > 0 {
		if err := doCopy("Folder", func(p string) error { return kuzu.WriteFolderNodesCSV(p, folders) }); err != nil {
			return err
		}
	}
	if err := doCopy("Document", func(p string) error { return kuzu.WriteDocumentNodesCSV(p, docs) }); err != nil {
		return err
	}
	if len(headings) > 0 {
		if err := doCopy("Heading", func(p string) error { return kuzu.WriteHeadingNodesCSV(p, headings) }); err != nil {
			return err
		}
	}

	// Create edges via transaction.
	var edgeQueries []string
	for _, e := range containsEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (f:Folder {vpath: %q}), (d:Document {vpath: %q}) MERGE (f)-[:CONTAINS_DOC]->(d)`,
			e.FolderVPath, e.DocVPath,
		))
	}
	for _, e := range hasHeadingEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (d:Document {vpath: %q}), (h:Heading {id: %q}) MERGE (d)-[:HAS_HEADING]->(h)`,
			e.DocVPath, e.HeadingID,
		))
	}
	for _, e := range childHeadingEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (p:Heading {id: %q}), (c:Heading {id: %q}) MERGE (p)-[:CHILD_HEADING]->(c)`,
			e.ParentID, e.ChildID,
		))
	}
	if len(edgeQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), edgeQueries); err != nil {
			return fmt.Errorf("create doc edges: %w", err)
		}
	}

	elapsed := time.Since(start)
	log.Printf("[INFO] DocIndexer.WriteGraph: %s (%d headings) in %v",
		sourcePath, len(headings), elapsed)
	return nil
}

func (di *DocIndexer) WriteGraphBatch(docs []*DocGraphInput) error {
	if len(docs) == 0 {
		return nil
	}

	di.writeMu.Lock()
	defer di.writeMu.Unlock()

	start := time.Now()

	// Phase 1: Collect all typed data and delete queries.
	var allFolders []kuzu.FolderNode
	var allDocs []kuzu.DocumentNode
	var allHeadings []kuzu.HeadingNode
	var deleteQueries []string
	var edgeQueries []string

	for _, d := range docs {
		folders, docNodes, headings, containsEdges, hasHeadingEdges, childHeadingEdges := di.extractFromPlugin(d.Result, d.SourcePath)
		if len(docNodes) == 0 {
			log.Printf("[WARN] DocIndexer.WriteGraphBatch: no Document node for %s, skipping", d.SourcePath)
			continue
		}

		deleteQueries = append(deleteQueries,
			kuzu.DeleteDocumentChunksQuery(d.SourcePath),
			kuzu.DeleteDocumentSectionsQuery(d.SourcePath),
			kuzu.DeleteDocumentQuery(d.SourcePath),
		)

		allFolders = append(allFolders, folders...)
		allDocs = append(allDocs, docNodes...)
		allHeadings = append(allHeadings, headings...)

		for _, e := range containsEdges {
			edgeQueries = append(edgeQueries, fmt.Sprintf(
				`MATCH (f:Folder {vpath: %q}), (d:Document {vpath: %q}) MERGE (f)-[:CONTAINS_DOC]->(d)`,
				e.FolderVPath, e.DocVPath,
			))
		}
		for _, e := range hasHeadingEdges {
			edgeQueries = append(edgeQueries, fmt.Sprintf(
				`MATCH (d:Document {vpath: %q}), (h:Heading {id: %q}) MERGE (d)-[:HAS_HEADING]->(h)`,
				e.DocVPath, e.HeadingID,
			))
		}
		for _, e := range childHeadingEdges {
			edgeQueries = append(edgeQueries, fmt.Sprintf(
				`MATCH (p:Heading {id: %q}), (c:Heading {id: %q}) MERGE (p)-[:CHILD_HEADING]->(c)`,
				e.ParentID, e.ChildID,
			))
		}
	}

	// Phase 2: Delete old data.
	if len(deleteQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), deleteQueries); err != nil {
			log.Printf("[WARN] DocIndexer.WriteGraphBatch: delete old data: %v", err)
		}
	}

	// Phase 3: CSV bulk load.
	doCopy := func(tableName string, writeFunc func(p string) error) error {
		tmp, err := os.CreateTemp("", "kuzu_doc_batch_"+tableName+"_*.csv")
		if err != nil {
			return err
		}
		csvPath := tmp.Name()
		tmp.Close()
		defer os.Remove(csvPath)

		if err := writeFunc(csvPath); err != nil {
			return fmt.Errorf("write %s: %w", tableName, err)
		}
		if err := kuzu.ExecCopyCSV(di.db.Conn(), tableName, csvPath); err != nil {
			return fmt.Errorf("copy %s: %w", tableName, err)
		}
		return nil
	}

	if len(allFolders) > 0 {
		if err := doCopy("Folder", func(p string) error { return kuzu.WriteFolderNodesCSV(p, allFolders) }); err != nil {
			return fmt.Errorf("batch folder copy: %w", err)
		}
	}
	if len(allDocs) > 0 {
		if err := doCopy("Document", func(p string) error { return kuzu.WriteDocumentNodesCSV(p, allDocs) }); err != nil {
			return fmt.Errorf("batch Document copy: %w", err)
		}
	}
	if len(allHeadings) > 0 {
		if err := doCopy("Heading", func(p string) error { return kuzu.WriteHeadingNodesCSV(p, allHeadings) }); err != nil {
			return fmt.Errorf("batch Heading copy: %w", err)
		}
	}

	// Phase 4: Create edges.
	if len(edgeQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), edgeQueries); err != nil {
			return fmt.Errorf("batch doc edges: %w", err)
		}
	}

	elapsed := time.Since(start)
	log.Printf("[INFO] DocIndexer.WriteGraphBatch: %d documents, %d headings in %v",
		len(allDocs), len(allHeadings), elapsed)
	return nil
}

// DocGraphInput holds a single document's plugin result for batch writing.
type DocGraphInput struct {
	Result     *gleann.PluginResult
	SourcePath string
}

// extractFromPlugin converts the generic PluginResult into typed KuzuDB structs.
func (di *DocIndexer) extractFromPlugin(result *gleann.PluginResult, sourcePath string) (
	folders []kuzu.FolderNode,
	docs []kuzu.DocumentNode,
	headings []kuzu.HeadingNode,
	containsDocs []kuzu.EdgeContainsDoc,
	hasHeadings []kuzu.EdgeHasHeading,
	childHeadings []kuzu.EdgeChildHeading,
) {
	var folderPath string

	for _, node := range result.Nodes {
		switch node.Type {
		case "Document":
			vpath := getStr(node.Data, "vpath", sourcePath)
			rpath := getStr(node.Data, "rpath", sourcePath)

			// Try to automatically parse folder from vpath
			if strings.Contains(vpath, "/") {
				parts := strings.Split(vpath, "/")
				folderPath = strings.Join(parts[:len(parts)-1], "/")
				folderName := parts[len(parts)-2]

				folders = append(folders, kuzu.FolderNode{
					VPath: folderPath,
					Name:  folderName,
				})

				containsDocs = append(containsDocs, kuzu.EdgeContainsDoc{
					FolderVPath: folderPath,
					DocVPath:    vpath,
				})
			}

			docs = append(docs, kuzu.DocumentNode{
				VPath:   vpath,
				RPath:   rpath,
				Name:    getStr(node.Data, "title", ""),
				Hash:    getStr(node.Data, "hash", ""),
				Summary: getStr(node.Data, "summary", ""),
			})
		case "Section":
			headings = append(headings, kuzu.HeadingNode{
				ID:    getStr(node.Data, "id", ""),
				Name:  getStr(node.Data, "heading", ""),
				Level: getInt64(node.Data, "level"),
			})
		}
	}

	for _, edge := range result.Edges {
		switch edge.Type {
		case "HAS_SECTION":
			hasHeadings = append(hasHeadings, kuzu.EdgeHasHeading{
				DocVPath:  edge.From,
				HeadingID: edge.To,
			})
		case "HAS_SUBSECTION":
			childHeadings = append(childHeadings, kuzu.EdgeChildHeading{
				ParentID: edge.From,
				ChildID:  edge.To,
			})
		}
	}

	return
}

// buildStructuredDocument converts KuzuDB types back to StructuredDocument for chunking.
func (di *DocIndexer) buildStructuredDocument(doc kuzu.DocumentNode, headings []kuzu.HeadingNode, result *gleann.PluginResult) *chunking.StructuredDocument {
	mdSections := make([]chunking.MarkdownSection, len(headings))
	idToContent := make(map[string]string)

	for _, n := range result.Nodes {
		if n.Type == "Section" {
			idToContent[getStr(n.Data, "id", "")] = getStr(n.Data, "content", "")
		}
	}

	for i, s := range headings {
		parentID := ""
		if lastDot := strings.LastIndex(s.ID, "."); lastDot > 0 {
			parentID = s.ID[:lastDot]
		}

		mdSections[i] = chunking.MarkdownSection{
			ID:       s.ID,
			Heading:  s.Name,
			Level:    int(s.Level),
			Content:  idToContent[s.ID],
			Summary:  "",
			ParentID: parentID,
		}
	}

	return &chunking.StructuredDocument{
		Document: chunking.DocumentMeta{
			Title:   doc.Name,
			Summary: doc.Summary,
		},
		Sections: mdSections,
	}
}

// --- helpers ---

func getStr(m map[string]any, key, fallback string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

func getInt64(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case int64:
			return n
		}
	}
	return 0
}
