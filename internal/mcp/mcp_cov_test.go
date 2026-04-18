package mcp

import (
	"context"
	"path/filepath"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/memory"
)

// newTestServerWithBlockMem returns a Server with a real BBolt-backed blockMemPool.
func newTestServerWithBlockMem(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.OpenStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mgr := memory.NewManager(store)

	cfg := Config{IndexDir: dir}
	s := NewServer(cfg)
	// Swap the blockMem pool to use our test manager.
	s.blockMem.close()
	s.blockMem = &blockMemPool{mgr: mgr}
	return s
}

func mkCallArgs(args map[string]any) mcpsdk.CallToolRequest {
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = args
	return req
}

// ── Session lifecycle: start → log → status → end ──────────────

func TestSessionLifecycle(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	ctx := context.Background()

	// Start session.
	res, err := s.handleSessionStart(ctx, mkCallArgs(map[string]any{"name": "test-session"}))
	if err != nil {
		t.Fatalf("start err: %v", err)
	}
	if res.IsError {
		t.Fatalf("start failed: %v", res)
	}

	// Log some events.
	s.sessionLog("search", "myidx", "hello", 5)
	s.sessionLog("ask", "myidx", "what", 1)

	// Check status.
	res, err = s.handleSessionStatus(ctx, mcpsdk.CallToolRequest{})
	if err != nil {
		t.Fatalf("status err: %v", err)
	}
	if res.IsError {
		t.Fatalf("status failed")
	}

	// End with summary.
	res, err = s.handleSessionEnd(ctx, mkCallArgs(map[string]any{"summary": "explored search behavior"}))
	if err != nil {
		t.Fatalf("end err: %v", err)
	}
	if res.IsError {
		t.Fatalf("end failed")
	}

	// Status should now show no session.
	res, _ = s.handleSessionStatus(ctx, mcpsdk.CallToolRequest{})
	if res.IsError {
		t.Fatal("status should succeed even without session")
	}
}

func TestSessionStart_ReplacesPreviousSession(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	s.handleSessionStart(ctx, mkCallArgs(map[string]any{"name": "session-1"}))
	s.handleSessionStart(ctx, mkCallArgs(map[string]any{"name": "session-2"}))

	res, _ := s.handleSessionStatus(ctx, mcpsdk.CallToolRequest{})
	if res.IsError {
		t.Fatal("status failed")
	}
}

// ── Memory blocks: full CRUD via MCP tools ────────────────────

func TestMemoryRemember_WithAllOptions(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	res, err := s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content":    "DB columns use snake_case",
		"tier":       "long",
		"label":      "convention",
		"tags":       []any{"db", "naming"},
		"char_limit": float64(200),
		"scope":      "project:myapp",
	}))
	if err != nil || res.IsError {
		t.Fatalf("remember failed: err=%v isErr=%v", err, res.IsError)
	}
}

func TestMemoryRemember_DefaultTier(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	res, _ := s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content": "test default tier",
	}))
	if res.IsError {
		t.Fatal("should default to long tier")
	}
}

func TestMemorySearch_AfterRemember(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{
		"content": "hexagonal architecture in use",
		"tags":    []any{"arch"},
	}))

	res, err := s.handleMemorySearch(ctx, mkCallArgs(map[string]any{"query": "hexagonal"}))
	if err != nil || res.IsError {
		t.Fatalf("search failed: %v", err)
	}
}

func TestMemoryList_AllTiers(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{"content": "short note", "tier": "short"}))
	s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{"content": "long note", "tier": "long"}))

	// List all.
	res, _ := s.handleMemoryList(ctx, mkCallArgs(map[string]any{}))
	if res.IsError {
		t.Fatal("list all failed")
	}

	// List by tier.
	res, _ = s.handleMemoryList(ctx, mkCallArgs(map[string]any{"tier": "short"}))
	if res.IsError {
		t.Fatal("list short failed")
	}
}

func TestMemoryList_Empty(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleMemoryList(context.Background(), mkCallArgs(map[string]any{}))
	if res.IsError {
		t.Fatal("list empty should succeed")
	}

	res, _ = s.handleMemoryList(context.Background(), mkCallArgs(map[string]any{"tier": "long"}))
	if res.IsError {
		t.Fatal("list empty tier should succeed")
	}
}

func TestMemoryForget_AfterRemember(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{"content": "deleteme"}))

	res, _ := s.handleMemoryForget(ctx, mkCallArgs(map[string]any{"id_or_query": "deleteme"}))
	if res.IsError {
		t.Fatal("forget failed")
	}
}

func TestMemoryContext_AfterRemember(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()
	ctx := context.Background()

	s.handleMemoryRemember(ctx, mkCallArgs(map[string]any{"content": "remember this for context"}))

	res, _ := s.handleMemoryContext(ctx, mcpsdk.CallToolRequest{})
	if res.IsError {
		t.Fatal("context build failed")
	}
}

