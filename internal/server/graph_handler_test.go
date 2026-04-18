package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// mockGraphDB implements graphDBHandle for testing.
type mockGraphDB struct {
	calleesResult  []GraphNode
	calleesErr     error
	callersResult  []GraphNode
	callersErr     error
	symbolsResult  []GraphNode
	symbolsErr     error
	impactResult   *ImpactResponse
	impactErr      error
	cypherResult   []map[string]any
	cypherErr      error
	fileCount      int
	fileCountErr   error
	symbolCount    int
	symbolCountErr error
	edgeCounts     map[string]int
	edgeCountErr   error
	closed         bool
}

func (m *mockGraphDB) Callees(fqn string) ([]GraphNode, error) { return m.calleesResult, m.calleesErr }
func (m *mockGraphDB) Callers(fqn string) ([]GraphNode, error) { return m.callersResult, m.callersErr }
func (m *mockGraphDB) SymbolsInFile(path string) ([]GraphNode, error) {
	return m.symbolsResult, m.symbolsErr
}
func (m *mockGraphDB) Impact(fqn string, maxDepth int) (*ImpactResponse, error) {
	return m.impactResult, m.impactErr
}
func (m *mockGraphDB) RawCypher(cypher string) ([]map[string]any, error) {
	return m.cypherResult, m.cypherErr
}
func (m *mockGraphDB) FileCount() (int, error)             { return m.fileCount, m.fileCountErr }
func (m *mockGraphDB) SymbolCount() (int, error)           { return m.symbolCount, m.symbolCountErr }
func (m *mockGraphDB) EdgeCount(relType string) (int, error) {
	if m.edgeCountErr != nil {
		return 0, m.edgeCountErr
	}
	return m.edgeCounts[relType], nil
}
func (m *mockGraphDB) Close() { m.closed = true }

func newTestServerWithGraph(db graphDBHandle) *Server {
	s := &Server{
		config:    gleann.Config{IndexDir: "/tmp/test-indexes"},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: newGraphDBPool("/tmp/test-indexes"),
	}
	if db != nil {
		s.graphPool.dbs["test-index"] = db
	}
	return s
}

// ── handleGraphQuery ──────────────────────────────────────────────────────

func TestHandleGraphQuery_MissingName(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("POST", "/api/graph//query", bytes.NewBufferString(`{"query":"callees","symbol":"foo"}`))
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_BadJSON(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(`{bad`))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_NilGraphPool(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: "/tmp"},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: nil,
	}
	body := `{"query":"callees","symbol":"foo"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandleGraphQuery_IndexNotFound(t *testing.T) {
	s := newTestServerWithGraph(nil)
	body := `{"query":"callees","symbol":"main.Foo"}`
	req := httptest.NewRequest("POST", "/api/graph/nonexistent/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Callees(t *testing.T) {
	db := &mockGraphDB{
		calleesResult: []GraphNode{{FQN: "pkg.Bar", Name: "Bar", Kind: "func"}},
	}
	s := newTestServerWithGraph(db)
	body := `{"query":"callees","symbol":"pkg.Foo"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp GraphQueryResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Count != 1 {
		t.Errorf("expected 1 result, got %d", resp.Count)
	}
}

