package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/graph/community"
	"github.com/tevfik/gleann/internal/graphstore"
)

// analysisMockGraphDB is a mockGraphDB that also feeds the A2A analysis skills.
type analysisMockGraphDB struct {
	mockGraphDB
	communities    *community.Result
	communitiesErr error
	callGraph      *community.Graph
	callGraphErr   error
}

func (m *analysisMockGraphDB) Communities(int, int) (*community.Result, error) {
	return m.communities, m.communitiesErr
}
func (m *analysisMockGraphDB) CallGraph() (*community.Graph, error) {
	return m.callGraph, m.callGraphErr
}

func graphReq(s *Server, h http.HandlerFunc, method, name, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/graph/x", strings.NewReader(body))
	r.SetPathValue("name", name)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestGraphHandlers_NameAndAccessGuards(t *testing.T) {
	s := newTestServerWithGraph(&mockGraphDB{})
	deny := map[string]string{"X-Gleann-Target": "another"}
	handlers := map[string]http.HandlerFunc{
		"query":     s.handleGraphQuery,
		"stats":     s.handleGraphStats,
		"index":     s.handleGraphIndex,
		"toc":       s.handleDocumentTOC,
		"documents": s.handleListDocuments,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			if w := graphReq(s, h, "POST", "bad name!", `{}`, nil); w.Code != http.StatusBadRequest {
				t.Errorf("invalid name: %d %s", w.Code, w.Body.String())
			}
			if w := graphReq(s, h, "POST", "test-index", `{}`, deny); w.Code != http.StatusForbidden {
				t.Errorf("foreign client target: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestGraphHandlers_NilPool(t *testing.T) {
	s := newTestServerWithGraph(nil)
	s.graphPool = nil
	for name, h := range map[string]http.HandlerFunc{
		"toc":       s.handleDocumentTOC,
		"documents": s.handleListDocuments,
	} {
		if w := graphReq(s, h, "GET", "test-index", "", nil); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// handleGraphIndex tolerates a missing pool, reaching the build step.
	s.config.IndexDir = t.TempDir()
	s.config.AllowedRoots = []string{t.TempDir()}
	if w := graphReq(s, s.handleGraphIndex, "POST", "idx", `{"docs_dir":"`+filepath.ToSlash(s.config.AllowedRoots[0])+`"}`, nil); w.Code != http.StatusConflict {
		t.Errorf("no such index: %d %s", w.Code, w.Body.String())
	}
}

func TestGraphHandlers_OpenFailureIs404(t *testing.T) {
	s := newTestServerWithGraph(nil)
	s.config.IndexDir = t.TempDir()
	s.graphPool = newGraphDBPool(s.config.IndexDir)
	for name, h := range map[string]http.HandlerFunc{
		"toc":       s.handleDocumentTOC,
		"documents": s.handleListDocuments,
	} {
		w := graphReq(s, h, "GET", "absent", "", nil)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "graph index") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if _, err := s.graphPool.get("bad name!"); err == nil {
		t.Error("pool must reject invalid names")
	}
	if _, err := s.graphPool.get("absent"); !errors.Is(err, errNoCodeGraph) {
		t.Errorf("absent index error = %v, want errNoCodeGraph", err)
	}
}

func TestHandleDocumentTOC_ErrorPaths(t *testing.T) {
	db := &mockGraphDB{listDocsErr: errors.New("list boom"), tocErr: errors.New("toc boom")}
	s := newTestServerWithGraph(db)

	if w := graphReq(s, s.handleDocumentTOC, "GET", "test-index", "", nil); w.Code != http.StatusInternalServerError ||
		!strings.Contains(w.Body.String(), "list boom") {
		t.Errorf("list failure: %d %s", w.Code, w.Body.String())
	}
	// A TOC failure for a named document is reported as not found; vpath is an alias of path.
	r := httptest.NewRequest("GET", "/api/graph/test-index/toc?vpath=docs/a.md", nil)
	r.SetPathValue("name", "test-index")
	w := httptest.NewRecorder()
	s.handleDocumentTOC(w, r)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "docs/a.md") {
		t.Errorf("toc failure: %d %s", w.Code, w.Body.String())
	}
	if w := graphReq(s, s.handleListDocuments, "GET", "test-index", "", nil); w.Code != http.StatusInternalServerError {
		t.Errorf("documents failure: %d %s", w.Code, w.Body.String())
	}
}

func TestHandleGraphQuery_TOCAndFailureBranches(t *testing.T) {
	t.Run("toc falls back to symbol, missing doc is 404", func(t *testing.T) {
		s := newTestServerWithGraph(&mockGraphDB{tocErr: fmt.Errorf("read: %w", graphstore.ErrDocumentNotFound)})
		w := graphReq(s, s.handleGraphQuery, "POST", "test-index", `{"query":"toc","symbol":"docs/x.md"}`, nil)
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "docs/x.md") {
			t.Errorf("%d %s", w.Code, w.Body.String())
		}
	})
	t.Run("toc failure that is not a missing document is 500", func(t *testing.T) {
		s := newTestServerWithGraph(&mockGraphDB{tocErr: errors.New("disk")})
		w := graphReq(s, s.handleGraphQuery, "POST", "test-index", `{"query":"toc","file":"a.md"}`, nil)
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "document toc failed") {
			t.Errorf("%d %s", w.Code, w.Body.String())
		}
	})
	t.Run("toc without file or symbol", func(t *testing.T) {
		s := newTestServerWithGraph(&mockGraphDB{})
		if w := graphReq(s, s.handleGraphQuery, "POST", "test-index", `{"query":"toc"}`, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%d", w.Code)
		}
	})
	t.Run("each query surfaces backend errors", func(t *testing.T) {
		boom := errors.New("backend down")
		s := newTestServerWithGraph(&mockGraphDB{callersErr: boom, symbolsErr: boom, impactErr: boom})
		for _, body := range []string{
			`{"query":"callers","symbol":"x"}`,
			`{"query":"symbols_in_file","file":"f.go"}`,
			`{"query":"impact","symbol":"x"}`,
		} {
			w := graphReq(s, s.handleGraphQuery, "POST", "test-index", body, nil)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "backend down") {
				t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
			}
		}
	})
	t.Run("cypher backend error that is not the removal sentinel", func(t *testing.T) {
		s := newTestServerWithGraph(&mockGraphDB{cypherErr: errors.New("syntax")})
		w := graphReq(s, s.handleGraphQuery, "POST", "test-index", `{"query":"cypher","cypher":"MATCH"}`, nil)
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "cypher failed") {
			t.Errorf("%d %s", w.Code, w.Body.String())
		}
	})
	t.Run("cypher removal sentinel is 400", func(t *testing.T) {
		s := newTestServerWithGraph(&mockGraphDB{cypherErr: errCypherRemoved})
		if w := graphReq(s, s.handleGraphQuery, "POST", "test-index", `{"query":"cypher","cypher":"MATCH"}`, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%d", w.Code)
		}
	})
	t.Run("open failure", func(t *testing.T) {
		s := newTestServerWithGraph(nil)
		s.config.IndexDir = t.TempDir()
		s.graphPool = newGraphDBPool(s.config.IndexDir)
		if w := graphReq(s, s.handleGraphQuery, "POST", "absent", `{"query":"callers","symbol":"x"}`, nil); w.Code != http.StatusNotFound {
			t.Errorf("%d", w.Code)
		}
	})
}

