package integration

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ═══════════════════════════════════════════════════════════════
// E2E: MarkItDown CLI Wrapper
// ═══════════════════════════════════════════════════════════════

func TestE2E_FindMarkItDown(t *testing.T) {
	path, err := gleann.FindMarkItDown()
	if err != nil {
		t.Skipf("markitdown not installed: %v", err)
	}
	if path == "" {
		t.Fatal("FindMarkItDown returned empty path")
	}
	t.Logf("markitdown found at: %s", path)
}

func TestE2E_MarkItDown_CSV(t *testing.T) {
	ext := gleann.NewMarkItDownExtractor()
	if ext == nil {
		t.Skip("markitdown not available")
	}

	// Create a temp CSV file.
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "data.csv")
	err := os.WriteFile(csvPath, []byte("name,age,city\nAlice,30,Istanbul\nBob,25,Ankara\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	md, err := ext.Extract(csvPath)
	if err != nil {
		t.Fatalf("Extract CSV: %v", err)
	}

	if !strings.Contains(md, "Alice") {
		t.Errorf("expected 'Alice' in output, got: %s", md[:min(len(md), 200)])
	}
	if !strings.Contains(md, "Ankara") {
		t.Errorf("expected 'Ankara' in output, got: %s", md[:min(len(md), 200)])
	}
	t.Logf("CSV extraction produced %d bytes", len(md))
}

func TestE2E_MarkItDown_CanHandle(t *testing.T) {
	ext := gleann.NewMarkItDownExtractor()
	if ext == nil {
		t.Skip("markitdown not available")
	}

	// These should be handled.
	for _, e := range []string{".pdf", ".docx", ".xlsx", ".pptx", ".csv", ".png", ".jpg"} {
		if !ext.CanHandle(e) {
			t.Errorf("expected CanHandle(%s) = true", e)
		}
	}

	// These should NOT be handled.
	for _, e := range []string{".go", ".py", ".txt", ".html", ".json", ".rs"} {
		if ext.CanHandle(e) {
			t.Errorf("expected CanHandle(%s) = false", e)
		}
	}
}

// ═══════════════════════════════════════════════════════════════
// E2E: DocExtractor (Layer 0 → PluginResult)
// ═══════════════════════════════════════════════════════════════

func TestE2E_DocExtractor_CSVToPluginResult(t *testing.T) {
	ext := gleann.NewMarkItDownExtractor()
	if ext == nil {
		t.Skip("markitdown not available")
	}

	dir := t.TempDir()
	csvPath := filepath.Join(dir, "people.csv")
	err := os.WriteFile(csvPath, []byte("id,name,role\n1,Alice,Engineer\n2,Bob,Designer\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	de := gleann.NewDocExtractor(ext, nil)
	result, err := de.Extract(csvPath)
	if err != nil {
		t.Fatalf("Extract CSV: %v", err)
	}

	// Must have at least a Document node.
	if len(result.Nodes) < 1 {
		t.Fatal("expected at least 1 node (Document)")
	}
	if result.Nodes[0].Type != "Document" {
		t.Errorf("first node should be Document, got %s", result.Nodes[0].Type)
	}

	// Check format.
	if result.Nodes[0].Data["format"] != "csv" {
		t.Errorf("expected format 'csv', got %v", result.Nodes[0].Data["format"])
	}

	t.Logf("DocExtractor produced %d nodes, %d edges", len(result.Nodes), len(result.Edges))
}

func TestE2E_DocExtractor_FallbackOrder(t *testing.T) {
	// With nil plugin manager + nil markitdown → should error.
	de := gleann.NewDocExtractor(nil, nil)
	_, err := de.Extract("file.pdf")
	if err == nil {
		t.Error("expected error with nil extractors")
	}
}

// ═══════════════════════════════════════════════════════════════
// E2E: MarkdownToPluginResult (Go-native parsing)
// ═══════════════════════════════════════════════════════════════

func TestE2E_MarkdownToPluginResult_ComplexDocument(t *testing.T) {
	md := `# Project Report

This is the executive summary.

## Chapter 1: Introduction

This chapter introduces the project.

### 1.1 Background

Historical background information.

### 1.2 Objectives

Project objectives listed here.

## Chapter 2: Methodology

This chapter describes the methodology.

### 2.1 Data Collection

Data was collected from multiple sources.

### 2.2 Analysis

Analysis was performed using statistical methods.

#### 2.2.1 Regression

Linear regression was used.

#### 2.2.2 Classification

Classification models were trained.

## Chapter 3: Results

The results show significant improvement.

## Conclusion

The project was successful.
`

	result := gleann.MarkdownToPluginResult(md, "report.pdf")

	// Document + 11 sections (H1 also creates a section) = 12 nodes.
	if len(result.Nodes) != 12 {
		t.Fatalf("expected 12 nodes, got %d", len(result.Nodes))
	}

	// Check Document node.
	doc := result.Nodes[0]
	if doc.Type != "Document" {
		t.Errorf("expected Document, got %s", doc.Type)
	}
	if doc.Data["title"] != "Project Report" {
		t.Errorf("expected title 'Project Report', got %v", doc.Data["title"])
	}
	if doc.Data["format"] != "pdf" {
		t.Errorf("expected format 'pdf', got %v", doc.Data["format"])
	}

	// Count edge types.
	hasSectionCount := 0
	hasSubsectionCount := 0
	for _, e := range result.Edges {
		switch e.Type {
		case "HAS_SECTION":
			hasSectionCount++
		case "HAS_SUBSECTION":
			hasSubsectionCount++
		}
	}

	// Root sections: H1 creates s0 (HAS_SECTION from doc), all others are HAS_SUBSECTION
	// Total edges = 11 (1 HAS_SECTION + 10 HAS_SUBSECTION)
	if len(result.Edges) != 11 {
		t.Errorf("expected 11 edges, got %d (has_section=%d, has_subsection=%d)",
			len(result.Edges), hasSectionCount, hasSubsectionCount)
	}

	t.Logf("Complex doc: %d nodes, %d edges (section=%d, subsection=%d)",
		len(result.Nodes), len(result.Edges), hasSectionCount, hasSubsectionCount)
}

// ═══════════════════════════════════════════════════════════════
// E2E: MarkdownChunker Pipeline
// ═══════════════════════════════════════════════════════════════

func TestE2E_FullChunkingPipeline(t *testing.T) {
	// Simulate full pipeline: Markdown → ParseHeadings → ChunkDocument → verify chunks.
	md := `# Architecture

This document describes the system architecture.

## Frontend

The frontend uses React with TypeScript for component rendering.
State management is handled by Redux with middleware for side effects.
The build system uses Vite for fast development and production builds.

## Backend

The backend is written in Go with a clean architecture pattern.
It uses PostgreSQL for persistence and Redis for caching.
API endpoints follow REST conventions with OpenAPI documentation.

### Database

PostgreSQL 15 is the primary database with connection pooling via pgbouncer.
Migrations are managed through golang-migrate.

### API Layer

The API uses Chi router with middleware for authentication and logging.
Rate limiting is implemented using a token bucket algorithm.

## Deployment

Docker Compose for local development.
Kubernetes with Helm charts for production deployment.
`

	chunker := chunking.NewMarkdownChunker(256, 32)
	chunks := chunker.ChunkMarkdown(md, "architecture.md")

	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 chunks, got %d", len(chunks))
	}

	// Check that chunks have proper metadata.
	for i, c := range chunks {
		if c.Metadata == nil {
			t.Errorf("chunk %d has nil metadata", i)
			continue
		}
		if c.Metadata["source"] != "architecture.md" {
			t.Errorf("chunk %d: expected source 'architecture.md', got %v", i, c.Metadata["source"])
		}
		if c.Text == "" {
			t.Errorf("chunk %d has empty text", i)
		}
	}

	// Check that section paths are preserved.
	foundBackend := false
	for _, c := range chunks {
		if path, ok := c.Metadata["section_path"].(string); ok {
			if strings.Contains(path, "Backend") {
				foundBackend = true
			}
		}
	}
	if !foundBackend {
		t.Error("expected at least one chunk with 'Backend' in section path")
	}

	t.Logf("Chunking produced %d chunks from architecture.md", len(chunks))
}

// ═══════════════════════════════════════════════════════════════
// E2E: Plugin Registry
// ═══════════════════════════════════════════════════════════════

func TestE2E_PluginRegistry_LoadNoFile(t *testing.T) {
	// Should not panic when plugins.json doesn't exist.
	reg, err := gleann.LoadPlugins()
	if err != nil {
		// Only fail if it's not a "file not found" error.
		if !os.IsNotExist(err) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if reg == nil {
		t.Log("Registry is nil (no plugins.json)")
	} else {
		t.Logf("Registry loaded with %d plugins", len(reg.Plugins))
	}
}

func TestE2E_PluginManager_FindExtractor_NoPlugins(t *testing.T) {
	pm := &gleann.PluginManager{
		Registry: &gleann.PluginRegistry{},
	}
	plugin := pm.FindDocumentExtractor(".pdf")
	if plugin != nil {
		t.Error("expected nil when no plugins registered")
	}
}

func TestE2E_PluginManager_FindExtractor_WithPlugin(t *testing.T) {
	pm := &gleann.PluginManager{
		Registry: &gleann.PluginRegistry{
			Plugins: []gleann.Plugin{
				{
					Name:         "gleann-docs",
					URL:          "http://localhost:5050",
					Capabilities: []string{"document-extraction"},
					Extensions:   []string{".pdf", ".docx", ".xlsx"},
				},
			},
		},
	}

	// Should find it.
	plugin := pm.FindDocumentExtractor(".pdf")
	if plugin == nil {
		t.Fatal("expected to find plugin for .pdf")
	}
	if plugin.Name != "gleann-docs" {
		t.Errorf("expected 'gleann-docs', got %s", plugin.Name)
	}

	// Should not find it.
	plugin = pm.FindDocumentExtractor(".go")
	if plugin != nil {
		t.Error("expected nil for .go extension")
	}
}

// ═══════════════════════════════════════════════════════════════
// E2E: Cross-Platform Compatibility
// ═══════════════════════════════════════════════════════════════

func TestE2E_CrossPlatform_PathHandling(t *testing.T) {
	// Test that filepath.Join works correctly on current platform.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get user home dir")
	}

	pluginPath := filepath.Join(home, ".gleann", "plugins", "gleann-docs")
	if pluginPath == "" {
		t.Fatal("filepath.Join produced empty path")
	}

	// On Windows, should use backslashes.
	if runtime.GOOS == "windows" {
		if strings.Contains(pluginPath, "/") {
			t.Errorf("Windows path contains forward slashes: %s", pluginPath)
		}
	}

	t.Logf("Plugin path on %s: %s", runtime.GOOS, pluginPath)
}

func TestE2E_CrossPlatform_VenvBinaries(t *testing.T) {
	// Test the expected venv binary paths per platform.
	venvDir := filepath.Join(t.TempDir(), ".venv")

	var expectedPip, expectedPython string
	if runtime.GOOS == "windows" {
		expectedPip = filepath.Join(venvDir, "Scripts", "pip.exe")
		expectedPython = filepath.Join(venvDir, "Scripts", "python.exe")
	} else {
		expectedPip = filepath.Join(venvDir, "bin", "pip")
		expectedPython = filepath.Join(venvDir, "bin", "python")
	}

	t.Logf("Expected pip: %s", expectedPip)
	t.Logf("Expected python: %s", expectedPython)

	// Verify the paths are non-empty and platform-appropriate.
	if expectedPip == "" || expectedPython == "" {
		t.Fatal("expected non-empty paths")
	}
}

func TestE2E_CrossPlatform_SharedLibExtension(t *testing.T) {
	// Check correct shared library extension per platform.
	var expectedExt string
	switch runtime.GOOS {
	case "windows":
		expectedExt = ".dll"
	case "darwin":
		expectedExt = ".dylib"
	default: // linux, freebsd, etc.
		expectedExt = ".so"
	}

	t.Logf("Platform %s: shared lib ext = %s", runtime.GOOS, expectedExt)
}

func TestE2E_CrossPlatform_MarkItDownPaths(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get user home dir")
	}

	// The FindMarkItDown function should check platform-appropriate paths.
	var expectedPaths []string
	switch runtime.GOOS {
	case "windows":
		expectedPaths = []string{
			filepath.Join(home, "AppData", "Roaming", "Python", "Scripts", "markitdown.exe"),
			filepath.Join(home, ".local", "bin", "markitdown.exe"),
		}
	case "darwin":
		expectedPaths = []string{
			filepath.Join(home, ".local", "bin", "markitdown"),
			filepath.Join(home, "Library", "Python", "bin", "markitdown"),
		}
	default: // linux
		expectedPaths = []string{
			filepath.Join(home, ".local", "bin", "markitdown"),
			filepath.Join(home, ".local", "pipx", "venvs", "markitdown", "bin", "markitdown"),
		}
	}

	t.Logf("Expected markitdown paths on %s:", runtime.GOOS)
	for _, p := range expectedPaths {
		exists := "✗"
		if _, err := os.Stat(p); err == nil {
			exists = "✓"
		}
		t.Logf("  %s %s", exists, p)
	}
}

// ═══════════════════════════════════════════════════════════════
// E2E: Symlink (Windows compatibility)
// ═══════════════════════════════════════════════════════════════

func TestE2E_CrossPlatform_Symlink(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "link")

	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}

	err := os.Symlink(src, dst)
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("Symlink failed on Windows (expected without admin): %v", err)
			t.Log("RECOMMENDATION: Use os.Rename or directory copy on Windows instead of symlinks")
		} else {
			t.Fatalf("Symlink failed on %s: %v", runtime.GOOS, err)
		}
	} else {
		// Verify it's a symlink.
		info, err := os.Lstat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Error("expected symlink mode")
		}
	}
}

