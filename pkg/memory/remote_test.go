package memory_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

func TestRemoteClient(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("GET /api/blocks", func(w http.ResponseWriter, r *http.Request) {
		tier := r.URL.Query().Get("tier")
		blocks := []memory.Block{
			{ID: "block-1", Content: "Note 1", Tier: memory.TierLong},
			{ID: "block-2", Content: "Note 2", Tier: memory.TierMedium},
		}
		if tier == string(memory.TierLong) {
			blocks = blocks[:1]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"blocks": blocks,
			"count":  len(blocks),
		})
	})

	mux.HandleFunc("GET /api/blocks/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		var results []memory.Block
		if q == "test" {
			results = append(results, memory.Block{ID: "block-test", Content: "test content", Tier: memory.TierLong})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"blocks": results,
			"count":  len(results),
		})
	})

	mux.HandleFunc("GET /api/blocks/stats", func(w http.ResponseWriter, r *http.Request) {
		stats := memory.Stats{
			TotalCount:    2,
			LongTermCount: 1,
		}
		_ = json.NewEncoder(w).Encode(stats)
	})

	mux.HandleFunc("GET /api/blocks/context", func(w http.ResponseWriter, r *http.Request) {
		format := r.URL.Query().Get("format")
		if format == "xml" {
			w.Header().Set("Content-Type", "text/xml; charset=utf-8")
			_, _ = w.Write([]byte("<memory_context><note>test</note></memory_context>"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rendered": "<memory_context><note>test</note></memory_context>",
		})
	})

	mux.HandleFunc("POST /api/blocks", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(memory.Block{
			ID:      "block-new",
			Content: req["content"].(string),
			Tier:    memory.TierLong,
		})
	})

	mux.HandleFunc("DELETE /api/blocks/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": 1})
	})

	mux.HandleFunc("DELETE /api/blocks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": 5})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := memory.NewRemoteClient(ts.URL)

	// Test List
	blocks, err := client.List("")
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 blocks, got %d", len(blocks))
	}

	blocksLong, err := client.List(memory.TierLong)
	if err != nil {
		t.Fatalf("List long error: %v", err)
	}
	if len(blocksLong) != 1 {
		t.Errorf("expected 1 block, got %d", len(blocksLong))
	}

	// Test Search
	searchResults, err := client.Search("test")
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(searchResults) != 1 || searchResults[0].ID != "block-test" {
		t.Errorf("expected 1 result with ID block-test, got %+v", searchResults)
	}

	// Test Stats
	stats, err := client.Stats()
	if err != nil {
		t.Fatalf("Stats error: %v", err)
	}
	if stats.TotalCount != 2 {
		t.Errorf("expected TotalCount 2, got %d", stats.TotalCount)
	}

	// Test Context
	ctxXML, err := client.Context("")
	if err != nil {
		t.Fatalf("Context error: %v", err)
	}
	if ctxXML != "<memory_context><note>test</note></memory_context>" {
		t.Errorf("unexpected context XML: %s", ctxXML)
	}

	// Test Add
	added, err := client.Add(memory.TierLong, "note", "New content", []string{"tag1"})
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if added.ID != "block-new" {
		t.Errorf("expected ID block-new, got %s", added.ID)
	}

	// Test AddBlock with ExpiresAt
	exp := time.Now().Add(10 * time.Minute)
	b := &memory.Block{
		Content:   "Expiring content",
		Tier:      memory.TierShort,
		ExpiresAt: &exp,
	}
	addedBlock, err := client.AddBlock(b)
	if err != nil {
		t.Fatalf("AddBlock error: %v", err)
	}
	if addedBlock.ID != "block-new" {
		t.Errorf("expected ID block-new, got %s", addedBlock.ID)
	}

	// Test Forget
	n, err := client.Forget("block-1")
	if err != nil {
		t.Fatalf("Forget error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 deleted, got %d", n)
	}

	// Test Clear
	deleted, err := client.Clear(memory.TierLong)
	if err != nil {
		t.Fatalf("Clear error: %v", err)
	}
	if deleted != 5 {
		t.Errorf("expected 5 deleted, got %d", deleted)
	}

	// Test Remote() probe
	os.Setenv("GLEANN_REMOTE_ADDR", ts.URL)
	defer os.Unsetenv("GLEANN_REMOTE_ADDR")
	memory.ResetRemoteForTesting()

	rc := memory.Remote()
	if rc == nil {
		t.Fatal("expected non-nil remote client from probe")
	}

	// Test disabled remote
	os.Setenv("GLEANN_REMOTE_ADDR", "off")
	memory.ResetRemoteForTesting()
	if memory.Remote() != nil {
		t.Error("expected nil when GLEANN_REMOTE_ADDR=off")
	}
}