func TestHandleGraphIndex_PathAndBuildFailures(t *testing.T) {
	root := t.TempDir()
	s := newTestServerWithGraph(nil)
	s.config.IndexDir = t.TempDir()
	s.config.AllowedRoots = []string{root}
	s.graphPool = newGraphDBPool(s.config.IndexDir)
	docs := filepath.ToSlash(root)

	// Outside the allowed roots.
	other := filepath.ToSlash(t.TempDir())
	if w := graphReq(s, s.handleGraphIndex, "POST", "idx", `{"docs_dir":"`+other+`"}`, nil); w.Code != http.StatusForbidden {
		t.Errorf("outside roots: %d %s", w.Code, w.Body.String())
	}
	// No index of that name: 409 (nothing to attach a graph to).
	if w := graphReq(s, s.handleGraphIndex, "POST", "idx", `{"docs_dir":"`+docs+`"}`, nil); w.Code != http.StatusConflict {
		t.Errorf("no index: %d %s", w.Code, w.Body.String())
	}
	// A cached handle is closed before the build is attempted.
	mock := &mockGraphDB{}
	s.graphPool.dbs["idx"] = mock
	graphReq(s, s.handleGraphIndex, "POST", "idx", `{"docs_dir":"`+docs+`"}`, nil)
	if !mock.closed {
		t.Error("cached graph handle must be closed before re-indexing")
	}
	// An index directory whose database is not a database fails the build with 500.
	idxDir := filepath.Join(s.config.IndexDir, "broken")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxDir, "broken.db"), []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := graphReq(s, s.handleGraphIndex, "POST", "broken", `{"docs_dir":"`+docs+`"}`, nil)
	if w.Code != http.StatusInternalServerError && w.Code != http.StatusConflict {
		t.Errorf("corrupt database: %d %s", w.Code, w.Body.String())
	}
}

