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
	docs, sections, hasSectionEdges, hasSubsectionEdges := di.extractFromPlugin(result, sourcePath)

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

	if err := doCopy("Document", func(p string) error { return kuzu.WriteDocumentNodesCSV(p, docs) }); err != nil {
		return nil, err
	}
	if len(sections) > 0 {
		if err := doCopy("Section", func(p string) error { return kuzu.WriteSectionNodesCSV(p, sections) }); err != nil {
			return nil, err
		}
	}

	// 4. Create edges via transaction
	var edgeQueries []string
	for _, e := range hasSectionEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (d:Document {path: %q}), (s:Section {id: %q}) MERGE (d)-[:HAS_SECTION]->(s)`,
			e.DocPath, e.SectionID,
		))
	}
	for _, e := range hasSubsectionEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (p:Section {id: %q}), (c:Section {id: %q}) MERGE (p)-[:HAS_SUBSECTION]->(c)`,
			e.ParentID, e.ChildID,
		))
	}
	if len(edgeQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), edgeQueries); err != nil {
			return nil, fmt.Errorf("create doc edges: %w", err)
		}
	}

	// 5. Chunk section content for vector indexing
	doc := di.buildStructuredDocument(docs[0], sections)
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
	log.Printf("[INFO] DocIndexer: indexed %s (%d sections, %d chunks) in %v",
		sourcePath, len(sections), len(mdChunks), elapsed)

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

	docs, sections, hasSectionEdges, hasSubsectionEdges := di.extractFromPlugin(result, sourcePath)
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

	if err := doCopy("Document", func(p string) error { return kuzu.WriteDocumentNodesCSV(p, docs) }); err != nil {
		return err
	}
	if len(sections) > 0 {
		if err := doCopy("Section", func(p string) error { return kuzu.WriteSectionNodesCSV(p, sections) }); err != nil {
			return err
		}
	}

	// Create edges via transaction.
	var edgeQueries []string
	for _, e := range hasSectionEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (d:Document {path: %q}), (s:Section {id: %q}) MERGE (d)-[:HAS_SECTION]->(s)`,
			e.DocPath, e.SectionID,
		))
	}
	for _, e := range hasSubsectionEdges {
		edgeQueries = append(edgeQueries, fmt.Sprintf(
			`MATCH (p:Section {id: %q}), (c:Section {id: %q}) MERGE (p)-[:HAS_SUBSECTION]->(c)`,
			e.ParentID, e.ChildID,
		))
	}
	if len(edgeQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), edgeQueries); err != nil {
			return fmt.Errorf("create doc edges: %w", err)
		}
	}

	elapsed := time.Since(start)
	log.Printf("[INFO] DocIndexer.WriteGraph: %s (%d sections) in %v",
		sourcePath, len(sections), elapsed)
	return nil
}

// WriteGraphBatch writes multiple documents to KuzuDB in a single batch.
// Instead of per-document CSV writes and transactions (~200ms each),
// this collects all nodes/edges across documents and does:
//   - 1 CSV COPY for all Document nodes
//   - 1 CSV COPY for all Section nodes
//   - 1 transaction for all edges
//
// This reduces 42 × ~200ms = 8.4s down to ~1-2s.
func (di *DocIndexer) WriteGraphBatch(docs []*DocGraphInput) error {
	if len(docs) == 0 {
		return nil
	}

	di.writeMu.Lock()
	defer di.writeMu.Unlock()

	start := time.Now()

	// Phase 1: Collect all typed data and delete queries.
	var allDocs []kuzu.DocumentNode
	var allSections []kuzu.SectionNode
	var deleteQueries []string
	var edgeQueries []string

	for _, d := range docs {
		docNodes, sections, hasSectionEdges, hasSubsectionEdges := di.extractFromPlugin(d.Result, d.SourcePath)
		if len(docNodes) == 0 {
			log.Printf("[WARN] DocIndexer.WriteGraphBatch: no Document node for %s, skipping", d.SourcePath)
			continue
		}

		// Collect delete queries for idempotent re-index.
		deleteQueries = append(deleteQueries,
			kuzu.DeleteDocumentChunksQuery(d.SourcePath),
			kuzu.DeleteDocumentSectionsQuery(d.SourcePath),
			kuzu.DeleteDocumentQuery(d.SourcePath),
		)

		allDocs = append(allDocs, docNodes...)
		allSections = append(allSections, sections...)

		for _, e := range hasSectionEdges {
			edgeQueries = append(edgeQueries, fmt.Sprintf(
				`MATCH (d:Document {path: %q}), (s:Section {id: %q}) MERGE (d)-[:HAS_SECTION]->(s)`,
				e.DocPath, e.SectionID,
			))
		}
		for _, e := range hasSubsectionEdges {
			edgeQueries = append(edgeQueries, fmt.Sprintf(
				`MATCH (p:Section {id: %q}), (c:Section {id: %q}) MERGE (p)-[:HAS_SUBSECTION]->(c)`,
				e.ParentID, e.ChildID,
			))
		}
	}

	// Phase 2: Delete old data in one transaction.
	if len(deleteQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), deleteQueries); err != nil {
			log.Printf("[WARN] DocIndexer.WriteGraphBatch: delete old data: %v", err)
		}
	}

	// Phase 3: CSV bulk load all nodes in one shot.
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

	if len(allDocs) > 0 {
		if err := doCopy("Document", func(p string) error { return kuzu.WriteDocumentNodesCSV(p, allDocs) }); err != nil {
			return fmt.Errorf("batch Document copy: %w", err)
		}
	}
	if len(allSections) > 0 {
		if err := doCopy("Section", func(p string) error { return kuzu.WriteSectionNodesCSV(p, allSections) }); err != nil {
			return fmt.Errorf("batch Section copy: %w", err)
		}
	}

	// Phase 4: Create all edges in one transaction.
	if len(edgeQueries) > 0 {
		if err := kuzu.ExecTxOn(di.db.Conn(), edgeQueries); err != nil {
			return fmt.Errorf("batch doc edges: %w", err)
		}
	}

	elapsed := time.Since(start)
	log.Printf("[INFO] DocIndexer.WriteGraphBatch: %d documents, %d sections in %v",
		len(allDocs), len(allSections), elapsed)
	return nil
}

// DocGraphInput holds a single document's plugin result for batch writing.
type DocGraphInput struct {
	Result     *gleann.PluginResult
	SourcePath string
}

// extractFromPlugin converts the generic PluginResult into typed KuzuDB structs.
func (di *DocIndexer) extractFromPlugin(result *gleann.PluginResult, sourcePath string) (
	docs []kuzu.DocumentNode,
	sections []kuzu.SectionNode,
	hasSections []kuzu.EdgeHasSection,
	hasSubsections []kuzu.EdgeHasSubsection,
) {
	for _, node := range result.Nodes {
		switch node.Type {
		case "Document":
			docs = append(docs, kuzu.DocumentNode{
				Path:      getStr(node.Data, "path", sourcePath),
				Title:     getStr(node.Data, "title", ""),
				Format:    getStr(node.Data, "format", ""),
				Summary:   getStr(node.Data, "summary", ""),
				WordCount: getInt64(node.Data, "word_count"),
				PageCount: getInt64(node.Data, "page_count"),
			})
		case "Section":
			sections = append(sections, kuzu.SectionNode{
				ID:      getStr(node.Data, "id", ""),
				Heading: getStr(node.Data, "heading", ""),
				Level:   getInt64(node.Data, "level"),
				Content: getStr(node.Data, "content", ""),
				Summary: getStr(node.Data, "summary", ""),
				DocPath: getStr(node.Data, "doc_path", sourcePath),
			})
		}
	}

	for _, edge := range result.Edges {
		switch edge.Type {
		case "HAS_SECTION":
			hasSections = append(hasSections, kuzu.EdgeHasSection{
				DocPath:   edge.From,
				SectionID: edge.To,
			})
		case "HAS_SUBSECTION":
			hasSubsections = append(hasSubsections, kuzu.EdgeHasSubsection{
				ParentID: edge.From,
				ChildID:  edge.To,
			})
		}
	}

	return
}

// buildStructuredDocument converts KuzuDB types back to StructuredDocument for chunking.
func (di *DocIndexer) buildStructuredDocument(doc kuzu.DocumentNode, sections []kuzu.SectionNode) *chunking.StructuredDocument {
	mdSections := make([]chunking.MarkdownSection, len(sections))
	for i, s := range sections {
		// Extract parent ID from HAS_SUBSECTION edges — encoded in section ID.
		// e.g. "doc:report.pdf:s0.1" → parent is "doc:report.pdf:s0"
		parentID := ""
		if lastDot := strings.LastIndex(s.ID, "."); lastDot > 0 {
			parentID = s.ID[:lastDot]
		}

		mdSections[i] = chunking.MarkdownSection{
			ID:       s.ID,
			Heading:  s.Heading,
			Level:    int(s.Level),
			Content:  s.Content,
			Summary:  s.Summary,
			ParentID: parentID,
		}
	}

	wc := int(doc.WordCount)
	pc := int(doc.PageCount)
	return &chunking.StructuredDocument{
		Document: chunking.DocumentMeta{
			Title:     doc.Title,
			Format:    doc.Format,
			PageCount: &pc,
			WordCount: wc,
			Summary:   doc.Summary,
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
