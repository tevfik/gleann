package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
	"github.com/tevfik/gleann/pkg/memory"
)

// newBlocksTestServer builds a minimal Server with a real BBolt memory manager
// backed by a temp directory.  All /api/blocks/* routes are registered.
func newBlocksTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	dir := t.TempDir()

	storePath := dir + "/memory.db"
	store, err := memory.OpenStore(storePath)
	if err != nil {
		t.Fatalf("open test memory store: %v", err)
	}
	mgr := memory.NewManager(store)

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir

	s := &Server{
		config:    cfg,
		searchers: make(map[string]*gleann.LeannSearcher),
		version:   "test",
		blockMem:  mgr,
	}
	t.Cleanup(func() { _ = mgr.Close() })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/blocks/search", s.handleSearchBlocks)
	mux.HandleFunc("GET /api/blocks/context", s.handleBlockContext)
	mux.HandleFunc("GET /api/blocks/stats", s.handleBlockStats)
	mux.HandleFunc("GET /api/blocks", s.handleListBlocks)
	mux.HandleFunc("POST /api/blocks", s.handleAddBlock)
	mux.HandleFunc("DELETE /api/blocks/{id}", s.handleDeleteBlock)
	mux.HandleFunc("DELETE /api/blocks", s.handleClearBlocks)
	return s, mux
}

