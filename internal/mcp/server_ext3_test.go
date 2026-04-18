package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// ── Tool builder tests ─────────────────────────

func TestBuildSearchToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildSearchTool()
	if tool.Name != "gleann_search" {
		t.Errorf("name = %q", tool.Name)
	}
	if len(tool.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(tool.InputSchema.Required))
	}
}

func TestBuildListToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildListTool()
	if tool.Name != "gleann_list" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildAskToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildAskTool()
	if tool.Name != "gleann_ask" {
		t.Errorf("name = %q", tool.Name)
	}
	if len(tool.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required, got %d", len(tool.InputSchema.Required))
	}
}

func TestBuildGraphNeighborsToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildGraphNeighborsTool()
	if tool.Name != "gleann_graph_neighbors" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildDocumentLinksToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildDocumentLinksTool()
	if tool.Name != "gleann_document_links" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildImpactToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildImpactTool()
	if tool.Name != "gleann_impact" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildSearchIDsToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildSearchIDsTool()
	if tool.Name != "gleann_search_ids" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildFetchToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildFetchTool()
	if tool.Name != "gleann_fetch" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildGetToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildGetTool()
	if tool.Name != "gleann_get" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildSearchMultiToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildSearchMultiTool()
	if tool.Name != "gleann_search_multi" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildBatchAskToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildBatchAskTool()
	if tool.Name != "gleann_batch_ask" {
		t.Errorf("name = %q", tool.Name)
	}
}

// ── Session tool builder tests ─────────────────

func TestBuildSessionStartToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildSessionStartTool()
	if tool.Name != "gleann_session_start" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildSessionEndToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildSessionEndTool()
	if tool.Name != "gleann_session_end" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildSessionStatusToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildSessionStatusTool()
	if tool.Name != "gleann_session_status" {
		t.Errorf("name = %q", tool.Name)
	}
}

// ── Memory block tool builder tests ────────────

func TestBuildMemoryRememberToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildMemoryRememberTool()
	if tool.Name != "memory_remember" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemoryForgetToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildMemoryForgetTool()
	if tool.Name != "memory_forget" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemorySearchToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildMemorySearchTool()
	if tool.Name != "memory_search" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemoryListToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildMemoryListTool()
	if tool.Name != "memory_list" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemoryContextToolExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	tool := s.buildMemoryContextTool()
	if tool.Name != "memory_context" {
		t.Errorf("name = %q", tool.Name)
	}
}

// ── Handler validation tests ───────────────────

func TestHandleSearchInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not a map" // invalid
	result, err := s.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

func TestHandleSearchNonexistentIndexExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"index": "nope", "query": "test"}
	result, err := s.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent index")
	}
}

func TestHandleAskInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = 42 // invalid
	result, err := s.handleAsk(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleAskEmptyFieldsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"index": "", "question": ""}
	result, err := s.handleAsk(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for empty fields")
	}
}

func TestHandleGraphNeighborsInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = 42
	result, err := s.handleGraphNeighbors(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleGraphNeighborsEmptyFieldsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"index": "", "node_fqn": ""}
	result, err := s.handleGraphNeighbors(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleDocumentLinksInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = 42
	result, err := s.handleDocumentLinks(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleDocumentLinksEmptyExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"index": "", "doc_path": ""}
	result, err := s.handleDocumentLinks(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleImpactInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = 42
	result, err := s.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleImpactEmptyExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"index": "", "symbol": ""}
	result, err := s.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleSearchIDsInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = 42
	result, err := s.handleSearchIDs(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleListEmptyIndexDirExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	result, err := s.handleList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Should return "No indexes found." or empty list — not an error.
	if result.IsError {
		t.Fatal("should not be an error for empty dir")
	}
}

func TestHandleSearchMultiInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleSearchMulti(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleBatchAskInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleBatchAsk(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

// ── Session handler tests ──────────────────────

func TestHandleSessionStatusNoActiveExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	// Ensure no active session.
	serverSession.mu.Lock()
	serverSession.name = ""
	serverSession.mu.Unlock()

	req := mcpsdk.CallToolRequest{}
	result, err := s.handleSessionStatus(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("should not error")
	}
}

func TestHandleSessionStartInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleSessionStart(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleSessionStartEmptyNameExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"name": "   "}
	result, err := s.handleSessionStart(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for empty name")
	}
}

func TestHandleSessionEndNoActiveExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	serverSession.mu.Lock()
	serverSession.name = ""
	serverSession.mu.Unlock()

	req := mcpsdk.CallToolRequest{}
	result, err := s.handleSessionEnd(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("should not error")
	}
}

