package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

func newBatchTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(Config{
		IndexDir:          t.TempDir(),
		EmbeddingProvider: "ollama",
		EmbeddingModel:    "bge-m3",
		OllamaHost:        gleann.DefaultOllamaHost,
		Version:           "test",
	})
}

// --- buildBatchAskTool schema tests ---

func TestBuildBatchAskTool_Name(t *testing.T) {
	srv := newBatchTestServer(t)
	tool := srv.buildBatchAskTool()
	if tool.Name != "gleann_batch_ask" {
		t.Fatalf("expected tool name gleann_batch_ask, got %q", tool.Name)
	}
}

func TestBuildBatchAskTool_RequiredFields(t *testing.T) {
	srv := newBatchTestServer(t)
	tool := srv.buildBatchAskTool()
	required := tool.InputSchema.Required
	wantRequired := map[string]bool{"questions": true, "index": true}
	for _, field := range required {
		if !wantRequired[field] {
			t.Errorf("unexpected required field: %q", field)
		}
		delete(wantRequired, field)
	}
	for field := range wantRequired {
		t.Errorf("missing required field: %q", field)
	}
}

func TestBuildBatchAskTool_Properties(t *testing.T) {
	srv := newBatchTestServer(t)
	tool := srv.buildBatchAskTool()
	props := tool.InputSchema.Properties
	for _, key := range []string{"questions", "index", "top_k", "concurrency"} {
		if _, ok := props[key]; !ok {
			t.Errorf("expected property %q in schema", key)
		}
	}
}

// --- handleBatchAsk argument validation tests ---

func TestHandleBatchAsk_InvalidArguments(t *testing.T) {
	srv := newBatchTestServer(t)
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = "not-a-map"

	result, err := srv.handleBatchAsk(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for invalid arguments")
	}
}

func TestHandleBatchAsk_MissingQuestions(t *testing.T) {
	srv := newBatchTestServer(t)
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"index": "myindex",
	}

	result, err := srv.handleBatchAsk(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result when questions field is missing")
	}
}

func TestHandleBatchAsk_EmptyQuestionsArray(t *testing.T) {
	srv := newBatchTestServer(t)
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"questions": []interface{}{},
		"index":     "myindex",
	}

	result, err := srv.handleBatchAsk(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for empty questions array")
	}
}

func TestHandleBatchAsk_MissingIndex(t *testing.T) {
	srv := newBatchTestServer(t)
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"questions": []interface{}{"what is this?"},
		// index is missing
	}

	result, err := srv.handleBatchAsk(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result when index is missing")
	}
}

func TestHandleBatchAsk_IndexNotFound(t *testing.T) {
	srv := newBatchTestServer(t)
	req := mcpsdk.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"questions": []interface{}{"hello?"},
		"index":     "nonexistent-index",
	}

	result, err := srv.handleBatchAsk(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// index not found → error result with message
	if !result.IsError {
		t.Fatal("expected error result for missing index")
	}
}

func TestHandleBatchAsk_LimitsToMaxQuestions(t *testing.T) {
	// 12 questions exceeds batchMaxQuestions(10): tool should silently cap at 10.
	// We can't easily call the full tool end-to-end without an index,
	// so we just verify the tool schema advertises maxItems correctly.
	srv := newBatchTestServer(t)
	tool := srv.buildBatchAskTool()
	qProp, ok := tool.InputSchema.Properties["questions"].(map[string]interface{})
	if !ok {
		t.Fatal("questions property missing or wrong type")
	}
	if qProp["maxItems"] != batchMaxQuestions {
		t.Fatalf("expected maxItems=%d, got %v", batchMaxQuestions, qProp["maxItems"])
	}
}