// doBlockAdd posts a blockAddRequest to POST /api/blocks.
func doBlockAdd(t *testing.T, mux *http.ServeMux, req blockAddRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/api/blocks", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// ── POST /api/blocks ──────────────────────────────────────────────────────────

func TestHandleAddBlock_RequiresContent(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	w := doBlockAdd(t, mux, blockAddRequest{})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleAddBlock_DefaultsToLongTerm(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	w := doBlockAdd(t, mux, blockAddRequest{Content: "test fact"})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var block memory.Block
	if err := json.NewDecoder(w.Body).Decode(&block); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if block.Tier != memory.TierLong {
		t.Errorf("tier = %q, want %q", block.Tier, memory.TierLong)
	}
	if block.ID == "" {
		t.Error("expected non-empty ID")
	}
}

func TestHandleAddBlock_MediumTier(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	w := doBlockAdd(t, mux, blockAddRequest{
		Content: "daily summary",
		Tier:    "medium",
		Label:   "daily_note",
		Tags:    []string{"daily", "summary"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var block memory.Block
	json.NewDecoder(w.Body).Decode(&block)
	if block.Tier != memory.TierMedium {
		t.Errorf("tier = %q, want %q", block.Tier, memory.TierMedium)
	}
	if block.Label != "daily_note" {
		t.Errorf("label = %q, want daily_note", block.Label)
	}
}

func TestHandleAddBlock_InvalidTier(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	w := doBlockAdd(t, mux, blockAddRequest{Content: "x", Tier: "bogus"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleAddBlock_WithExpiry(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	w := doBlockAdd(t, mux, blockAddRequest{
		Content:   "temp fact",
		ExpiresIn: "1h",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var block memory.Block
	json.NewDecoder(w.Body).Decode(&block)
	if block.ExpiresAt == nil {
		t.Error("expected ExpiresAt to be set")
	}
}

// ── GET /api/blocks ───────────────────────────────────────────────────────────

func TestHandleListBlocks_Empty(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/blocks", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 0 {
		t.Errorf("count = %v, want 0", resp["count"])
	}
}

func TestHandleListBlocks_WithBlocks(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "fact one", Tier: "long"})
	doBlockAdd(t, mux, blockAddRequest{Content: "fact two", Tier: "long"})

	r := httptest.NewRequest(http.MethodGet, "/api/blocks", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2", resp["count"])
	}
}

func TestHandleListBlocks_TierFilter(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "long fact", Tier: "long"})
	doBlockAdd(t, mux, blockAddRequest{Content: "medium fact", Tier: "medium"})

	// Filter to medium only.
	r := httptest.NewRequest(http.MethodGet, "/api/blocks?tier=medium", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("count = %v, want 1", resp["count"])
	}
}

func TestHandleListBlocks_InvalidTier(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/blocks?tier=bogus", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ── DELETE /api/blocks/{id} ───────────────────────────────────────────────────

func TestHandleDeleteBlock_Success(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	addW := doBlockAdd(t, mux, blockAddRequest{Content: "to delete"})
	var block memory.Block
	json.NewDecoder(addW.Body).Decode(&block)

	r := httptest.NewRequest(http.MethodDelete, "/api/blocks/"+block.ID, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
}

func TestHandleDeleteBlock_NotFound(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	r := httptest.NewRequest(http.MethodDelete, "/api/blocks/nonexistent-id-xyz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ── DELETE /api/blocks ────────────────────────────────────────────────────────

func TestHandleClearBlocks_All(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "a", Tier: "long"})
	doBlockAdd(t, mux, blockAddRequest{Content: "b", Tier: "medium"})

	r := httptest.NewRequest(http.MethodDelete, "/api/blocks", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
	// 2 blocks should be cleared
	if resp["cleared"].(float64) < 2 {
		t.Errorf("cleared = %v, want >= 2", resp["cleared"])
	}
}

func TestHandleClearBlocks_ByTier(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "long1", Tier: "long"})
	doBlockAdd(t, mux, blockAddRequest{Content: "medium1", Tier: "medium"})

	r := httptest.NewRequest(http.MethodDelete, "/api/blocks?tier=long", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Remaining: only medium, so long-tier list should now be empty.
	lr := httptest.NewRequest(http.MethodGet, "/api/blocks?tier=long", nil)
	lw := httptest.NewRecorder()
	mux.ServeHTTP(lw, lr)
	var listResp map[string]any
	json.NewDecoder(lw.Body).Decode(&listResp)
	if listResp["count"].(float64) != 0 {
		t.Errorf("long-term count after clear = %v, want 0", listResp["count"])
	}
}

// ── GET /api/blocks/search ────────────────────────────────────────────────────

func TestHandleSearchBlocks_RequiresQ(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/blocks/search", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSearchBlocks_FindsMatch(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "user prefers dark mode"})
	doBlockAdd(t, mux, blockAddRequest{Content: "project uses Go 1.24"})

	r := httptest.NewRequest(http.MethodGet, "/api/blocks/search?q=dark+mode", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) < 1 {
		t.Errorf("count = %v, want >= 1", resp["count"])
	}
}

// ── GET /api/blocks/context ───────────────────────────────────────────────────

func TestHandleBlockContext_Empty(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "/api/blocks/context", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestHandleBlockContext_WithBlocks(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "I use Neovim"})

	r := httptest.NewRequest(http.MethodGet, "/api/blocks/context", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	rendered, _ := resp["rendered"].(string)
	if rendered == "" {
		t.Error("expected non-empty rendered context")
	}
}

func TestHandleBlockContext_XMLFormat(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "test xml"})

	r := httptest.NewRequest(http.MethodGet, "/api/blocks/context?format=xml", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct == "" || (ct != "text/xml; charset=utf-8" && ct[:8] != "text/xml") {
		t.Errorf("content-type = %q, want text/xml", ct)
	}
}

// ── GET /api/blocks/stats ─────────────────────────────────────────────────────

func TestHandleBlockStats(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "stats test"})

	r := httptest.NewRequest(http.MethodGet, "/api/blocks/stats", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var stats memory.Stats
	if err := json.NewDecoder(w.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if stats.TotalCount < 1 {
		t.Errorf("total_count = %d, want >= 1", stats.TotalCount)
	}
}

// ── Scope and CharLimit Handler Tests ─────────────────────────────────────────

func TestHandleAddBlock_WithScope(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	w := doBlockAdd(t, mux, blockAddRequest{
		Content: "scoped fact",
		Scope:   "conv-abc",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var block memory.Block
	json.NewDecoder(w.Body).Decode(&block)
	if block.Scope != "conv-abc" {
		t.Errorf("scope = %q, want conv-abc", block.Scope)
	}
}

func TestHandleAddBlock_WithCharLimit(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	w := doBlockAdd(t, mux, blockAddRequest{
		Content:   "test",
		CharLimit: 500,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var block memory.Block
	json.NewDecoder(w.Body).Decode(&block)
	if block.CharLimit != 500 {
		t.Errorf("char_limit = %d, want 500", block.CharLimit)
	}
}

func TestHandleListBlocks_ScopeFilter(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "global fact"})
	doBlockAdd(t, mux, blockAddRequest{Content: "conv-1 fact", Scope: "conv-1"})
	doBlockAdd(t, mux, blockAddRequest{Content: "conv-2 fact", Scope: "conv-2"})

	// Filter by scope=conv-1 → should get global + conv-1 = 2.
	r := httptest.NewRequest(http.MethodGet, "/api/blocks?scope=conv-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 2 {
		t.Errorf("scope=conv-1 count = %v, want 2", resp["count"])
	}
}

func TestHandleSearchBlocks_ScopeFilter(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "dark mode global"})
	doBlockAdd(t, mux, blockAddRequest{Content: "dark theme conv1", Scope: "conv-1"})
	doBlockAdd(t, mux, blockAddRequest{Content: "dark bg conv2", Scope: "conv-2"})

	// Scope=conv-1, q=dark → global match + conv-1 match = 2.
	r := httptest.NewRequest(http.MethodGet, "/api/blocks/search?q=dark&scope=conv-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 2 {
		t.Errorf("scoped search count = %v, want 2", resp["count"])
	}
}

func TestHandleBlockContext_ScopeFilter(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "global memory"})
	doBlockAdd(t, mux, blockAddRequest{Content: "scoped A memory", Scope: "scope-A"})
	doBlockAdd(t, mux, blockAddRequest{Content: "scoped B memory", Scope: "scope-B"})

	r := httptest.NewRequest(http.MethodGet, "/api/blocks/context?scope=scope-A", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	rendered, _ := resp["rendered"].(string)
	if rendered == "" {
		t.Error("expected non-empty rendered context")
	}
}
