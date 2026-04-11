// gen_fixtures generates binary document fixtures for gleann e2e tests.
// Produces: DOCX, XLSX, PPTX, and HTML files in the e2e/fixtures/binary/ directory.
//
// Usage:
//
//	go run ./e2e/cmd/gen_fixtures/
//
// This creates deterministic fixture files that are committed to the repo and used
// by the e2e/run.sh test suite to verify native document extraction (PDF, DOCX, etc.).

package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

func main() {
	outDir := filepath.Join("e2e", "fixtures", "binary")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}

	generators := []struct {
		name string
		fn   func(string) error
	}{
		{"technical_report.docx", genDOCX},
		{"benchmark_data.xlsx", genXLSX},
		{"research_slides.pptx", genPPTX},
		{"api_reference.html", genHTML},
	}

	ok := true
	for _, g := range generators {
		path := filepath.Join(outDir, g.name)
		if err := g.fn(path); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", g.name, err)
			ok = false
		} else {
			fi, _ := os.Stat(path)
			fmt.Printf("✓ %-30s  %5d bytes\n", g.name, fi.Size())
		}
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println("\nAll fixtures generated in", outDir)
}

// ── DOCX ────────────────────────────────────────────────────────────────────

func genDOCX(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	// [Content_Types].xml
	ct, _ := zw.Create("[Content_Types].xml")
	ct.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Override PartName="/word/document.xml"
    ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`))

	// _rels/.rels
	rels, _ := zw.Create("_rels/.rels")
	rels.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
    Target="word/document.xml"/>
</Relationships>`))

	// word/document.xml
	type para struct {
		style string
		text  string
	}
	paragraphs := []para{
		{"Heading1", "Vector Database Performance Report"},
		{"Normal", "This report evaluates HNSW and FAISS backend performance for the gleann vector search engine across multiple embedding models and dataset sizes."},
		{"Heading2", "Executive Summary"},
		{"Normal", "The FAISS backend achieves 35% lower P99 latency compared to the pure-Go HNSW implementation for datasets exceeding 100k documents. HNSW remains preferable for deployments without CGo support."},
		{"Heading2", "Methodology"},
		{"Normal", "Benchmarks were run on a single-node AMD EPYC 9354 server with 128GB RAM and NVMe SSD storage. Each backend was evaluated with nomic-embed-text (768 dimensions) and bge-m3 (1024 dimensions) embedding models."},
		{"Heading2", "Recall at 10"},
		{"Normal", "FAISS IndexFlatL2 achieves perfect recall (0.99) at the cost of exhaustive search. HNSW with ef_search=128 achieves 0.94 recall at 10x lower memory usage than flat exhaustive search."},
		{"Heading2", "Hybrid Search Impact"},
		{"Normal", "Combining BM25 lexical search with HNSW vector search (alpha=0.7) improves recall by 4% on domain-specific technical queries compared to vector-only search. This is particularly effective for rare terminology like algorithm names and API identifiers."},
		{"Heading2", "Recommendations"},
		{"Normal", "For production deployments with more than 50k documents: use gleann-full binary (FAISS enabled) with IVF1024 index. For development and CI environments: use pure-Go HNSW backend. Enable hybrid search for technical documentation corpora."},
	}

	var xmlBuf strings.Builder
	xmlBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>`)
	for _, p := range paragraphs {
		xmlBuf.WriteString(`<w:p>`)
		if p.style != "" {
			xmlBuf.WriteString(`<w:pPr><w:pStyle w:val="`)
			xml.Escape(&xmlBuf, []byte(p.style))
			xmlBuf.WriteString(`"/></w:pPr>`)
		}
		xmlBuf.WriteString(`<w:r><w:t xml:space="preserve">`)
		xml.Escape(&xmlBuf, []byte(p.text))
		xmlBuf.WriteString(`</w:t></w:r></w:p>`)
	}
	xmlBuf.WriteString(`</w:body></w:document>`)

	doc, _ := zw.Create("word/document.xml")
	doc.Write([]byte(xmlBuf.String()))
	return nil
}

// ── XLSX ────────────────────────────────────────────────────────────────────

func genXLSX(path string) error {
	f := excelize.NewFile()

	// Sheet 1: Vector search benchmarks
	f.SetSheetName("Sheet1", "Search Benchmarks")
	headers := []string{"Backend", "Dimensions", "Dataset Size", "Build Time (s)", "P50 Latency (ms)", "P99 Latency (ms)", "Recall@10", "QPS", "Memory (MB)"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Search Benchmarks", cell, h)
	}

	rows := [][]any{
		{"hnsw", 768, 100_000, 12.4, 1.2, 8.5, 0.94, 820, 312},
		{"faiss-flat", 768, 100_000, 9.8, 0.8, 4.2, 0.99, 1240, 448},
		{"faiss-ivf", 768, 100_000, 14.2, 1.1, 6.8, 0.96, 1100, 280},
		{"hnsw", 1024, 500_000, 58.3, 1.8, 12.4, 0.93, 680, 890},
		{"faiss-ivf", 1024, 500_000, 72.1, 1.4, 9.2, 0.95, 920, 540},
		{"hybrid-hnsw+bm25", 768, 100_000, 20.4, 2.1, 14.3, 0.97, 610, 356},
	}
	for ri, row := range rows {
		for ci, val := range row {
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+2)
			f.SetCellValue("Search Benchmarks", cell, val)
		}
	}

	// Sheet 2: Embedding model comparison
	f.NewSheet("Embedding Models")
	eHeaders := []string{"Model", "Provider", "Dimensions", "Tokens/s", "MTEB Score", "Storage/1k docs (MB)"}
	for i, h := range eHeaders {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue("Embedding Models", cell, h)
	}
	eRows := [][]any{
		{"nomic-embed-text", "ollama", 768, 4200, 0.621, 2.8},
		{"bge-m3", "ollama", 1024, 2800, 0.654, 3.9},
		{"text-embedding-3-small", "openai", 1536, 12000, 0.672, 5.9},
		{"text-embedding-3-large", "openai", 3072, 6000, 0.703, 11.8},
		{"snowflake-arctic-embed", "ollama", 1024, 3100, 0.641, 3.9},
	}
	for ri, row := range eRows {
		for ci, val := range row {
			cell, _ := excelize.CoordinatesToCellName(ci+1, ri+2)
			f.SetCellValue("Embedding Models", cell, val)
		}
	}

	return f.SaveAs(path)
}

