//go:build treesitter

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// newMemoryTestServer builds a minimal Server with a real memoryPool backed by
// a temp directory.  It registers all four Memory Engine routes on a fresh mux.
func newMemoryTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	dir := t.TempDir()

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir

	s := &Server{
		config:     cfg,
		searchers:  make(map[string]*gleann.LeannSearcher),
		version:    "test",
		memoryPool: newMemoryPool(dir),
	}
	t.Cleanup(func() { s.memoryPool.closeAll() })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/memory/{name}/inject", s.handleMemoryInject)
	mux.HandleFunc("DELETE /api/memory/{name}/nodes/{id}", s.handleMemoryDeleteNode)
	mux.HandleFunc("DELETE /api/memory/{name}/edges", s.handleMemoryDeleteEdge)
	mux.HandleFunc("POST /api/memory/{name}/traverse", s.handleMemoryTraverse)
	return s, mux
}

// doInject posts a GraphInjectionPayload to /api/memory/{name}/inject.
func doInject(t *testing.T, mux *http.ServeMux, name string, payload gleann.GraphInjectionPayload) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/memory/"+name+"/inject", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// ── POST /api/memory/{name}/inject ───────────────────────────────────────────

func TestHandleMemoryInject_EmptyPayload(t *testing.T) {
	_, mux := newMemoryTestServer(t)
	payload := gleann.GraphInjectionPayload{}
	w := doInject(t, mux, "idx", payload)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
	if resp["nodes_sent"].(float64) != 0 {
		t.Errorf("nodes_sent = %v, want 0", resp["nodes_sent"])
	}
}

func TestHandleMemoryInject_WithNodes(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "req-1", Type: "requirement", Content: "User can register"},
			{ID: "req-2", Type: "requirement", Content: "User can log in"},
		},
	}
	w := doInject(t, mux, "myidx", payload)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["nodes_sent"].(float64) != 2 {
		t.Errorf("nodes_sent = %v, want 2", resp["nodes_sent"])
	}
}

func TestHandleMemoryInject_WithNodesAndEdges(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "n-a", Type: "concept"},
			{ID: "n-b", Type: "concept"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "n-a", To: "n-b", RelationType: "RELATED_TO", Weight: 1},
		},
	}
	w := doInject(t, mux, "myidx", payload)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["edges_sent"].(float64) != 1 {
		t.Errorf("edges_sent = %v, want 1", resp["edges_sent"])
	}
}

func TestHandleMemoryInject_InvalidJSON(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/memory/idx/inject", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleMemoryInject_Idempotent(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	payload := gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "idem-1", Type: "item"}},
	}

	// Two identical injections must both succeed.
	for i := 0; i < 2; i++ {
		w := doInject(t, mux, "idx", payload)
		if w.Code != http.StatusOK {
			t.Errorf("call %d: expected 200, got %d", i+1, w.Code)
		}
	}
}

func TestHandleMemoryInject_MissingNameUsesPathValue(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	// Route "/" does not exist in our mux — 404 expected, not a 400 from handler.
	payload := gleann.GraphInjectionPayload{}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/memory/", bytes.NewReader(b))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// 404 because path doesn't match pattern (trailing slash not bound to {name}).
	if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
		t.Errorf("expected 404 or 400, got %d", w.Code)
	}
}

// ── DELETE /api/memory/{name}/nodes/{id} ──────────────────────────────────────

func TestHandleMemoryDeleteNode_AfterInject(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	// Inject first.
	doInject(t, mux, "idx", gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "to-delete", Type: "item"}},
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/memory/idx/nodes/to-delete", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
	if resp["deleted_id"] != "to-delete" {
		t.Errorf("deleted_id = %v, want to-delete", resp["deleted_id"])
	}
}

func TestHandleMemoryDeleteNode_NonExistent_StillOK(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/memory/idx/nodes/ghost", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (idempotent), got %d", w.Code)
	}
}

// ── DELETE /api/memory/{name}/edges ──────────────────────────────────────────

