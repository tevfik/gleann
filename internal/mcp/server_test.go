package mcp

import (
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
	}

	// Build tools directly to verify their structure
	builtTools := map[string]bool{
		srv.buildSearchTool().Name:         true,
		srv.buildListTool().Name:           true,
		srv.buildAskTool().Name:            true,
		srv.buildGraphNeighborsTool().Name: true,
		srv.buildDocumentLinksTool().Name:  true,
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