// ── PPTX ────────────────────────────────────────────────────────────────────

func genPPTX(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	slides := []struct{ title, body string }{
		{
			"gleann: AI-Powered Search Engine",
			"Semantic search, code intelligence, and long-term memory in a single binary",
		},
		{
			"Dual Backend Architecture",
			"HNSW: Pure-Go, zero dependencies, ideal for development and CI\nFAISS: C++ optimized, 35% lower P99 latency, for production workloads over 50k docs",
		},
		{
			"Retrieval Pipeline",
			"1. Document ingestion with native extraction (PDF, DOCX, XLSX, HTML)\n2. AST-aware code chunking via tree-sitter (16 languages)\n3. Parallel embedding with batching and caching\n4. Hybrid HNSW+BM25 search with reranking\n5. LLM-powered answer generation with retrieved context",
		},
		{
			"Community Detection Results",
			"Louvain algorithm on gleann codebase:\nModularity Q = 0.597 (strong community structure)\n235 communities identified\n20 god nodes detected (hotspots)\n62x token reduction vs full corpus",
		},
		{
			"Performance Metrics",
			"Build time: ~18s for 100k token codebase\nSearch latency P50: 1.2ms (HNSW) / 0.8ms (FAISS)\nRecall@10: 0.94 (HNSW) / 0.99 (FAISS flat)\nEmbedding cache hit rate: 78% on rebuild\nMemory: 312MB RSS for 100k documents (HNSW)",
		},
	}

	for i, slide := range slides {
		slideXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
       xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld>
    <p:spTree>
      <p:sp><p:txBody><a:p><a:r><a:t>%s</a:t></a:r></a:p></p:txBody></p:sp>
      <p:sp><p:txBody><a:p><a:r><a:t>%s</a:t></a:r></a:p></p:txBody></p:sp>
    </p:spTree>
  </p:cSld>
</p:sld>`, xmlEscape(slide.title), xmlEscape(slide.body))

		w, _ := zw.Create(fmt.Sprintf("ppt/slides/slide%d.xml", i+1))
		w.Write([]byte(slideXML))
	}
	return nil
}

// ── HTML ────────────────────────────────────────────────────────────────────

func genHTML(path string) error {
	content := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>gleann REST API Reference</title>
</head>
<body>
<h1>gleann REST API Reference</h1>
<p>gleann exposes a full OpenAPI-compatible REST API for programmatic index management, 
semantic search, RAG queries, and memory operations.</p>

<h2>Base URL</h2>
<p>Default: <code>http://localhost:8080</code>. Configure via <code>server_addr</code> in config.json 
or <code>GLEANN_SERVER_ADDR</code> environment variable.</p>

<h2>Index Operations</h2>

<h3>POST /api/v1/indexes</h3>
<p>Create a new vector index from a list of text passages.</p>
<pre><code>{
  "name": "my-docs",
  "texts": ["passage one", "passage two"],
  "metadata": [{"source": "doc.md", "section": "intro"}]
}</code></pre>

<h3>GET /api/v1/indexes</h3>
<p>List all available indexes with metadata including passage count, embedding model, and backend.</p>

<h3>DELETE /api/v1/indexes/{name}</h3>
<p>Remove an index and all associated embeddings. Irreversible.</p>

<h2>Search</h2>

<h3>POST /api/v1/indexes/{name}/search</h3>
<p>Semantic vector search with optional BM25 hybrid reranking.</p>
<pre><code>{
  "query": "Byzantine fault tolerant consensus",
  "top_k": 10,
  "hybrid_alpha": 0.7,
  "filters": [{"field": "source", "op": "contains", "value": "protocol"}]
}</code></pre>

<h2>Memory API</h2>

<h3>POST /api/v1/memory/ingest</h3>
<p>Store facts, summaries, or any text content into the hierarchical memory system.</p>
<pre><code>{
  "content": "The user prefers concise technical answers with code examples",
  "tier": "long",
  "tags": ["preference", "style"],
  "project": "my-project"
}</code></pre>

<h3>POST /api/v1/memory/recall</h3>
<p>Retrieve relevant memories using semantic similarity and tier/tag filters.</p>

<h2>OpenAI-Compatible Proxy</h2>
<p>gleann implements the OpenAI Chat Completions API, allowing any OpenAI-compatible client 
to use gleann indexes as knowledge sources by setting model to <code>gleann/{index-name}</code>.</p>
<pre><code>POST /v1/chat/completions
{
  "model": "gleann/my-docs",
  "messages": [{"role": "user", "content": "What is the token bucket algorithm?"}]
}</code></pre>

<h2>Authentication</h2>
<p>Configure an API key via <code>GLEANN_API_KEY</code> environment variable. 
When set, all API requests must include <code>Authorization: Bearer {key}</code>.</p>
</body>
</html>`

	return os.WriteFile(path, []byte(content), 0o644)
}

func xmlEscape(s string) string {
	var b strings.Builder
	xml.Escape(&b, []byte(s))
	return b.String()
}
