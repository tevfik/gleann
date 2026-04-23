package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// ── handleImpact — additional coverage ─────────────────────────

func TestHandleImpactInvalidArgs(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not a map"
	result, err := s.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result for invalid args")
	}
}

func TestHandleImpactMissingIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol": "main.main",
	}
	result, err := s.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result when index missing")
	}
}

func TestHandleImpactNonexistentIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index":  "nonexistent",
		"symbol": "pkg.Func",
	}
	result, err := s.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result for nonexistent index")
	}
}

func TestHandleImpactWithMaxDepth(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index":     "nonexistent",
		"symbol":    "pkg.Func",
		"max_depth": float64(3),
	}
	result, err := s.handleImpact(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should reach getSearcher and fail
	if result == nil || !result.IsError {
		t.Error("expected error for nonexistent index")
	}
}

// ── handleGraphNeighbors — additional coverage ─────────────────

func TestHandleGraphNeighborsInvalidArgs(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not a map"
	result, err := s.handleGraphNeighbors(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result for invalid args")
	}
}

func TestHandleGraphNeighborsNonexistentIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index":    "nonexistent",
		"node_fqn": "main.Func",
	}
	result, err := s.handleGraphNeighbors(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error for nonexistent index")
	}
}

// ── handleDocumentLinks — additional coverage ──────────────────

func TestHandleDocumentLinksInvalidArgs(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not a map"
	result, err := s.handleDocumentLinks(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result for invalid args")
	}
}

func TestHandleDocumentLinksNonexistentIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index":    "nonexistent",
		"doc_path": "docs/readme.md",
	}
	result, err := s.handleDocumentLinks(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error for nonexistent index")
	}
}

// ── handleSearch — additional invalid args ─────────────────────

func TestHandleSearchInvalidArgs(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not a map"
	result, err := s.handleSearch(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result for invalid args")
	}
}

// ── handleAsk — additional coverage ────────────────────────────

func TestHandleAskInvalidArgs(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not a map"
	result, err := s.handleAsk(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error result for invalid args")
	}
}

func TestHandleAskNonexistentIndex(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index":    "nonexistent",
		"question": "what is this?",
	}
	result, err := s.handleAsk(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("expected error for nonexistent index")
	}
}

// ── handleList — additional coverage ───────────────────────────

func TestHandleListInvalidArgs(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not a map"
	result, err := s.handleList(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// handleList may be lenient — just check it doesn't panic
	_ = result
}

// ── Resource handlers — coverage ───────────────────────────────

func TestHandleIndexListResourceHC(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.ReadResourceRequest{}
	contents, err := s.handleIndexListResource(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contents == nil {
		t.Error("expected non-nil contents")
	}
}

func TestHandleReadResourceBadURIHC(t *testing.T) {
	cfg := Config{IndexDir: t.TempDir()}
	s := NewServer(cfg)
	defer s.Close()

	req := mcpsdk.ReadResourceRequest{}
	req.Params.URI = "gleann://bad_format"
	_, err := s.handleReadResource(context.Background(), req)
	// Should handle gracefully
	_ = err
}
