package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryFailureToolSchemas(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	add := s.buildMemoryFailureAddTool()
	if add.Name != "memory_failure_add" {
		t.Errorf("name = %q, want memory_failure_add", add.Name)
	}
	required := map[string]bool{}
	for _, r := range add.InputSchema.Required {
		required[r] = true
	}
	if !required["command"] || !required["error"] {
		t.Errorf("memory_failure_add should require command and error, got %v", add.InputSchema.Required)
	}

	list := s.buildMemoryFailureListTool()
	if list.Name != "memory_failure_list" {
		t.Errorf("name = %q, want memory_failure_list", list.Name)
	}

	// memory_context advertises the task-scoped arguments.
	ctx := s.buildMemoryContextTool()
	if _, ok := ctx.InputSchema.Properties["command"]; !ok {
		t.Error("memory_context schema should include 'command'")
	}
	if _, ok := ctx.InputSchema.Properties["paths"]; !ok {
		t.Error("memory_context schema should include 'paths'")
	}
}

func TestHandleMemoryFailureAddAndList(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	ctx := context.Background()

	res, err := s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go test ./pkg/memory",
		"error":   "panic: nil pointer at /a/b/block.go:42",
		"fix":     "nil check",
		"paths":   []any{"pkg/memory/block.go"},
	}))
	if err != nil || res.IsError {
		t.Fatalf("add err=%v result=%+v", err, res)
	}
	if !strings.Contains(extractText(res), "Recorded failure") {
		t.Errorf("expected 'Recorded failure', got %q", extractText(res))
	}

	// A second recording differing only in a path/line number confirms.
	res, err = s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go test ./pkg/memory",
		"error":   "panic: nil pointer at /x/y/block.go:99",
		"fix":     "nil check",
	}))
	if err != nil || res.IsError {
		t.Fatalf("add err=%v result=%+v", err, res)
	}
	if !strings.Contains(extractText(res), "Failure confirmed (2x)") {
		t.Errorf("expected 'Failure confirmed (2x)', got %q", extractText(res))
	}

	// Missing command/error is rejected.
	res, err = s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{"command": "go test"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Error("missing error should be a tool error")
	}

	// List shows the single deduplicated failure.
	res, err = s.handleMemoryFailureList(ctx, mkCallArgs(map[string]any{}))
	if err != nil || res.IsError {
		t.Fatalf("list err=%v result=%+v", err, res)
	}
	text := extractText(res)
	if !strings.Contains(text, "1 recorded failure") {
		t.Errorf("expected 1 recorded failure, got:\n%s", text)
	}
	if !strings.Contains(text, "nil check") {
		t.Errorf("expected the fix in the list, got:\n%s", text)
	}
}

func TestHandleMemoryContextSurfacesFailures(t *testing.T) {
	s := newTestServerWithBlockMem(t)
	defer s.Close()

	ctx := context.Background()

	if _, err := s.handleMemoryFailureAdd(ctx, mkCallArgs(map[string]any{
		"command": "go test ./pkg/memory",
		"error":   "panic: nil pointer at /a/b/block.go:42",
		"fix":     "nil check",
		"paths":   []any{"pkg/memory/block.go"},
	})); err != nil {
		t.Fatal(err)
	}

	// Without command/paths the failure is not surfaced.
	res, err := s.handleMemoryContext(ctx, mkCallArgs(map[string]any{"scope": "all"}))
	if err != nil || res.IsError {
		t.Fatalf("context err=%v result=%+v", err, res)
	}
	if strings.Contains(extractText(res), "<failure_memory>") {
		t.Errorf("failure should not surface without a task, got:\n%s", extractText(res))
	}

	// With the command it surfaces.
	res, err = s.handleMemoryContext(ctx, mkCallArgs(map[string]any{"scope": "all", "command": "go test"}))
	if err != nil || res.IsError {
		t.Fatalf("context err=%v result=%+v", err, res)
	}
	text := extractText(res)
	if !strings.Contains(text, "<failure_memory>") {
		t.Errorf("expected <failure_memory> section, got:\n%s", text)
	}
	if !strings.Contains(text, "nil check") {
		t.Errorf("expected the fix in the surfaced failure, got:\n%s", text)
	}
}