func TestHandleGraphQuery_Callees_MissingSymbol(t *testing.T) {
	db := &mockGraphDB{}
	s := newTestServerWithGraph(db)
	body := `{"query":"callees"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Callers(t *testing.T) {
	db := &mockGraphDB{
		callersResult: []GraphNode{{FQN: "pkg.Baz", Name: "Baz", Kind: "func"}},
	}
	s := newTestServerWithGraph(db)
	body := `{"query":"callers","symbol":"pkg.Foo"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Callers_MissingSymbol(t *testing.T) {
	db := &mockGraphDB{}
	s := newTestServerWithGraph(db)
	body := `{"query":"callers"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_SymbolsInFile(t *testing.T) {
	db := &mockGraphDB{
		symbolsResult: []GraphNode{
			{FQN: "pkg.Foo", Name: "Foo", Kind: "func"},
			{FQN: "pkg.Bar", Name: "Bar", Kind: "struct"},
		},
	}
	s := newTestServerWithGraph(db)
	body := `{"query":"symbols_in_file","file":"main.go"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp GraphQueryResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Count != 2 {
		t.Errorf("expected 2 results, got %d", resp.Count)
	}
}

func TestHandleGraphQuery_SymbolsInFile_MissingFile(t *testing.T) {
	db := &mockGraphDB{}
	s := newTestServerWithGraph(db)
	body := `{"query":"symbols_in_file"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Impact(t *testing.T) {
	db := &mockGraphDB{
		impactResult: &ImpactResponse{
			Symbol:            "pkg.Foo",
			DirectCallers:     []string{"pkg.Bar"},
			TransitiveCallers: []string{"pkg.Baz"},
			AffectedFiles:     []string{"main.go"},
			Depth:             5,
		},
	}
	s := newTestServerWithGraph(db)
	body := `{"query":"impact","symbol":"pkg.Foo"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp ImpactResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.TotalAffected != 2 {
		t.Errorf("expected TotalAffected=2, got %d", resp.TotalAffected)
	}
}

func TestHandleGraphQuery_Impact_MissingSymbol(t *testing.T) {
	db := &mockGraphDB{}
	s := newTestServerWithGraph(db)
	body := `{"query":"impact"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Impact_Error(t *testing.T) {
	db := &mockGraphDB{
		impactErr: errTestFail,
	}
	s := newTestServerWithGraph(db)
	body := `{"query":"impact","symbol":"pkg.Foo"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Impact_DefaultMaxDepth(t *testing.T) {
	db := &mockGraphDB{
		impactResult: &ImpactResponse{Symbol: "pkg.Foo", Depth: 5},
	}
	s := newTestServerWithGraph(db)
	body := `{"query":"impact","symbol":"pkg.Foo","max_depth":0}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Cypher(t *testing.T) {
	db := &mockGraphDB{
		cypherResult: []map[string]any{{"n": "foo"}},
	}
	s := newTestServerWithGraph(db)
	body := `{"query":"cypher","cypher":"MATCH (n) RETURN n LIMIT 1"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Cypher_MissingCypher(t *testing.T) {
	db := &mockGraphDB{}
	s := newTestServerWithGraph(db)
	body := `{"query":"cypher"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Cypher_Error(t *testing.T) {
	db := &mockGraphDB{cypherErr: errTestFail}
	s := newTestServerWithGraph(db)
	body := `{"query":"cypher","cypher":"INVALID"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestHandleGraphQuery_UnknownQueryType(t *testing.T) {
	db := &mockGraphDB{}
	s := newTestServerWithGraph(db)
	body := `{"query":"unknown","symbol":"foo"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphQuery_Callees_Error(t *testing.T) {
	db := &mockGraphDB{calleesErr: errTestFail}
	s := newTestServerWithGraph(db)
	body := `{"query":"callees","symbol":"pkg.Foo"}`
	req := httptest.NewRequest("POST", "/api/graph/test-index/query", bytes.NewBufferString(body))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphQuery(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ── handleGraphStats ──────────────────────────────────────────────────────

func TestHandleGraphStats_MissingName(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("GET", "/api/graph/", nil)
	w := httptest.NewRecorder()
	s.handleGraphStats(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphStats_NilGraphPool(t *testing.T) {
	s := &Server{
		config:    gleann.Config{IndexDir: "/tmp"},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: nil,
	}
	req := httptest.NewRequest("GET", "/api/graph/test-index", nil)
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphStats(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp GraphStatsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Available {
		t.Error("expected Available=false for nil graph pool")
	}
}

func TestHandleGraphStats_IndexNotFound(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("GET", "/api/graph/nonexistent", nil)
	req.SetPathValue("name", "nonexistent")
	w := httptest.NewRecorder()
	s.handleGraphStats(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp GraphStatsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Available {
		t.Error("expected Available=false for missing index")
	}
}

func TestHandleGraphStats_WithDB(t *testing.T) {
	db := &mockGraphDB{
		fileCount:   10,
		symbolCount: 50,
		edgeCounts:  map[string]int{"CALLS": 30, "DECLARES": 20},
	}
	s := newTestServerWithGraph(db)
	req := httptest.NewRequest("GET", "/api/graph/test-index", nil)
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphStats(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var resp GraphStatsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Available {
		t.Error("expected Available=true")
	}
	if resp.FileCount != 10 || resp.SymCount != 50 || resp.CallsCount != 30 || resp.DeclCount != 20 {
		t.Errorf("unexpected stats: %+v", resp)
	}
}

// ── handleGraphIndex ──────────────────────────────────────────────────────

func TestHandleGraphIndex_MissingName(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("POST", "/api/graph//index", bytes.NewBufferString(`{"docs_dir":"/tmp"}`))
	w := httptest.NewRecorder()
	s.handleGraphIndex(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphIndex_BadJSON(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("POST", "/api/graph/test-index/index", bytes.NewBufferString(`{bad`))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphIndex(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphIndex_EmptyDocsDir(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("POST", "/api/graph/test-index/index", bytes.NewBufferString(`{"docs_dir":""}`))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphIndex(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphIndex_StubReturnsError(t *testing.T) {
	s := newTestServerWithGraph(nil)
	req := httptest.NewRequest("POST", "/api/graph/test-index/index", bytes.NewBufferString(`{"docs_dir":"/tmp/test"}`))
	req.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleGraphIndex(w, req)
	// In !treesitter build, runGraphIndex returns error
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

// ── graphDBPool ──────────────────────────────────────────────────────────

func TestGraphDBPool_Get_CacheHit(t *testing.T) {
	pool := newGraphDBPool("/tmp")
	db := &mockGraphDB{}
	pool.dbs["cached"] = db

	got, err := pool.get("cached")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != db {
		t.Error("expected cached DB to be returned")
	}
}

func TestGraphDBPool_Get_StubReturnsError(t *testing.T) {
	pool := newGraphDBPool("/tmp")
	_, err := pool.get("nonexistent")
	if err == nil {
		t.Error("expected error from stub openGraphDB")
	}
}

func TestGraphDBPool_Close(t *testing.T) {
	pool := newGraphDBPool("/tmp")
	db := &mockGraphDB{}
	pool.dbs["test"] = db

	pool.close("test")
	if !db.closed {
		t.Error("expected Close() to be called")
	}
	if _, ok := pool.dbs["test"]; ok {
		t.Error("expected db to be removed from pool")
	}
}

func TestGraphDBPool_CloseNonexistent(t *testing.T) {
	pool := newGraphDBPool("/tmp")
	pool.close("nonexistent") // should not panic
}

func TestGraphDBPool_CloseAll(t *testing.T) {
	pool := newGraphDBPool("/tmp")
	db1 := &mockGraphDB{}
	db2 := &mockGraphDB{}
	pool.dbs["a"] = db1
	pool.dbs["b"] = db2

	pool.closeAll()
	if !db1.closed || !db2.closed {
		t.Error("expected all DBs to be closed")
	}
	if len(pool.dbs) != 0 {
		t.Error("expected pool to be empty after closeAll")
	}
}

// ── memory stub handlers ─────────────────────────────────────────────────

func TestMemoryStub_HandleMemoryInject(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("POST", "/api/memory/test/inject", nil)
	w := httptest.NewRecorder()
	s.handleMemoryInject(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_HandleMemoryDeleteNode(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("DELETE", "/api/memory/test/nodes/abc", nil)
	w := httptest.NewRecorder()
	s.handleMemoryDeleteNode(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_HandleMemoryDeleteEdge(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("DELETE", "/api/memory/test/edges", nil)
	w := httptest.NewRecorder()
	s.handleMemoryDeleteEdge(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_HandleMemoryTraverse(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	req := httptest.NewRequest("POST", "/api/memory/test/traverse", nil)
	w := httptest.NewRecorder()
	s.handleMemoryTraverse(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}
}

func TestMemoryStub_StopMemoryPool(t *testing.T) {
	s := &Server{config: gleann.Config{}, searchers: make(map[string]*gleann.LeannSearcher)}
	// Should not panic.
	s.stopMemoryPool(nil)
}

func TestMemoryStub_CloseAll(t *testing.T) {
	p := newMemoryPool("/tmp")
	p.closeAll() // should not panic
}

// ── graph stub functions ─────────────────────────────────────────────────

func TestGraphStub_OpenGraphDB(t *testing.T) {
	_, err := openGraphDB("/tmp/test")
	if err == nil {
		t.Error("expected error from stub openGraphDB")
	}
}

func TestGraphStub_RunGraphIndex(t *testing.T) {
	err := runGraphIndex("test", "/tmp/docs", "/tmp/index", "github.com/test")
	if err == nil {
		t.Error("expected error from stub runGraphIndex")
	}
}

// errTestFail is a reusable test error.
var errTestFail = fmt.Errorf("test failure")