// ── Memory block handler tests ─────────────────

func TestHandleMemoryRememberInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleMemoryRemember(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleMemoryRememberEmptyContentExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"content": ""}
	result, err := s.handleMemoryRemember(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for empty content")
	}
}

func TestHandleMemoryRememberBadTierExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"content": "test", "tier": "invalid"}
	result, err := s.handleMemoryRemember(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for bad tier")
	}
}

func TestHandleMemoryForgetInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleMemoryForget(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleMemoryForgetEmptyExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"id_or_query": ""}
	result, err := s.handleMemoryForget(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleMemorySearchInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleMemorySearch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleMemorySearchEmptyExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"query": ""}
	result, err := s.handleMemorySearch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleMemoryListNoTierExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	result, err := s.handleMemoryList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// May error due to blockMem not being able to open, or succeed with empty.
	_ = result
}

func TestHandleMemoryListBadTierExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{"tier": "invalid_tier"}
	result, err := s.handleMemoryList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid tier")
	}
}

// ── parseFilters edge cases ────────────────────

func TestParseFiltersWithBadItemExt3(t *testing.T) {
	args := map[string]interface{}{
		"filters": []interface{}{
			"not-a-map",
			map[string]interface{}{"field": "f", "operator": "eq", "value": "v"},
		},
	}
	filters, logic := parseFilters(args)
	if len(filters) != 1 {
		t.Errorf("expected 1 filter, got %d", len(filters))
	}
	if logic != "and" {
		t.Errorf("expected and, got %s", logic)
	}
}

func TestParseFiltersOrLogicExt3(t *testing.T) {
	args := map[string]interface{}{
		"filter_logic": "or",
		"filters": []interface{}{
			map[string]interface{}{"field": "f", "operator": "eq", "value": "v"},
		},
	}
	_, logic := parseFilters(args)
	if logic != "or" {
		t.Errorf("expected or, got %s", logic)
	}
}

func TestParseFiltersInvalidLogicExt3(t *testing.T) {
	args := map[string]interface{}{
		"filter_logic": "xor",
	}
	_, logic := parseFilters(args)
	if logic != "and" {
		t.Errorf("expected default and, got %s", logic)
	}
}

func TestParseFiltersMissingFieldsExt3(t *testing.T) {
	args := map[string]interface{}{
		"filters": []interface{}{
			map[string]interface{}{"field": "f"}, // missing operator and value
		},
	}
	filters, _ := parseFilters(args)
	if len(filters) != 0 {
		t.Errorf("should skip incomplete filter, got %d", len(filters))
	}
}

// ── blockMemPool ───────────────────────────────

func TestBlockMemPoolCloseNilExt3(t *testing.T) {
	pool := &blockMemPool{}
	// Should not panic.
	pool.close()
}

// ── mcpMemoryPool ──────────────────────────────

func TestMCPMemoryPoolExt3(t *testing.T) {
	pool := newMCPMemoryPool(t.TempDir())
	// closeAll on empty pool should not panic.
	pool.closeAll()
}

// ── handleReadResource ─────────────────────────

func TestHandleReadResourceBadURIExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "http://wrong-scheme/test"
	_, err := s.handleReadResource(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for bad scheme")
	}
}

func TestHandleReadResourceBadFormatExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "gleann://just-index-no-file"
	_, err := s.handleReadResource(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for bad format")
	}
}

// ── sessionLog fire-and-forget ─────────────────

func TestSessionLogNoActiveSessionExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	serverSession.mu.Lock()
	serverSession.name = ""
	serverSession.mu.Unlock()

	// Should not panic.
	s.sessionLog("search", "idx", "query", 5)
}

// ── handleFetch / handleGet invalid args ───────

func TestHandleFetchInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleFetch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleGetInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "bad"
	result, err := s.handleGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error")
	}
}

func TestHandleMemoryContextInvalidArgsExt3(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]interface{}{}
	// May succeed or fail depending on BBolt availability; just verify no panic.
	_, _ = s.handleMemoryContext(context.Background(), req)
}
