package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func createEmptyCallToolRequest() mcp.CallToolRequest {
	return mcp.CallToolRequest{}
}

func TestNewServer_ToolRegistration(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
	if srv.mcpServer == nil {
		t.Fatal("mcpServer is nil")
	}
}

func TestNewServer_ToolNames(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	// Verify tool definitions are well-formed
	tools := []struct {
		name string
		fn   func() interface{ GetName() string }
	}{
		{"gleann_search", nil},
		{"gleann_list", nil},
		{"gleann_ask", nil},
		{"gleann_graph_neighbors", nil},
		{"gleann_document_links", nil},
		{"gleann_read_full_document", nil},
		{"gleann_sync", nil},
	}

	// Build tools directly to verify their structure
	builtTools := map[string]bool{
		srv.buildSearchTool().Name:           true,
		srv.buildListTool().Name:             true,
		srv.buildAskTool().Name:              true,
		srv.buildGraphNeighborsTool().Name:   true,
		srv.buildDocumentLinksTool().Name:    true,
		srv.buildReadFullDocumentTool().Name: true,
		srv.buildDocumentTOCTool().Name:      true,
		srv.buildSyncTool().Name:             true,
	}

	for _, tt := range tools {
		if !builtTools[tt.name] {
			t.Errorf("tool %q not found in registered tools", tt.name)
		}
	}
}

func TestParseFilters(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]interface{}
		wantCount int
		wantLogic string
	}{
		{
			name:      "no filters",
			args:      map[string]interface{}{},
			wantCount: 0,
			wantLogic: "and",
		},
		{
			name: "single filter",
			args: map[string]interface{}{
				"filters": []interface{}{
					map[string]interface{}{
						"field":    "ext",
						"operator": "eq",
						"value":    ".go",
					},
				},
			},
			wantCount: 1,
			wantLogic: "and",
		},
		{
			name: "multiple filters with or logic",
			args: map[string]interface{}{
				"filter_logic": "or",
				"filters": []interface{}{
					map[string]interface{}{
						"field":    "ext",
						"operator": "eq",
						"value":    ".go",
					},
					map[string]interface{}{
						"field":    "type",
						"operator": "eq",
						"value":    "function",
					},
				},
			},
			wantCount: 2,
			wantLogic: "or",
		},
		{
			name: "invalid filter entry skipped",
			args: map[string]interface{}{
				"filters": []interface{}{
					"not a map",
					map[string]interface{}{
						"field":    "ext",
						"operator": "eq",
						"value":    ".py",
					},
				},
			},
			wantCount: 1,
			wantLogic: "and",
		},
		{
			name: "invalid logic defaults to and",
			args: map[string]interface{}{
				"filter_logic": "xor",
			},
			wantCount: 0,
			wantLogic: "and",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters, logic := parseFilters(tt.args)
			if len(filters) != tt.wantCount {
				t.Errorf("parseFilters() got %d filters, want %d", len(filters), tt.wantCount)
			}
			if logic != tt.wantLogic {
				t.Errorf("parseFilters() logic = %q, want %q", logic, tt.wantLogic)
			}
		})
	}
}

func TestHandleList_EmptyIndexDir(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	// Create a mock MCP request
	result, err := srv.handleList(nil, createEmptyCallToolRequest())
	if err != nil {
		t.Fatalf("handleList returned error: %v", err)
	}
	if result == nil {
		t.Fatal("handleList returned nil result")
	}
}

