package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newUnifiedTestServer(t *testing.T) *Server {
	t.Helper()
	s := NewServer(gleann.Config{IndexDir: t.TempDir()}, ":0", "test")
	t.Cleanup(func() {
		s.closeBlockMem()
		s.bgManager.Stop()
	})
	return s
}

func TestUnifiedIngest_Facts(t *testing.T) {
	s := newUnifiedTestServer(t)

	body := `{
		"facts": [
			{"content": "Go is a compiled language", "tags": ["go", "lang"]},
			{"content": "Rust has ownership", "tier": "medium"}
		]
	}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UnifiedIngestResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.FactsStored != 2 {
		t.Errorf("expected 2 facts stored, got %d", resp.FactsStored)
	}
	if len(resp.FactIDs) != 2 {
		t.Errorf("expected 2 fact IDs, got %d", len(resp.FactIDs))
	}
}

func TestUnifiedIngest_ScopedFacts(t *testing.T) {
	s := newUnifiedTestServer(t)

	body := `{
		"scope": "session-123",
		"facts": [{"content": "user prefers dark mode"}]
	}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UnifiedIngestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.FactsStored != 1 {
		t.Errorf("expected 1 fact stored, got %d", resp.FactsStored)
	}
}

func TestUnifiedIngest_EmptyRequest(t *testing.T) {
	s := newUnifiedTestServer(t)

	body := `{}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUnifiedIngest_EmptyFactContent(t *testing.T) {
	s := newUnifiedTestServer(t)

	body := `{"facts": [{"content": ""}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestUnifiedRecall_Blocks(t *testing.T) {
	s := newUnifiedTestServer(t)

	// First ingest some facts.
	ingestBody := `{"facts": [{"content": "Go supports goroutines", "tags": ["concurrency"]}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(ingestBody))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	// Now recall.
	recallBody := `{"query": "goroutines", "layers": ["blocks"]}`
	req = httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(recallBody))
	w = httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UnifiedRecallResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Blocks) == 0 {
		t.Error("expected at least one block result")
	}
	if resp.Graph != nil {
		t.Error("expected nil graph when only blocks layer requested")
	}
}

func TestUnifiedRecall_EmptyQuery(t *testing.T) {
	s := newUnifiedTestServer(t)

	body := `{"query": ""}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestUnifiedRecall_ContextFormat(t *testing.T) {
	s := newUnifiedTestServer(t)

	// Ingest a fact.
	ingestBody := `{"facts": [{"content": "Python is dynamically typed"}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(ingestBody))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	// Recall with context format.
	recallBody := `{"query": "Python", "layers": ["blocks"], "format": "context"}`
	req = httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(recallBody))
	w = httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	var resp UnifiedRecallResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Context == "" {
		t.Error("expected context string in response")
	}
	if !contains(resp.Context, "<memory_context>") {
		t.Error("expected memory_context XML wrapper")
	}
}

func TestUnifiedRecall_AllLayersDefault(t *testing.T) {
	s := newUnifiedTestServer(t)

	// When no layers specified, all layers are queried (blocks, graph, vector).
	// Graph and vector return nil (no index available), blocks return empty.
	recallBody := `{"query": "test"}`
	req := httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(recallBody))
	w := httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestBuildRecallContext(t *testing.T) {
	resp := UnifiedRecallResponse{
		Query: "test",
		Blocks: []RecallBlock{
			{Tier: "short", Content: "fact one"},
		},
		Vector: []RecallHit{
			{Content: "document text", Source: "doc.md", Score: 0.95},
		},
	}

	ctx := buildRecallContext(resp)

	if !contains(ctx, "<facts>") {
		t.Error("expected facts section")
	}
	if !contains(ctx, "<relevant_documents>") {
		t.Error("expected documents section")
	}
	if !contains(ctx, "fact one") {
		t.Error("expected fact content")
	}
}

func TestUnifiedIngest_WithMetadata(t *testing.T) {
	s := newUnifiedTestServer(t)

	body := `{
		"facts": [{
			"content": "auth uses JWT tokens",
			"metadata": {"source_file": "auth.go", "confidence": "high"},
			"tags": ["auth", "security"],
			"char_limit": 500
		}]
	}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UnifiedIngestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.FactsStored != 1 {
		t.Errorf("expected 1 fact, got %d", resp.FactsStored)
	}

	// Recall and verify metadata is returned.
	recallBody := `{"query": "JWT", "layers": ["blocks"]}`
	req = httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(recallBody))
	w = httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	var recallResp UnifiedRecallResponse
	json.NewDecoder(w.Body).Decode(&recallResp)
	if len(recallResp.Blocks) == 0 {
		t.Fatal("expected block result")
	}
	if recallResp.Blocks[0].Metadata["source_file"] != "auth.go" {
		t.Errorf("expected metadata source_file=auth.go, got %v", recallResp.Blocks[0].Metadata)
	}
	if recallResp.Blocks[0].CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt timestamp")
	}
}

func TestUnifiedIngest_WithTTL(t *testing.T) {
	s := newUnifiedTestServer(t)

	body := `{
		"facts": [{"content": "temporary fact", "expires_in": "1h"}]
	}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp UnifiedIngestResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.FactsStored != 1 {
		t.Errorf("expected 1, got %d", resp.FactsStored)
	}
}

func TestUnifiedRecall_TagFilter(t *testing.T) {
	s := newUnifiedTestServer(t)

	// Ingest facts with different tags.
	ingestBody := `{
		"facts": [
			{"content": "Go uses goroutines for concurrency", "tags": ["go", "concurrency"]},
			{"content": "Go compiles to native code", "tags": ["go", "compiler"]},
			{"content": "Rust is memory safe", "tags": ["rust", "safety"]}
		]
	}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(ingestBody))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	// Recall with tag filter — only "go" tagged.
	recallBody := `{"query": "memory", "layers": ["blocks"], "tags": ["go"]}`
	req = httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(recallBody))
	w = httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	var resp UnifiedRecallResponse
	json.NewDecoder(w.Body).Decode(&resp)
	// Should only return Go-tagged facts, not the Rust one.
	for _, b := range resp.Blocks {
		hasGoTag := false
		for _, tag := range b.Tags {
			if tag == "go" {
				hasGoTag = true
			}
		}
		if !hasGoTag {
			t.Errorf("block %q should have 'go' tag but has %v", b.Content, b.Tags)
		}
	}
}