func TestA2AGraphSkills_ErrorPaths(t *testing.T) {
	boom := errors.New("analysis boom")
	handlers := map[string]func(*Server) func(a2a.SkillContext) (string, error){
		"communities": func(s *Server) func(a2a.SkillContext) (string, error) { return s.a2aCommunitiesHandler },
		"repo map":    func(s *Server) func(a2a.SkillContext) (string, error) { return s.a2aRepoMapHandler },
		"risk":        func(s *Server) func(a2a.SkillContext) (string, error) { return s.a2aRiskHandler },
	}
	ctx := a2a.SkillContext{Metadata: map[string]interface{}{"index": "test-index"}}

	for name, pick := range handlers {
		t.Run(name, func(t *testing.T) {
			// No indexes at all and no index named.
			s := newTestServerWithGraph(nil)
			s.config.IndexDir = t.TempDir()
			if _, err := pick(s)(a2a.SkillContext{}); err == nil || !strings.Contains(err.Error(), "no indexes available") {
				t.Errorf("no indexes: %v", err)
			}
			// Invalid index name.
			if _, err := pick(s)(a2a.SkillContext{Metadata: map[string]interface{}{"index": "bad name!"}}); err == nil {
				t.Error("invalid name accepted")
			}
			// Graph cannot be opened.
			if _, err := pick(s)(ctx); err == nil || !strings.Contains(err.Error(), "not found") {
				t.Errorf("open failure: %v", err)
			}
			// Handle without analysis support.
			s = newTestServerWithGraph(&mockGraphDB{})
			if _, err := pick(s)(ctx); err == nil || !strings.Contains(err.Error(), "does not support analysis") {
				t.Errorf("no analysis: %v", err)
			}
			// Analysis backend failures.
			s = newTestServerWithGraph(&analysisMockGraphDB{communitiesErr: boom, callGraphErr: boom})
			if _, err := pick(s)(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
				t.Errorf("backend failure: %v", err)
			}
		})
	}

	t.Run("empty call graph", func(t *testing.T) {
		s := newTestServerWithGraph(&analysisMockGraphDB{callGraph: community.NewGraph()})
		for name, h := range map[string]func(a2a.SkillContext) (string, error){"repo map": s.a2aRepoMapHandler, "risk": s.a2aRiskHandler} {
			out, err := h(ctx)
			if err != nil || !strings.Contains(out, "No nodes in graph") {
				t.Errorf("%s: %q %v", name, out, err)
			}
		}
	})
}

func TestResolveIndex(t *testing.T) {
	dir := t.TempDir()
	if got := resolveIndex(map[string]interface{}{"index": "named"}, dir); got != "named" {
		t.Errorf("explicit index = %q", got)
	}
	if got := resolveIndex(nil, dir); got != "" {
		t.Errorf("empty dir = %q", got)
	}
	createMockIndexMeta(t, dir, "first", true, nil)
	if got := resolveIndex(map[string]interface{}{"index": ""}, dir); got != "first" {
		t.Errorf("fallback = %q, want first", got)
	}
}
