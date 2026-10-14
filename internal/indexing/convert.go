package indexing

import (
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// PluginDoc holds a structured extraction result for deferred graph
// indexing. The read phase collects these while chunking; graph indexing
// writes them to KuzuDB as Document/Section nodes.
type PluginDoc struct {
	Result     *gleann.PluginResult
	SourcePath string
}

// PluginResultToDoc converts a PluginResult to a StructuredDocument for
// chunking. This is a pure-Go conversion (no CGo/KuzuDB dependency) — the
// chunking package handles all markdown splitting while preserving section
// hierarchy.
func PluginResultToDoc(result *gleann.PluginResult) *chunking.StructuredDocument {
	var doc chunking.DocumentMeta
	var sections []chunking.MarkdownSection

	for _, node := range result.Nodes {
		switch node.Type {
		case "Document":
			doc.Title, _ = node.Data["title"].(string)
			doc.Format, _ = node.Data["format"].(string)
			doc.Summary, _ = node.Data["summary"].(string)
			if wc, ok := node.Data["word_count"].(float64); ok {
				doc.WordCount = int(wc)
			}
			if pc, ok := node.Data["page_count"].(float64); ok {
				v := int(pc)
				doc.PageCount = &v
			}
		case "Section":
			sec := chunking.MarkdownSection{
				Heading: strVal(node.Data, "heading"),
				Content: strVal(node.Data, "content"),
				Summary: strVal(node.Data, "summary"),
			}
			sec.ID, _ = node.Data["id"].(string)
			if l, ok := node.Data["level"].(float64); ok {
				sec.Level = int(l)
			}
			sections = append(sections, sec)
		}
	}

	// Resolve ParentID from HAS_SUBSECTION edges.
	for _, edge := range result.Edges {
		if edge.Type == "HAS_SUBSECTION" {
			for i := range sections {
				if sections[i].ID == edge.To {
					sections[i].ParentID = edge.From
				}
			}
		}
	}

	return &chunking.StructuredDocument{Document: doc, Sections: sections}
}

// strVal extracts a string value from a map with a zero-value fallback.
func strVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// MarkdownSectionsToPluginResult converts parsed MarkdownSections into a
// PluginResult suitable for graph indexing (Document + Section nodes,
// HAS_SECTION + HAS_SUBSECTION edges). This allows native .md files to
// produce the same graph structure as plugin-extracted documents.
func MarkdownSectionsToPluginResult(sections []chunking.MarkdownSection, sourcePath string, wordCount int, rawMarkdown string) *gleann.PluginResult {
	result := &gleann.PluginResult{}

	// Document node.
	title := "Untitled"
	for _, s := range sections {
		if s.Level == 1 {
			title = s.Heading
			break
		}
	}
	if title == "Untitled" && len(sections) > 0 {
		title = sections[0].Heading
	}

	// Extract summary using the zero-config extractive summarizer.
	summaryStr := gleann.ExtractSummary(rawMarkdown)

	result.Nodes = append(result.Nodes, gleann.PluginNode{
		Type: "Document",
		Data: map[string]any{
			"path":       sourcePath,
			"title":      title,
			"format":     "md",
			"summary":    summaryStr,
			"word_count": float64(wordCount),
			"page_count": float64(0),
		},
	})

	// Section nodes.
	for _, sec := range sections {
		result.Nodes = append(result.Nodes, gleann.PluginNode{
			Type: "Section",
			Data: map[string]any{
				"id":       "doc:" + sourcePath + ":" + sec.ID,
				"heading":  sec.Heading,
				"level":    float64(sec.Level),
				"content":  sec.Content,
				"summary":  sec.Summary,
				"doc_path": sourcePath,
			},
		})
	}

	// Edges.
	for _, sec := range sections {
		fullID := "doc:" + sourcePath + ":" + sec.ID
		if sec.ParentID == "" {
			// Top-level section: Document → HAS_SECTION → Section
			result.Edges = append(result.Edges, gleann.PluginEdge{
				Type: "HAS_SECTION",
				From: sourcePath,
				To:   fullID,
			})
		} else {
			// Child section: Parent → HAS_SUBSECTION → Child
			parentFullID := "doc:" + sourcePath + ":" + sec.ParentID
			result.Edges = append(result.Edges, gleann.PluginEdge{
				Type: "HAS_SUBSECTION",
				From: parentFullID,
				To:   fullID,
			})
		}
	}

	return result
}