func TestUnifiedRecall_TierFilter(t *testing.T) {
	s := newUnifiedTestServer(t)

	ingestBody := `{
		"facts": [
			{"content": "short fact", "tier": "short"},
			{"content": "long fact", "tier": "long"}
		]
	}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(ingestBody))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	// Filter by long tier only.
	recallBody := `{"query": "fact", "layers": ["blocks"], "tier": "long"}`
	req = httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(recallBody))
	w = httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	var resp UnifiedRecallResponse
	json.NewDecoder(w.Body).Decode(&resp)
	for _, b := range resp.Blocks {
		if b.Tier != "long" {
			t.Errorf("expected tier=long, got %s", b.Tier)
		}
	}
}

func TestUnifiedRecall_DateFilter(t *testing.T) {
	s := newUnifiedTestServer(t)

	ingestBody := `{"facts": [{"content": "recent fact from today"}]}`
	req := httptest.NewRequest("POST", "/api/memory/ingest", bytes.NewBufferString(ingestBody))
	w := httptest.NewRecorder()
	s.handleUnifiedIngest(w, req)

	// Filter: only facts from last 1 hour.
	recallBody := `{"query": "recent", "layers": ["blocks"], "after": "1h"}`
	req = httptest.NewRequest("POST", "/api/memory/recall", bytes.NewBufferString(recallBody))
	w = httptest.NewRecorder()
	s.handleUnifiedRecall(w, req)

	var resp UnifiedRecallResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Blocks) == 0 {
		t.Error("expected recent fact to be found with after=1h filter")
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"24h", "24h0m0s"},
		{"7d", "168h0m0s"},
		{"1w", "168h0m0s"},
		{"30m", "30m0s"},
	}
	for _, tt := range tests {
		d, err := parseDuration(tt.input)
		if err != nil {
			t.Errorf("parseDuration(%q) error: %v", tt.input, err)
			continue
		}
		if d.String() != tt.want {
			t.Errorf("parseDuration(%q) = %s, want %s", tt.input, d, tt.want)
		}
	}
}

func TestContainsAllTags(t *testing.T) {
	if !containsAllTags([]string{"go", "rust", "python"}, []string{"go", "rust"}) {
		t.Error("should match when all required tags present")
	}
	if containsAllTags([]string{"go", "rust"}, []string{"go", "python"}) {
		t.Error("should not match when required tag missing")
	}
	if !containsAllTags([]string{"Go", "RUST"}, []string{"go", "rust"}) {
		t.Error("should match case-insensitively")
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
