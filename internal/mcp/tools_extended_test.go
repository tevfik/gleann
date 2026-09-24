package mcp

import (
	"context"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/memory"
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
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

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
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

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
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

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
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

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
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

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

func TestMemoryAutoRepoScopeAndIsolation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

	s := testMCPServer()
	defer s.Close()

	ctx := context.Background()

	// 1. Remember in scope repoA
	_, err := s.handleMemoryRemember(ctx, makeCallToolReq(map[string]any{
		"content": "Secret config for Project Alpha",
		"scope":   "org/repoA",
	}))
	if err != nil {
		t.Fatal(err)
	}

	// 2. Remember in scope repoB
	_, err = s.handleMemoryRemember(ctx, makeCallToolReq(map[string]any{
		"content": "Secret config for Project Beta",
		"scope":   "org/repoB",
	}))
	if err != nil {
		t.Fatal(err)
	}

	// 3. Search within repoA scope — must NOT see repoB
	resA, err := s.handleMemorySearch(ctx, makeCallToolReq(map[string]any{
		"query": "Secret config",
		"scope": "org/repoA",
	}))
	if err != nil {
		t.Fatal(err)
	}
	textA := resA.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(textA, "Project Alpha") {
		t.Errorf("expected Project Alpha in repoA search, got: %s", textA)
	}
	if strings.Contains(textA, "Project Beta") {
		t.Errorf("repoB memory leaked into repoA scope search: %s", textA)
	}

	// 4. Search within repoB scope — must NOT see repoA
	resB, err := s.handleMemorySearch(ctx, makeCallToolReq(map[string]any{
		"query": "Secret config",
		"scope": "org/repoB",
	}))
	if err != nil {
		t.Fatal(err)
	}
	textB := resB.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(textB, "Project Beta") {
		t.Errorf("expected Project Beta in repoB search, got: %s", textB)
	}
	if strings.Contains(textB, "Project Alpha") {
		t.Errorf("repoA memory leaked into repoB scope search: %s", textB)
	}

	// 5. Search with scope 'all' — sees both
	resAll, err := s.handleMemorySearch(ctx, makeCallToolReq(map[string]any{
		"query": "Secret config",
		"scope": "all",
	}))
	if err != nil {
		t.Fatal(err)
	}
	textAll := resAll.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(textAll, "Project Alpha") || !strings.Contains(textAll, "Project Beta") {
		t.Errorf("scope 'all' should find both projects, got: %s", textAll)
	}
}

func TestMemoryProvenanceFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

	s := testMCPServer()
	defer s.Close()

	ctx := context.Background()

	// 1. Remember with provenance fields
	res, err := s.handleMemoryRemember(ctx, makeCallToolReq(map[string]any{
		"content": "Store uses BoltDB bucket 'blocks'",
		"tier":    "long",
		"label":   "architecture",
		"repo":    "github.com/tevfik/gleann",
		"paths":   []interface{}{"pkg/memory/store.go", "pkg/memory/block.go"},
		"symbols": []interface{}{"OpenStore", "Block"},
		"commit":  "abcdef123456",
	}))
	if err != nil {
		t.Fatalf("remember failed: %v", err)
	}
	text := res.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "symbols: OpenStore, Block") {
		t.Errorf("expected symbols in response, got: %s", text)
	}
	if !strings.Contains(text, "files: pkg/memory/store.go, pkg/memory/block.go") {
		t.Errorf("expected files in response, got: %s", text)
	}

	// 2. Verify stored block fields in store
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := mgr.Store().List(memory.TierLong)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	b := blocks[0]
	if b.Repo != "github.com/tevfik/gleann" {
		t.Errorf("expected repo 'github.com/tevfik/gleann', got %q", b.Repo)
	}
	if len(b.Paths) != 2 || b.Paths[0] != "pkg/memory/store.go" {
		t.Errorf("unexpected paths: %v", b.Paths)
	}
	if len(b.Symbols) != 2 || b.Symbols[0] != "OpenStore" {
		t.Errorf("unexpected symbols: %v", b.Symbols)
	}
	if b.Commit != "abcdef123456" {
		t.Errorf("expected commit abcdef123456, got %q", b.Commit)
	}

	// 3. Test Context rendering includes symbols
	ctxRes, err := s.handleMemoryContext(ctx, makeCallToolReq(map[string]any{
		"scope": "github.com/tevfik/gleann",
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctxText := ctxRes.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(ctxText, "(symbols: OpenStore, Block)") {
		t.Errorf("context window should include symbols, got: %s", ctxText)
	}

	// 4. Mark block suspect and check context warning
	b.Suspect = true
	b.StaleReason = "symbol OpenStore signature changed"
	if err := mgr.Store().Update(&b); err != nil {
		t.Fatal(err)
	}

	ctxRes2, err := s.handleMemoryContext(ctx, makeCallToolReq(map[string]any{
		"scope": "github.com/tevfik/gleann",
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctxText2 := ctxRes2.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(ctxText2, "<suspect_memory>") || !strings.Contains(ctxText2, "[⚠️ SUSPECT: symbol OpenStore signature changed]") {
		t.Errorf("context window should display suspect warning in suspect_memory, got: %s", ctxText2)
	}
}

func TestMemoryDedupAndContradiction_MCP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

	s := testMCPServer()
	defer s.Close()

	ctx := context.Background()

	// 1. Remember fact first time
	res1, err := s.handleMemoryRemember(ctx, makeCallToolReq(map[string]any{
		"content": "Database uses BoltDB for persistent storage",
		"tier":    "long",
		"scope":   "project-alpha",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text1 := res1.Content[0].(mcpsdk.TextContent).Text
	if strings.Contains(text1, "reinforced") {
		t.Fatalf("first remember should not be marked reinforced: %s", text1)
	}

	// 2. Remember exact same fact second time -> should dedup and reinforce
	res2, err := s.handleMemoryRemember(ctx, makeCallToolReq(map[string]any{
		"content": "Database uses BoltDB for persistent storage",
		"tier":    "long",
		"scope":   "project-alpha",
	}))
	if err != nil {
		t.Fatal(err)
	}
	text2 := res2.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text2, "reinforced 2x") {
		t.Fatalf("expected reinforced 2x notice on dedup, got: %s", text2)
	}

	// Verify only 1 block exists in store with confirms = 1
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := mgr.ListScoped("project-alpha", memory.TierLong)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 deduplicated block, got %d", len(blocks))
	}
	if blocks[0].Confirms != 1 {
		t.Errorf("expected Confirms=1, got %d", blocks[0].Confirms)
	}
}

func TestMemoryStalenessDetection_MCP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

	s := testMCPServer()
	defer s.Close()

	ctx := context.Background()

	// 1. Remember a fact with symbols and paths
	_, err := s.handleMemoryRemember(ctx, makeCallToolReq(map[string]any{
		"content": "Router uses chi mux with auth middleware",
		"tier":    "long",
		"scope":   "web-repo",
		"paths":   []interface{}{"internal/router/router.go"},
		"symbols": []interface{}{"SetupRouter"},
	}))
	if err != nil {
		t.Fatal(err)
	}

	// 2. Context before change: normal <long_term_memory>
	ctxBefore, err := s.handleMemoryContext(ctx, makeCallToolReq(map[string]any{"scope": "web-repo"}))
	if err != nil {
		t.Fatal(err)
	}
	tb := ctxBefore.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(tb, "<long_term_memory>") || strings.Contains(tb, "<suspect_memory>") {
		t.Errorf("expected clean long term memory, got: %s", tb)
	}

	// 3. Mark symbol suspect via MarkSuspect
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}
	n, err := mgr.MarkSuspect(nil, []string{"SetupRouter"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 block marked suspect, got %d", n)
	}

	// 4. Context after change: block isolated in <suspect_memory> with warning
	ctxAfter, err := s.handleMemoryContext(ctx, makeCallToolReq(map[string]any{"scope": "web-repo"}))
	if err != nil {
		t.Fatal(err)
	}
	ta := ctxAfter.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(ta, "<suspect_memory>") {
		t.Errorf("expected suspect_memory section, got: %s", ta)
	}
	if !strings.Contains(ta, "[⚠️ SUSPECT: symbol SetupRouter was modified]") {
		t.Errorf("expected suspect warning with symbol reason, got: %s", ta)
	}
}

func TestSessionEndPromotion_MCP(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("GLEANN_MEMORY_DIR", tmp)

	s := testMCPServer()
	defer s.Close()

	ctx := context.Background()

	// 1. Start work session
	_, err := s.handleSessionStart(ctx, makeCallToolReq(map[string]any{
		"name": "refactor-session-xyz",
	}))
	if err != nil {
		t.Fatal(err)
	}

	// 2. Log events
	s.sessionLog("search", "myidx", "memory architecture", 3)
	s.sessionLog("ask", "myidx", "how does dedup work", 1)

	// 3. End session
	endRes, err := s.handleSessionEnd(ctx, makeCallToolReq(map[string]any{
		"summary": "Completed memory deduplication and staleness check",
	}))
	if err != nil {
		t.Fatal(err)
	}
	endText := endRes.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(endText, "refactor-session-xyz") {
		t.Errorf("unexpected end response: %s", endText)
	}

	// 4. Verify that session logs were promoted to medium-term and summary to long-term
	mgr, err := s.blockMem.get()
	if err != nil {
		t.Fatal(err)
	}

	medBlocks, err := mgr.Store().List(memory.TierMedium)
	if err != nil {
		t.Fatal(err)
	}
	// At least the session start and log events promoted
	if len(medBlocks) == 0 {
		t.Errorf("expected promoted medium-term blocks after session end, got 0")
	}

	longBlocks, err := mgr.Store().List(memory.TierLong)
	if err != nil {
		t.Fatal(err)
	}
	foundSummary := false
	for _, lb := range longBlocks {
		if lb.Label == "session_summary" && strings.Contains(lb.Content, "refactor-session-xyz") {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Errorf("expected session_summary block in long-term memory")
	}
}

func TestMCP_SearchTool_RerankSchemaAndOptions(t *testing.T) {
	s := testMCPServer()
	defer s.Close()

	tool := s.buildSearchTool()
	props := tool.InputSchema.Properties
	if props["rerank"] == nil {
		t.Fatal("gleann_search should have 'rerank' property")
	}
	if props["include_tests"] == nil {
		t.Fatal("gleann_search should have 'include_tests' property")
	}
	if props["kind"] == nil {
		t.Fatal("gleann_search should have 'kind' property")
	}

	toolIDs := s.buildSearchIDsTool()
	propsIDs := toolIDs.InputSchema.Properties
	if propsIDs["rerank"] == nil {
		t.Fatal("gleann_search_ids should have 'rerank' property")
	}
}

func TestMCP_ToolProfiles(t *testing.T) {
	// 1. Default (core profile)
	srvCore := NewServer(Config{
		IndexDir:          "/tmp/test-mcp-core",
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "test",
		ToolsProfile:      "core",
	})
	defer srvCore.Close()

	coreExpected := []string{
		"gleann_search",
		"gleann_read",
		"gleann_graph_neighbors",
		"gleann_impact",
		"memory_remember",
		"memory_context",
		"memory_search",
		"memory_forget",
		"gleann_sync",
	}
	for _, tool := range coreExpected {
		if !srvCore.isToolEnabled(tool) {
			t.Errorf("core profile should enable %q", tool)
		}
	}

	coreDisabled := []string{
		"gleann_list",
		"gleann_ask",
		"gleann_batch_ask",
		"gleann_shell",
		"gleann_gain",
		"inject_knowledge_graph",
		"gleann_communities",
	}
	for _, tool := range coreDisabled {
		if srvCore.isToolEnabled(tool) {
			t.Errorf("core profile should NOT enable %q", tool)
		}
	}

	// 2. Full profile
	srvFull := NewServer(Config{
		IndexDir:          "/tmp/test-mcp-full",
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "test",
		ToolsProfile:      "full",
	})
	defer srvFull.Close()

	for _, tool := range append(coreExpected, coreDisabled...) {
		if !srvFull.isToolEnabled(tool) {
			t.Errorf("full profile should enable %q", tool)
		}
	}

	// 3. Custom profile with aliases (e.g. "search,symbol,recall")
	srvCustom := NewServer(Config{
		IndexDir:          "/tmp/test-mcp-custom",
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "test",
		ToolsProfile:      "search,symbol,recall",
	})
	defer srvCustom.Close()

	if !srvCustom.isToolEnabled("gleann_search") {
		t.Errorf("custom profile should enable gleann_search")
	}
	if !srvCustom.isToolEnabled("gleann_graph_neighbors") {
		t.Errorf("custom profile should enable gleann_graph_neighbors via 'symbol' alias")
	}
	if !srvCustom.isToolEnabled("memory_context") {
		t.Errorf("custom profile should enable memory_context via 'recall' alias")
	}
	if !srvCustom.isToolEnabled("memory_search") {
		t.Errorf("custom profile should enable memory_search via 'recall' alias")
	}
	if srvCustom.isToolEnabled("gleann_read") {
		t.Errorf("custom profile should NOT enable gleann_read")
	}
}



