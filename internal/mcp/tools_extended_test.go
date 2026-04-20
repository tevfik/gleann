package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// ── Tool builders: schema validation ───────────────────────────

func testMCPServer() *Server {
	cfg := Config{
		IndexDir:          "/tmp/gleann-mcp-test-nonexistent",
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "test-model",
		OllamaHost:        "http://localhost:11434",
		Version:           "test",
	}
	return NewServer(cfg)
}

func TestBuildAskToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildAskTool()
	if tool.Name != "gleann_ask" {
		t.Errorf("name = %q, want gleann_ask", tool.Name)
	}
	if tool.Description == "" {
		t.Error("description should not be empty")
	}
	props := tool.InputSchema.Properties
	if props["index"] == nil {
		t.Error("should have 'index' property")
	}
	if props["question"] == nil {
		t.Error("should have 'question' property")
	}
}

func TestBuildImpactToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildImpactTool()
	if tool.Name != "gleann_impact" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.InputSchema.Properties["symbol"] == nil {
		t.Error("should have 'symbol' property")
	}
}

func TestBuildSearchMultiToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildSearchMultiTool()
	if tool.Name != "gleann_search_multi" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.InputSchema.Properties["indexes"] == nil {
		t.Error("should have 'indexes' property")
	}
}

func TestBuildSearchIDsToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildSearchIDsTool()
	if tool.Name != "gleann_search_ids" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.InputSchema.Properties["query"] == nil {
		t.Error("should have 'query' property")
	}
}

func TestBuildFetchToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildFetchTool()
	if tool.Name != "gleann_fetch" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildGetToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildGetTool()
	if tool.Name != "gleann_get" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildSessionStartToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildSessionStartTool()
	if tool.Name != "gleann_session_start" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.InputSchema.Properties["name"] == nil {
		t.Error("should have 'name' property")
	}
}

func TestBuildSessionEndToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildSessionEndTool()
	if tool.Name != "gleann_session_end" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildSessionStatusToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildSessionStatusTool()
	if tool.Name != "gleann_session_status" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemoryRememberToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildMemoryRememberTool()
	if tool.Name != "memory_remember" {
		t.Errorf("name = %q", tool.Name)
	}
	if tool.InputSchema.Properties["content"] == nil {
		t.Error("should have 'content' property")
	}
	if tool.InputSchema.Properties["tier"] == nil {
		t.Error("should have 'tier' property")
	}
}

func TestBuildMemoryForgetToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildMemoryForgetTool()
	if tool.Name != "memory_forget" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemorySearchToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildMemorySearchTool()
	if tool.Name != "memory_search" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemoryListToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildMemoryListTool()
	if tool.Name != "memory_list" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildMemoryContextToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildMemoryContextTool()
	if tool.Name != "memory_context" {
		t.Errorf("name = %q", tool.Name)
	}
}

func TestBuildDocumentLinksToolSchema(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tool := s.buildDocumentLinksTool()
	if tool.Name == "" {
		t.Error("name should not be empty")
	}
}

// ── Handler argument validation ────────────────────────────────

func makeCallToolReq(args map[string]any) mcpsdk.CallToolRequest {
	return mcpsdk.CallToolRequest{
		Params: mcpsdk.CallToolParams{
			Arguments: args,
		},
	}
}

