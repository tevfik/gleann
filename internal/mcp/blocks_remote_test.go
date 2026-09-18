package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/memory"
)

func TestMCPMemoryRemoteRouting(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("POST /api/blocks", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(memory.Block{
			ID:      "rem-12345678",
			Content: req["content"].(string),
			Tier:    memory.TierLong,
			Label:   "test_label",
		})
	})

	mux.HandleFunc("DELETE /api/blocks/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": 1})
	})

	mux.HandleFunc("GET /api/blocks/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		blocks := []memory.Block{
			{
				ID:      "search-1",
				Content: "Found item for " + q,
				Tier:    memory.TierLong,
				Tags:    []string{"remote", "tag"},
			},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"blocks": blocks,
			"count":  len(blocks),
		})
	})

	mux.HandleFunc("GET /api/blocks", func(w http.ResponseWriter, r *http.Request) {
		blocks := []memory.Block{
			{
				ID:      "list-1",
				Content: "Listed item",
				Tier:    memory.TierLong,
				Label:   "note",
				Tags:    []string{"test"},
			},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"blocks": blocks,
			"count":  len(blocks),
		})
	})

	mux.HandleFunc("GET /api/blocks/context", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte("<memory_context><block>remote context</block></memory_context>"))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	os.Setenv("GLEANN_REMOTE_ADDR", ts.URL)
	defer os.Unsetenv("GLEANN_REMOTE_ADDR")
	defer memory.ResetRemoteForTesting()
	memory.ResetRemoteForTesting()

	srv := &Server{
		blockMem: &blockMemPool{},
	}

	ctx := context.Background()

	// 1. Test handleMemoryRemember
	var remReq mcpsdk.CallToolRequest
	remReq.Params.Arguments = map[string]any{
		"content": "Remember via remote REST",
		"tier":    "long",
	}
	res, err := srv.handleMemoryRemember(ctx, remReq)
	if err != nil {
		t.Fatalf("handleMemoryRemember unexpected err: %v", err)
	}
	if res.IsError {
		t.Fatalf("handleMemoryRemember returned tool error: %+v", res)
	}
	text := res.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "Remembered (ID: rem-12345678") {
		t.Errorf("unexpected remember result: %s", text)
	}

	// 2. Test handleMemoryForget
	var forReq mcpsdk.CallToolRequest
	forReq.Params.Arguments = map[string]any{
		"id_or_query": "rem-12345678",
	}
	res, err = srv.handleMemoryForget(ctx, forReq)
	if err != nil {
		t.Fatalf("handleMemoryForget unexpected err: %v", err)
	}
	text = res.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "Forgot 1 block(s)") {
		t.Errorf("unexpected forget result: %s", text)
	}

	// 3. Test handleMemorySearch
	var searchReq mcpsdk.CallToolRequest
	searchReq.Params.Arguments = map[string]any{
		"query": "hello",
	}
	res, err = srv.handleMemorySearch(ctx, searchReq)
	if err != nil {
		t.Fatalf("handleMemorySearch unexpected err: %v", err)
	}
	text = res.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "Found 1 memory block(s)") || !strings.Contains(text, "search-1") {
		t.Errorf("unexpected search result: %s", text)
	}

	// 4. Test handleMemoryList
	var listReq mcpsdk.CallToolRequest
	listReq.Params.Arguments = map[string]any{
		"tier": "long",
	}
	res, err = srv.handleMemoryList(ctx, listReq)
	if err != nil {
		t.Fatalf("handleMemoryList unexpected err: %v", err)
	}
	text = res.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "1 memory block(s) in long-term tier") || !strings.Contains(text, "list-1") {
		t.Errorf("unexpected list result: %s", text)
	}

	// 5. Test handleMemoryContext
	var contextReq mcpsdk.CallToolRequest
	res, err = srv.handleMemoryContext(ctx, contextReq)
	if err != nil {
		t.Fatalf("handleMemoryContext unexpected err: %v", err)
	}
	text = res.Content[0].(mcpsdk.TextContent).Text
	if !strings.Contains(text, "<memory_context><block>remote context</block></memory_context>") {
		t.Errorf("unexpected context result: %s", text)
	}
}