func doDeleteEdge(t *testing.T, mux *http.ServeMux, name string, req DeleteEdgeRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodDelete, "/api/memory/"+name+"/edges", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestHandleMemoryDeleteEdge_AfterInject(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	doInject(t, mux, "idx", gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "a", Type: "item"},
			{ID: "b", Type: "item"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "a", To: "b", RelationType: "LINK", Weight: 1},
		},
	})

	w := doDeleteEdge(t, mux, "idx", DeleteEdgeRequest{From: "a", To: "b", RelationType: "LINK"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("ok = %v, want true", resp["ok"])
	}
}

func TestHandleMemoryDeleteEdge_InvalidJSON(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	r := httptest.NewRequest(http.MethodDelete, "/api/memory/idx/edges", bytes.NewReader([]byte("bad json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleMemoryDeleteEdge_MissingFields(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	// Missing relation_type.
	b, _ := json.Marshal(map[string]string{"from": "a", "to": "b"})
	r := httptest.NewRequest(http.MethodDelete, "/api/memory/idx/edges", bytes.NewReader(b))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleMemoryDeleteEdge_NonExistent_StillOK(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	w := doDeleteEdge(t, mux, "idx", DeleteEdgeRequest{From: "x", To: "y", RelationType: "PHANTOM"})
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (idempotent), got %d", w.Code)
	}
}

// ── POST /api/memory/{name}/traverse ─────────────────────────────────────────

func doTraverse(t *testing.T, mux *http.ServeMux, name string, req TraverseRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/api/memory/"+name+"/traverse", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestHandleMemoryTraverse_EmptyGraph_ReturnsEmptySlices(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	w := doTraverse(t, mux, "idx", TraverseRequest{StartID: "nonexistent", Depth: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TraverseResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// Ensure we always get arrays (not null) in the JSON.
	if resp.Nodes == nil {
		t.Error("nodes should be an empty array, not null")
	}
	if resp.Edges == nil {
		t.Error("edges should be an empty array, not null")
	}
}

func TestHandleMemoryTraverse_SingleNode(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	doInject(t, mux, "idx", gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "solo", Type: "concept", Content: "lonely node"}},
	})

	w := doTraverse(t, mux, "idx", TraverseRequest{StartID: "solo", Depth: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TraverseResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Count < 1 {
		t.Errorf("count = %d, want >= 1", resp.Count)
	}
	if len(resp.Nodes) < 1 {
		t.Errorf("expected at least 1 node, got %d", len(resp.Nodes))
	}
}

func TestHandleMemoryTraverse_MultiNodeGraph(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	doInject(t, mux, "idx", gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "root", Type: "req"},
			{ID: "child1", Type: "code"},
			{ID: "child2", Type: "code"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "root", To: "child1", RelationType: "IMPL", Weight: 1},
			{From: "root", To: "child2", RelationType: "IMPL", Weight: 1},
		},
	})

	w := doTraverse(t, mux, "idx", TraverseRequest{StartID: "root", Depth: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp TraverseResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Count < 3 {
		t.Errorf("expected >= 3 nodes (root + 2 children), got %d", resp.Count)
	}
	if len(resp.Edges) < 2 {
		t.Errorf("expected >= 2 edges, got %d", len(resp.Edges))
	}
}

func TestHandleMemoryTraverse_InvalidJSON(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	r := httptest.NewRequest(http.MethodPost, "/api/memory/idx/traverse", bytes.NewReader([]byte("bad json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleMemoryTraverse_MissingStartID(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	w := doTraverse(t, mux, "idx", TraverseRequest{StartID: "", Depth: 1})
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing start_id, got %d", w.Code)
	}
}

func TestHandleMemoryTraverse_DefaultDepth(t *testing.T) {
	_, mux := newMemoryTestServer(t)

	doInject(t, mux, "idx", gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "d-root", Type: "item"}},
	})

	// depth 0 should be treated as default (1).
	w := doTraverse(t, mux, "idx", TraverseRequest{StartID: "d-root", Depth: 0})
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ── Integration: inject → traverse → delete ───────────────────────────────────

func TestMemoryEngine_FullLifecycle(t *testing.T) {
	_, mux := newMemoryTestServer(t)
	const idx = "lifecycle"

	// 1. Inject a small knowledge graph.
	doInject(t, mux, idx, gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{
			{ID: "feat-login", Type: "feature", Content: "Login feature"},
			{ID: "impl-jwt", Type: "code", Content: "JWT implementation"},
			{ID: "impl-db", Type: "code", Content: "DB session store"},
		},
		Edges: []gleann.MemoryGraphEdge{
			{From: "feat-login", To: "impl-jwt", RelationType: "IMPLEMENTED_BY", Weight: 1},
			{From: "feat-login", To: "impl-db", RelationType: "IMPLEMENTED_BY", Weight: 1},
		},
	})

	// 2. Traverse from the feature node — expect all 3 nodes.
	w := doTraverse(t, mux, idx, TraverseRequest{StartID: "feat-login", Depth: 1})
	if w.Code != http.StatusOK {
		t.Fatalf("traverse: expected 200, got %d", w.Code)
	}
	var resp TraverseResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Count < 3 {
		t.Errorf("traverse: expected >= 3 nodes, got %d", resp.Count)
	}

	// 3. Remove one edge.
	doDeleteEdge(t, mux, idx, DeleteEdgeRequest{From: "feat-login", To: "impl-db", RelationType: "IMPLEMENTED_BY"})

	// 4. Re-traverse — impl-db is still a node but no longer reachable via IMPLEMENTED_BY from root.
	w2 := doTraverse(t, mux, idx, TraverseRequest{StartID: "feat-login", Depth: 1})
	json.NewDecoder(w2.Body).Decode(&resp)

	for _, e := range resp.Edges {
		if e.To == "impl-db" {
			t.Error("impl-db should no longer be reachable via an IMPLEMENTED_BY edge")
		}
	}

	// 5. Delete the feature node.
	req := httptest.NewRequest(http.MethodDelete, "/api/memory/"+idx+"/nodes/feat-login", nil)
	wDel := httptest.NewRecorder()
	mux.ServeHTTP(wDel, req)
	if wDel.Code != http.StatusOK {
		t.Fatalf("delete node: expected 200, got %d", wDel.Code)
	}

	// 6. After deleting root, traversal from feat-login should be empty.
	w3 := doTraverse(t, mux, idx, TraverseRequest{StartID: "feat-login", Depth: 2})
	json.NewDecoder(w3.Body).Decode(&resp)
	if resp.Count != 0 {
		t.Errorf("after delete root, traverse should return 0 nodes, got %d", resp.Count)
	}
}

// ── memoryPool ────────────────────────────────────────────────────────────────

func TestMemoryPool_GetReusesConnection(t *testing.T) {
	dir := t.TempDir()
	pool := newMemoryPool(dir)
	t.Cleanup(pool.closeAll)

	svc1, err := pool.get("test")
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	svc2, err := pool.get("test")
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	if svc1 != svc2 {
		t.Error("pool should return the same MemoryService on repeated calls")
	}
}

func TestMemoryPool_IndependentIndexes(t *testing.T) {
	dir := t.TempDir()
	pool := newMemoryPool(dir)
	t.Cleanup(pool.closeAll)

	svcA, err := pool.get("alpha")
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	svcB, err := pool.get("beta")
	if err != nil {
		t.Fatalf("get beta: %v", err)
	}
	if svcA == svcB {
		t.Error("different index names should produce different MemoryService instances")
	}
}

func TestMemoryPool_CloseAll(t *testing.T) {
	dir := t.TempDir()
	pool := newMemoryPool(dir)

	if _, err := pool.get("one"); err != nil {
		t.Fatalf("get: %v", err)
	}
	// closeAll should not panic even when called multiple times.
	pool.closeAll()
	pool.closeAll()
}