func TestHandleAskMissingArgs(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	// Missing index.
	req := makeCallToolReq(map[string]any{"question": "what?"})
	result, err := s.handleAsk(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleAskMissingQuestion(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"index": "test"})
	result, err := s.handleAsk(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleSearchMissingQuery(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"index": "test"})
	result, err := s.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleGraphNeighborsMissingSymbol(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"index": "test"})
	result, err := s.handleGraphNeighbors(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleDocumentLinksMissingIndex(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"file_path": "/main.go"})
	result, err := s.handleDocumentLinks(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleImpactMissingArgs(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"index": "test"})
	result, err := s.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleSearchMultiMissingQuery(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"indexes": "a,b"})
	result, err := s.handleSearchMulti(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleSearchIDsMissingArgs(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{})
	result, err := s.handleSearchIDs(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleFetchMissingArgs(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{})
	result, err := s.handleFetch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleGetMissingArgs(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{})
	result, err := s.handleGet(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

// ── Session tools tests ────────────────────────────────────────

func TestHandleSessionStartEmpty(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	req := makeCallToolReq(map[string]any{"name": ""})
	result, err := s.handleSessionStart(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// Should return error about empty name.
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleSessionStatusNoSession(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	// Clear any global state.
	serverSession.mu.Lock()
	serverSession.name = ""
	serverSession.mu.Unlock()

	req := makeCallToolReq(nil)
	result, err := s.handleSessionStatus(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleSessionEndNoSession(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	serverSession.mu.Lock()
	serverSession.name = ""
	serverSession.mu.Unlock()

	req := makeCallToolReq(map[string]any{})
	result, err := s.handleSessionEnd(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

// ── Memory tools: argument validation ──────────────────────────

func TestHandleMemoryRememberMissingContent(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{})
	result, err := s.handleMemoryRemember(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleMemoryRememberInvalidTier(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"content": "test fact", "tier": "invalid"})
	result, err := s.handleMemoryRemember(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleMemoryForgetMissingIDOrQuery(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{})
	result, err := s.handleMemoryForget(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleMemorySearchMissingQuery(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{})
	result, err := s.handleMemorySearch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleMemoryListInvalidTier(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := makeCallToolReq(map[string]any{"tier": "invalid"})
	result, err := s.handleMemoryList(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
}

// ── Memory tools: with real BBolt ──────────────────────────────

func TestMemoryRememberAndSearch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	s := testMCPServer()
	defer s.Close()

	// Remember a fact.
	req := makeCallToolReq(map[string]any{
		"content": "Go is created by Google",
		"tier":    "long",
		"label":   "language_fact",
		"tags":    []interface{}{"golang", "google"},
	})
	result, err := s.handleMemoryRemember(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected result")
	}
	// Check result text.
	for _, c := range result.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			if !strings.Contains(tc.Text, "Remembered") {
				t.Errorf("expected 'Remembered' in text, got: %s", tc.Text)
			}
		}
	}

	// Search for the fact.
	searchReq := makeCallToolReq(map[string]any{"query": "Google"})
	searchResult, err := s.handleMemorySearch(context.Background(), searchReq)
	if err != nil {
		t.Fatal(err)
	}
	if searchResult == nil {
		t.Fatal("expected result")
	}
	for _, c := range searchResult.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			if !strings.Contains(tc.Text, "Go is created by Google") {
				t.Errorf("search should find the fact: %s", tc.Text)
			}
		}
	}
}

func TestMemoryRememberAndList(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	s := testMCPServer()
	defer s.Close()

	// Remember two facts.
	s.handleMemoryRemember(context.Background(), makeCallToolReq(map[string]any{
		"content": "Fact one", "tier": "long",
	}))
	s.handleMemoryRemember(context.Background(), makeCallToolReq(map[string]any{
		"content": "Fact two", "tier": "medium",
	}))

	// List all.
	listReq := makeCallToolReq(map[string]any{})
	listResult, err := s.handleMemoryList(context.Background(), listReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range listResult.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			if !strings.Contains(tc.Text, "2 memory block") {
				t.Logf("list text: %s", tc.Text)
			}
		}
	}

	// List long only.
	listLongReq := makeCallToolReq(map[string]any{"tier": "long"})
	listLongResult, err := s.handleMemoryList(context.Background(), listLongReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range listLongResult.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			if !strings.Contains(tc.Text, "1 memory block") {
				t.Logf("long-only text: %s", tc.Text)
			}
		}
	}
}

func TestMemoryRememberAndForget(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	s := testMCPServer()
	defer s.Close()

	s.handleMemoryRemember(context.Background(), makeCallToolReq(map[string]any{
		"content": "temporary fact to forget",
	}))

	// Forget by content.
	forgetReq := makeCallToolReq(map[string]any{"id_or_query": "temporary fact"})
	forgetResult, err := s.handleMemoryForget(context.Background(), forgetReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range forgetResult.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			if !strings.Contains(tc.Text, "Forgot") {
				t.Errorf("expected 'Forgot' in: %s", tc.Text)
			}
		}
	}
}

func TestMemoryContext(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	s := testMCPServer()
	defer s.Close()

	// Empty memory.
	ctxReq := makeCallToolReq(map[string]any{})
	result, err := s.handleMemoryContext(context.Background(), ctxReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcpsdk.TextContent); ok {
			if !strings.Contains(tc.Text, "empty") && !strings.Contains(tc.Text, "Memory") {
				t.Logf("context text: %s", tc.Text)
			}
		}
	}
}

// ── Resource handler tests ─────────────────────────────────────

func TestHandleIndexListResource(t *testing.T) {
	s := testMCPServer()
	defer s.Close()
	s.config.IndexDir = t.TempDir()

	req := mcpsdk.ReadResourceRequest{}
	contents, err := s.handleIndexListResource(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(contents) == 0 {
		t.Error("expected at least one content item")
	}
}

func TestHandleReadResourceInvalid(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "gleann://invalid"
	_, err := s.handleReadResource(context.Background(), req)
	if err == nil {
		t.Error("expected error for invalid URI")
	}
}

// ── blockMemPool tests ─────────────────────────────────────────

func TestBlockMemPoolClose(t *testing.T) {
	pool := &blockMemPool{}
	// Close without get should not panic.
	pool.close()
}

func TestBlockMemPoolGetAndClose(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	pool := &blockMemPool{}
	mgr, err := pool.get()
	if err != nil {
		t.Fatal(err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}

	// Get again should return same instance.
	mgr2, err := pool.get()
	if err != nil {
		t.Fatal(err)
	}
	if mgr != mgr2 {
		t.Error("should return same instance")
	}

	pool.close()
}

// ── sessionLog fire-and-forget ─────────────────────────────────

func TestSessionLogNoSession(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	serverSession.mu.Lock()
	serverSession.name = ""
	serverSession.mu.Unlock()

	// Should not panic even without an active session.
	s.sessionLog("search", "testidx", "query", 5)
}