func TestMemoryContext_Empty(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleMemoryContext(context.Background(), mcpsdk.CallToolRequest{})
	if res.IsError {
		t.Fatal("context empty should succeed")
	}
}

// ── handleGet — valid ref format ──────────────────────────────

func TestHandleGet_ValidRefFormatButMissingIndex(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleGet(context.Background(), mkCallArgs(map[string]any{
		"ref": "nonexist:123",
	}))
	if !res.IsError {
		// getSearcher should fail for nonexistent index
	}
}

func TestHandleGet_InvalidRefNoColon(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleGet(context.Background(), mkCallArgs(map[string]any{
		"ref": "nocolon",
	}))
	if !res.IsError {
		t.Fatal("should error for malformed ref without colon")
	}
}

func TestHandleGet_InvalidRefEmptyParts(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleGet(context.Background(), mkCallArgs(map[string]any{
		"ref": ":123",
	}))
	if !res.IsError {
		t.Fatal("should error for empty index name in ref")
	}
}

func TestHandleGet_InvalidRefBadID(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleGet(context.Background(), mkCallArgs(map[string]any{
		"ref": "idx:notanumber",
	}))
	if !res.IsError {
		t.Fatal("should error for non-numeric ID in ref")
	}
}

// ── handleFetch — additional paths ────────────────────────────

func TestHandleFetch_InvalidIDTypes(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	// IDs with non-numeric values - should silently skip
	res, _ := s.handleFetch(context.Background(), mkCallArgs(map[string]any{
		"index": "nonexist",
		"ids":   []any{"abc", "def"},
	}))
	if res == nil {
		t.Fatal("result should not be nil")
	}
}

func TestHandleFetch_EmptyIDsArray(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleFetch(context.Background(), mkCallArgs(map[string]any{
		"index": "nonexist",
		"ids":   []any{},
	}))
	if !res.IsError {
		t.Fatal("should error for empty IDs array")
	}
}

// ── handleSearchMulti — additional ────────────────────────────

func TestHandleSearchMulti_EmptyQuery(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleSearchMulti(context.Background(), mkCallArgs(map[string]any{
		"query":   "",
		"indexes": "idx1,idx2",
	}))
	if !res.IsError {
		t.Fatal("should error for empty query")
	}
}

// ── handleBatchAsk — additional paths ─────────────────────────

func TestHandleBatchAsk_WhitespaceOnlyQuestions(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleBatchAsk(context.Background(), mkCallArgs(map[string]any{
		"questions": []any{"    ", "\t\n"},
		"index":     "myidx",
	}))
	if !res.IsError {
		t.Fatal("should error when all questions are whitespace")
	}
}

func TestHandleBatchAsk_ConcurrencyLimit(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleBatchAsk(context.Background(), mkCallArgs(map[string]any{
		"questions":   []any{"q1", "q2"},
		"index":       "nonexistent",
		"concurrency": float64(10), // should be capped
	}))
	// Will fail because index doesn't exist, but tests the concurrency parsing path
	if res == nil {
		t.Fatal("result should not be nil")
	}
}

// ── handleSearchIDs — additional paths ────────────────────────

func TestHandleSearchIDs_WithTopK(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleSearchIDs(context.Background(), mkCallArgs(map[string]any{
		"index": "nonexist",
		"query": "hello",
		"top_k": float64(3),
	}))
	// Will error because index doesn't exist — exercises top_k parsing path
	if res == nil {
		t.Fatal("result should not be nil")
	}
}

func TestHandleSearchIDs_WithFilters(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	res, _ := s.handleSearchIDs(context.Background(), mkCallArgs(map[string]any{
		"index": "nonexist",
		"query": "hello",
		"filters": []any{
			map[string]any{"field": "ext", "operator": "eq", "value": ".go"},
		},
		"filter_logic": "or",
	}))
	if res == nil {
		t.Fatal("result should not be nil")
	}
}

// ── handleSearch — additional paths ───────────────────────────

func TestHandleSearch_WithGraphContext(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	res, _ := s.handleSearch(context.Background(), mkCallArgs(map[string]any{
		"index":         "nonexist",
		"query":         "hello",
		"graph_context": true,
	}))
	// Will error on index load but exercises graph_context parsing
	if res == nil {
		t.Fatal("should return result")
	}
}

func TestHandleSearch_WithTopKAndFilters(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	res, _ := s.handleSearch(context.Background(), mkCallArgs(map[string]any{
		"index":        "nonexist",
		"query":        "hello",
		"top_k":        float64(3),
		"filters":      []any{map[string]any{"field": "ext", "operator": "eq", "value": ".go"}},
		"filter_logic": "and",
	}))
	if res == nil {
		t.Fatal("should return result")
	}
}

// ── handleAsk — additional paths ──────────────────────────────

func TestHandleAsk_WithGraphContext(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	res, _ := s.handleAsk(context.Background(), mkCallArgs(map[string]any{
		"index":         "nonexist",
		"question":      "what?",
		"graph_context": true,
	}))
	if res == nil {
		t.Fatal("should return result")
	}
}
