package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestRegisterPrompts(t *testing.T) {
	cfg := Config{
		IndexDir:          t.TempDir(),
		EmbeddingProvider: "mock",
	}
	server := NewServer(cfg)

	// registerPrompts is called inside NewServer.
	// We just verify they exist via the internal server structure or by triggering them manually.

	// Create mock requests
	req1 := mcp.GetPromptRequest{}
	req1.Params.Name = "gleann-deep-refactor"
	req1.Params.Arguments = map[string]string{
		"index":     "test-index",
		"component": "auth",
	}

	// We expect this to fail gracefully because test-index does not exist
	res, err := server.handleDeepRefactorPrompt(context.Background(), req1)
	if err == nil {
		t.Errorf("Expected error for missing index, got nil")
	}
	if res != nil {
		t.Errorf("Expected nil result on error")
	}

	req2 := mcp.GetPromptRequest{}
	req2.Params.Name = "gleann-bug-hunter"
	req2.Params.Arguments = map[string]string{
		"index":             "test-index",
		"error_description": "panic: nil pointer dereference",
	}

	res2, err := server.handleBugHunterPrompt(context.Background(), req2)
	if err == nil {
		t.Errorf("Expected error for missing index, got nil")
	}
	if res2 != nil {
		t.Errorf("Expected nil result on error")
	}

	// Test missing arguments
	req3 := mcp.GetPromptRequest{}
	req3.Params.Arguments = map[string]string{"index": "test-index"} // missing component
	_, err = server.handleDeepRefactorPrompt(context.Background(), req3)
	if err == nil || err.Error() != "component argument is required" {
		t.Errorf("Expected component argument error, got: %v", err)
	}

	req4 := mcp.GetPromptRequest{}
	req4.Params.Arguments = map[string]string{"index": "test-index"} // missing error_description
	_, err = server.handleBugHunterPrompt(context.Background(), req4)
	if err == nil || err.Error() != "error_description argument is required" {
		t.Errorf("Expected error_description argument error, got: %v", err)
	}
}