func TestHandleList_WithIndexes(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake index directory with meta.json
	indexDir := filepath.Join(tmpDir, "test-index")
	if err := os.MkdirAll(indexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaJSON := `{"name":"test-index","backend":"hnsw","embedding_model":"bge-m3","num_passages":42}`
	if err := os.WriteFile(filepath.Join(indexDir, "meta.json"), []byte(metaJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	result, err := srv.handleList(nil, createEmptyCallToolRequest())
	if err != nil {
		t.Fatalf("handleList returned error: %v", err)
	}
	if result == nil {
		t.Fatal("handleList returned nil result")
	}
}

func TestBuildSearchTool_Schema(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	tool := srv.buildSearchTool()
	if tool.Name != "gleann_search" {
		t.Errorf("search tool name = %q, want %q", tool.Name, "gleann_search")
	}
	if len(tool.InputSchema.Required) != 2 {
		t.Errorf("search tool required params = %d, want 2", len(tool.InputSchema.Required))
	}

	// Verify required fields
	required := map[string]bool{}
	for _, r := range tool.InputSchema.Required {
		required[r] = true
	}
	if !required["index"] || !required["query"] {
		t.Error("search tool should require 'index' and 'query'")
	}

	// Verify graph_context property is present in schema
	props := tool.InputSchema.Properties
	if _, ok := props["graph_context"]; !ok {
		t.Error("search tool schema should include 'graph_context' property")
	} else {
		gcProp := props["graph_context"].(map[string]interface{})
		if gcProp["type"] != "boolean" {
			t.Errorf("graph_context type = %v, want boolean", gcProp["type"])
		}
	}
}

func TestLRUEviction(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	// touchLRU should handle empty list gracefully
	srv.touchLRU("nonexistent")
	if len(srv.searcherLRU) != 0 {
		t.Errorf("touchLRU on empty list should not add entries, got %d", len(srv.searcherLRU))
	}

	// evictOldest on empty should not panic
	srv.evictOldest()
	if len(srv.searchers) != 0 {
		t.Error("evictOldest on empty should leave searchers empty")
	}

	// Simulate adding entries to searcherLRU and searchers
	for i := 0; i < 3; i++ {
		name := "idx-" + string(rune('a'+i))
		srv.searchers[name] = nil // placeholder, Close() won't be called on nil
		srv.searcherLRU = append(srv.searcherLRU, name)
	}

	// Touch middle entry — should move to end
	srv.touchLRU("idx-b")
	if srv.searcherLRU[len(srv.searcherLRU)-1] != "idx-b" {
		t.Errorf("touchLRU should move 'idx-b' to end, got %v", srv.searcherLRU)
	}

	// Evict oldest
	srv.evictOldest()
	if _, ok := srv.searchers["idx-a"]; ok {
		t.Error("evictOldest should have removed 'idx-a'")
	}
	if len(srv.searcherLRU) != 2 {
		t.Errorf("after eviction LRU len = %d, want 2", len(srv.searcherLRU))
	}
}

func TestMaxCachedSearchers(t *testing.T) {
	if maxCachedSearchers < 1 {
		t.Error("maxCachedSearchers should be at least 1")
	}
}

func TestBuildReadFullDocumentTool_Schema(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	tool := srv.buildReadFullDocumentTool()
	if tool.Name != "gleann_read_full_document" {
		t.Errorf("expected tool name gleann_read_full_document, got %q", tool.Name)
	}

	required := map[string]bool{}
	for _, r := range tool.InputSchema.Required {
		required[r] = true
	}
	if !required["index"] || !required["vpath"] {
		t.Errorf("expected required fields index and vpath, got %v", tool.InputSchema.Required)
	}
}

func TestHandleReadFullDocument_Validation(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	// 1. Invalid arguments format
	reqBad := mcp.CallToolRequest{}
	reqBad.Params.Arguments = "invalid-type"
	res, err := srv.handleReadFullDocument(nil, reqBad)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for invalid arguments format")
	}

	// 2. Missing required parameters
	reqMissing := mcp.CallToolRequest{}
	reqMissing.Params.Arguments = map[string]interface{}{
		"index": "",
		"vpath": "",
	}
	res2, err := srv.handleReadFullDocument(nil, reqMissing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res2.IsError {
		t.Errorf("expected error result for empty arguments")
	}

	// 3. Fallback on-disk direct read
	testFilePath := filepath.Join(tmpDir, "readme.md")
	testContent := "# Gleann Full Document Test Content\nValidating read_full_document tool."
	if err := os.WriteFile(testFilePath, []byte(testContent), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	reqFallback := mcp.CallToolRequest{}
	reqFallback.Params.Arguments = map[string]interface{}{
		"index": "nonexistent-index",
		"vpath": testFilePath,
	}
	res3, err := srv.handleReadFullDocument(nil, reqFallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fallback to direct file read if relative path exists
	if res3.IsError {
		t.Errorf("expected successful file read via fallback, got error result")
	}
}

func TestBuildDocumentTOCTool_Schema(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	tool := srv.buildDocumentTOCTool()
	if tool.Name != "gleann_document_toc" {
		t.Errorf("expected tool name gleann_document_toc, got %q", tool.Name)
	}

	required := map[string]bool{}
	for _, r := range tool.InputSchema.Required {
		required[r] = true
	}
	if !required["index"] {
		t.Errorf("expected required field index, got %v", tool.InputSchema.Required)
	}
}

func TestHandleDocumentTOC_Validation(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	// 1. Invalid arguments format
	reqBad := mcp.CallToolRequest{}
	reqBad.Params.Arguments = "invalid-type"
	res, err := srv.handleDocumentTOC(nil, reqBad)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for invalid arguments format")
	}

	// 2. Missing required index parameter
	reqMissing := mcp.CallToolRequest{}
	reqMissing.Params.Arguments = map[string]interface{}{
		"index": "",
	}
	res2, err := srv.handleDocumentTOC(nil, reqMissing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res2.IsError {
		t.Errorf("expected error result for empty index")
	}
}

func TestBuildSyncTool(t *testing.T) {
	srv := &Server{}
	tool := srv.buildSyncTool()

	if tool.Name != "gleann_sync" {
		t.Errorf("expected name gleann_sync, got %s", tool.Name)
	}
	if tool.Description == "" {
		t.Errorf("expected non-empty description")
	}

	props := tool.InputSchema.Properties
	if props["index"] == nil {
		t.Errorf("expected property index")
	}
	if props["docs_dir"] == nil {
		t.Errorf("expected property docs_dir")
	}
	if props["files"] == nil {
		t.Errorf("expected property files")
	}

	required := make(map[string]bool)
	for _, req := range tool.InputSchema.Required {
		required[req] = true
	}
	if !required["index"] {
		t.Errorf("expected required field index, got %v", tool.InputSchema.Required)
	}
}

func TestHandleSync(t *testing.T) {
	tmpDir := t.TempDir()
	srv := NewServer(Config{
		IndexDir:          tmpDir,
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})

	// 1. Missing index parameter
	reqMissing := mcp.CallToolRequest{}
	reqMissing.Params.Arguments = map[string]interface{}{}
	res, err := srv.handleSync(nil, reqMissing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for missing index")
	}

	// 2. Non-existent index
	reqNonExistent := mcp.CallToolRequest{}
	reqNonExistent.Params.Arguments = map[string]interface{}{
		"index": "non-existent-index",
	}
	res, err = srv.handleSync(nil, reqNonExistent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected error result for nonexistent index")
	}

	// 3. Existing index with mocked syncRunner
	testIndex := "test-sync-idx"
	idxDir := filepath.Join(tmpDir, testIndex)
	_ = os.MkdirAll(idxDir, 0755)
	meta := gleann.IndexMeta{
		Name:      testIndex,
		SourceDir: "/tmp/mock-source",
	}
	metaBytes, _ := json.Marshal(meta)
	_ = os.WriteFile(filepath.Join(idxDir, testIndex+".meta.json"), metaBytes, 0644)

	var capturedIndex, capturedDocs string
	var capturedFiles []string
	srv.syncRunner = func(ctx context.Context, idx, docs string, files []string) (string, error) {
		capturedIndex = idx
		capturedDocs = docs
		capturedFiles = files
		return "Mock sync complete: 2 files processed", nil
	}

	reqValid := mcp.CallToolRequest{}
	reqValid.Params.Arguments = map[string]interface{}{
		"index": testIndex,
		"files": []interface{}{"file1.go", "file2.go"},
	}
	res, err = srv.handleSync(nil, reqValid)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res)
	}

	if capturedIndex != testIndex {
		t.Errorf("expected index %s, got %s", testIndex, capturedIndex)
	}
	if capturedDocs != "/tmp/mock-source" {
		t.Errorf("expected docs /tmp/mock-source, got %s", capturedDocs)
	}
	if len(capturedFiles) != 2 || capturedFiles[0] != "file1.go" {
		t.Errorf("expected files [file1.go, file2.go], got %v", capturedFiles)
	}
}



