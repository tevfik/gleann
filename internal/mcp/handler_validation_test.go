package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// ── handleSearch — missing index / missing query ───────────────

func TestHandleSearchMissingIndexHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query": "hello",
	}
	result, err := s.handleSearch(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

func TestHandleSearchMissingQueryHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleSearch(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when query is missing")
	}
}

func TestHandleSearchNonexistentIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "nonexistent",
		"query": "hello",
	}
	result, err := s.handleSearch(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail for nonexistent index")
	}
}

// ── handleList — bad index ─────────────────────────────────────

func TestHandleListEmptyDir(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	result, err := s.handleList(context.Background(), req)
	// Should succeed with empty list
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

// ── handleAsk — validation ─────────────────────────────────────

func TestHandleAskMissingIndexHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"question": "what?",
	}
	result, err := s.handleAsk(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

func TestHandleAskMissingQuestionHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleAsk(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when question is missing")
	}
}

// ── handleGraphNeighbors — validation ──────────────────────────

func TestHandleGraphNeighborsMissingSymbolHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleGraphNeighbors(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when symbol is missing")
	}
}

func TestHandleGraphNeighborsMissingIndexHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol": "main",
	}
	result, err := s.handleGraphNeighbors(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

// ── handleImpact — validation ──────────────────────────────────

func TestHandleImpactMissingSymbolHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleImpact(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when symbol is missing")
	}
}

// ── handleDocumentLinks — validation ───────────────────────────

func TestHandleDocumentLinksMissingIndexHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"file": "main.go",
	}
	result, err := s.handleDocumentLinks(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

func TestHandleDocumentLinksMissingFileHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleDocumentLinks(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when file is missing")
	}
}

// ── handleSearchIDs — validation ───────────────────────────────

func TestHandleSearchIDsMissingIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query": "hello",
	}
	result, err := s.handleSearchIDs(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

func TestHandleSearchIDsMissingQuery(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleSearchIDs(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when query is missing")
	}
}

// ── handleFetch — validation ───────────────────────────────────

func TestHandleFetchMissingIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"ids": []any{float64(1), float64(2)},
	}
	result, err := s.handleFetch(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

func TestHandleFetchMissingIDs(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleFetch(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when ids is missing")
	}
}

// ── handleGet — validation ─────────────────────────────────────

func TestHandleGetMissingIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"id": float64(1),
	}
	result, err := s.handleGet(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

func TestHandleGetMissingID(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleGet(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when id is missing")
	}
}

// ── handleSearchMulti — validation ─────────────────────────────

func TestHandleSearchMultiMissingIndexesHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"query": "hello",
	}
	// indexes is optional — searches all when missing, so it may succeed or fail depending on state
	result, err := s.handleSearchMulti(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

func TestHandleSearchMultiMissingQueryHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"indexes": "idx1,idx2",
	}
	result, err := s.handleSearchMulti(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when query is missing")
	}
}

// ── handleBatchAsk — validation ────────────────────────────────

func TestHandleBatchAskMissingIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"questions": []any{"q1", "q2"},
	}
	result, err := s.handleBatchAsk(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when index is missing")
	}
}

func TestHandleBatchAskMissingQuestions(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}
	result, err := s.handleBatchAsk(context.Background(), req)
	if err == nil && result != nil && !result.IsError {
		t.Error("should fail when questions is missing")
	}
}

// ── LRU cache tests ────────────────────────────────────────────

func TestTouchLRUMovesToEnd(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	s.searcherLRU = []string{"a", "b", "c"}
	s.touchLRU("a")
	if s.searcherLRU[len(s.searcherLRU)-1] != "a" {
		t.Errorf("LRU after touch: %v", s.searcherLRU)
	}
}

func TestEvictOldestEmpty(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	s.searcherLRU = nil
	s.evictOldest() // should not panic
}

func TestEvictOldestRemoves(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	s.searcherLRU = []string{"a", "b", "c"}
	s.searchers["a"] = nil // intentionally nil
	s.evictOldest()
	if len(s.searcherLRU) != 2 {
		t.Errorf("LRU len = %d, want 2", len(s.searcherLRU))
	}
	if _, exists := s.searchers["a"]; exists {
		t.Error("should have removed 'a' from cache")
	}
}

// ── Session tools — handler validation ─────────────────────────

func TestHandleSessionStartNoSessionHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	result, err := s.handleSessionStart(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestHandleSessionEndNoSessionHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	result, err := s.handleSessionEnd(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestHandleSessionStatusNoSessionHV(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{}
	result, err := s.handleSessionStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

// ── Memory KG stub tools ───────────────────────────────────────

func TestHandleInjectKGStub(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"entity_name": "TestEntity",
		"entity_type": "concept",
		"observations": []any{"obs1"},
	}
	result, err := s.handleInjectKG(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestHandleDeleteEntityStub(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"entity_name": "TestEntity",
	}
	result, err := s.handleDeleteEntity(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestHandleTraverseKGStub(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"start": "NodeA",
	}
	result, err := s.handleTraverseKG(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

// ── handleReadResource ─────────────────────────────────────────

func TestHandleReadResourceInvalidURI(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "gleann:///"
	_, err := s.handleReadResource(context.Background(), req)
	if err == nil {
		t.Error("should fail on invalid URI")
	}
}

func TestHandleReadResourceNonexistentIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "gleann://nonexistent/main.go"
	_, err := s.handleReadResource(context.Background(), req)
	if err == nil {
		t.Error("should fail for nonexistent index")
	}
}