// ═══════════════════════════════════════════════════════════════
// E2E: Go-Native Document Extraction (zero Python deps)
// ═══════════════════════════════════════════════════════════════

func TestE2E_NativeExtractor_CSV(t *testing.T) {
	tmp := t.TempDir()
	csvPath := filepath.Join(tmp, "data.csv")

	f, _ := os.Create(csvPath)
	w := csv.NewWriter(f)
	w.WriteAll([][]string{{"X", "Y"}, {"1", "2"}, {"3", "4"}})
	f.Close()

	n := gleann.NewNativeExtractor()
	md, err := n.Extract(csvPath)
	if err != nil {
		t.Fatalf("native CSV extract: %v", err)
	}
	if !strings.Contains(md, "| X | Y |") {
		t.Error("expected markdown table")
	}
	t.Logf("Native CSV: %d bytes", len(md))
}

func TestE2E_NativeExtractor_FullPipeline(t *testing.T) {
	// Full pipeline: CSV → NativeExtractor → DocExtractor → PluginResult → graph nodes
	tmp := t.TempDir()
	csvPath := filepath.Join(tmp, "pipeline.csv")

	f, _ := os.Create(csvPath)
	w := csv.NewWriter(f)
	w.WriteAll([][]string{{"Name", "Score"}, {"Alpha", "95"}, {"Beta", "87"}})
	f.Close()

	// DocExtractor with NO markitdown, NO plugins → should use native.
	de := gleann.NewDocExtractor(nil, nil)
	result, err := de.Extract(csvPath)
	if err != nil {
		t.Fatalf("DocExtractor native pipeline: %v", err)
	}

	if len(result.Nodes) == 0 {
		t.Fatal("expected at least 1 node")
	}
	if result.Nodes[0].Type != "Document" {
		t.Errorf("expected Document node, got %s", result.Nodes[0].Type)
	}

	t.Logf("Native pipeline: %d nodes, %d edges", len(result.Nodes), len(result.Edges))
}

func TestE2E_NativeExtractor_AlwaysAvailable(t *testing.T) {
	// NativeExtractor should always be available (no external deps).
	n := gleann.NewNativeExtractor()
	if n == nil {
		t.Fatal("NativeExtractor should never be nil")
	}

	// Should handle common document types.
	for _, ext := range []string{".csv", ".docx", ".xlsx", ".pptx", ".pdf", ".md", ".txt", ".html"} {
		if !n.CanHandle(ext) {
			t.Errorf("NativeExtractor should handle %s", ext)
		}
	}
}
