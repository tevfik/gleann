package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/pkg/gleann"
)

// RFC 004 A2: the REST and A2A graph features on a format-2 index, in every
// build. The queries mirror the A0 Kùzu goldens (rest_*.json); see
// analysis_golden/README.md for the differences.

type restQueryCase struct {
	golden string
	req    GraphQueryRequest
}

var restQueryCases = []restQueryCase{
	{"rest_query_callers.json", GraphQueryRequest{Query: "callers", Symbol: "example.com/fixture/py.clean"}},
	{"rest_query_callers_go.json", GraphQueryRequest{Query: "callers", Symbol: "example.com/fixture/service.Service.validate"}},
	{"rest_query_callees_go.json", GraphQueryRequest{Query: "callees", Symbol: "example.com/fixture/service.Service.Save"}},
	{"rest_query_symbols_in_file.json", GraphQueryRequest{Query: "symbols_in_file", File: "py/app.py"}},
	{"rest_query_symbols_in_file_go.json", GraphQueryRequest{Query: "symbols_in_file", File: "service/service.go"}},
	{"rest_query_impact.json", GraphQueryRequest{Query: "impact", Symbol: "example.com/fixture/store.NewMemStore", MaxDepth: 3}},
	{"rest_query_impact_default_depth.json", GraphQueryRequest{Query: "impact", Symbol: "example.com/fixture/store.NewMemStore"}},
	{"rest_query_toc.json", GraphQueryRequest{Query: "toc", File: "py/app.py"}},
	{"rest_query_unknown.json", GraphQueryRequest{Query: "nonsense"}},
	{"rest_query_cypher.json", GraphQueryRequest{Query: "cypher", Cypher: "MATCH (s:Symbol) RETURN count(s) AS c"}},
}

func newRESTServer(t *testing.T) (*Server, *analysisgolden.Env) {
	t.Helper()
	env := analysisgolden.BuildIndexFormat2(t)
	s := &Server{
		config:    gleann.Config{IndexDir: env.IndexDir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: newGraphDBPool(env.IndexDir),
	}
	t.Cleanup(s.graphPool.closeAll)
	return s, env
}

func restStats(s *Server, name string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/graph/"+name, nil)
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	s.handleGraphStats(rec, req)
	return rec
}

func restQuery(s *Server, name string, q GraphQueryRequest) *httptest.ResponseRecorder {
	body, _ := json.Marshal(q)
	req := httptest.NewRequest(http.MethodPost, "/api/graph/"+name+"/query", bytes.NewReader(body))
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	s.handleGraphQuery(rec, req)
	return rec
}

func TestGraphRESTSQLite(t *testing.T) {
	s, env := newRESTServer(t)
	name := analysisgolden.IndexName

	rec := restStats(s, name)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats status %d: %s", rec.Code, rec.Body)
	}
	var st GraphStatsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.SymCount == 0 || st.FileCount == 0 {
		t.Errorf("stats = %+v", st)
	}
	if !strings.HasSuffix(st.DBPath, filepath.Join(name, name+".db")) {
		t.Errorf("db_path = %q", st.DBPath)
	}
	checkSQLiteGolden(t, "rest_stats.json", canonicalREST(t, env, rec.Code, rec.Body.Bytes()))

	rec = restStats(s, "absent")
	if rec.Code != http.StatusNotFound {
		t.Errorf("absent stats = %d, want 404", rec.Code)
	}
	checkSQLiteGolden(t, "rest_stats_absent.json", canonicalREST(t, env, rec.Code, rec.Body.Bytes()))

	for _, q := range restQueryCases {
		rec := restQuery(s, name, q.req)
		checkSQLiteGolden(t, q.golden, canonicalREST(t, env, rec.Code, rec.Body.Bytes()))
	}
}

func TestGraphRESTSQLiteStatuses(t *testing.T) {
	s, _ := newRESTServer(t)
	name := analysisgolden.IndexName

	if rec := restQuery(s, name, GraphQueryRequest{Query: "cypher", Cypher: "MATCH (n) RETURN n"}); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "raw Cypher was removed") {
		t.Errorf("cypher = %d %s", rec.Code, rec.Body)
	}
	// A code file is not a document: 404, not 500.
	if rec := restQuery(s, name, GraphQueryRequest{Query: "toc", File: "service/service.go"}); rec.Code != http.StatusNotFound {
		t.Errorf("toc of a code file = %d %s", rec.Code, rec.Body)
	}
	if rec := restQuery(s, "absent", GraphQueryRequest{Query: "callers", Symbol: "x"}); rec.Code != http.StatusNotFound {
		t.Errorf("query on an absent index = %d", rec.Code)
	}
	rec := restQuery(s, name, GraphQueryRequest{Query: "callers", Symbol: "example.com/fixture/service.Service.validate"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Service") {
		t.Errorf("callers = %d %s", rec.Code, rec.Body)
	}
	// /documents lists documents (none in the code fixture) without failing.
	req := httptest.NewRequest(http.MethodGet, "/api/graph/"+name+"/documents", nil)
	req.SetPathValue("name", name)
	w := httptest.NewRecorder()
	s.handleListDocuments(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("documents = %d %s", w.Code, w.Body)
	}
}

// POST /api/graph/{name}/index builds the format-2 graph in every build.
func TestGraphRESTSQLiteIndexBuild(t *testing.T) {
	s, env := newRESTServer(t)
	name := analysisgolden.IndexName
	t.Setenv("GLEANN_ALLOWED_ROOTS", env.Fixture)
	body, _ := json.Marshal(map[string]string{"docs_dir": env.Fixture})
	req := httptest.NewRequest(http.MethodPost, "/api/graph/"+name+"/index", bytes.NewReader(body))
	req.SetPathValue("name", name)
	w := httptest.NewRecorder()
	s.handleGraphIndex(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("index = %d %s", w.Code, w.Body)
	}
	if rec := restStats(s, name); rec.Code != http.StatusOK {
		t.Errorf("stats after rebuild = %d", rec.Code)
	}
}

func TestA2AGraphSkillsSQLite(t *testing.T) {
	s, env := newRESTServer(t)
	name := analysisgolden.IndexName
	ctx := a2a.SkillContext{Metadata: map[string]interface{}{"index": name}}
	for golden, h := range map[string]func(a2a.SkillContext) (string, error){
		"a2a_communities.txt": s.a2aCommunitiesHandler,
		"a2a_repo_map.txt":    s.a2aRepoMapHandler,
		"a2a_risk.txt":        s.a2aRiskHandler,
	} {
		first, err := h(ctx)
		if err != nil {
			t.Fatalf("%s: %v", golden, err)
		}
		for i := 0; i < 3; i++ {
			again, _ := h(ctx)
			if again != first {
				t.Fatalf("%s not deterministic", golden)
			}
		}
		checkSQLiteGolden(t, ""+golden, env.Normalize(first))
	}
	if _, err := s.a2aRiskHandler(a2a.SkillContext{Metadata: map[string]interface{}{"index": "absent"}}); err == nil {
		t.Error("absent index must fail")
	}
}
