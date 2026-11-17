//go:build treesitter && !windows

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/pkg/gleann"
)

// RFC 004 A0: the REST graph endpoints on the Kùzu (format-1) path, frozen as
// goldens under internal/graph/testdata/analysis_golden/rest_*. Regenerate with
//
//	go test -tags treesitter -run TestGraphRESTGolden ./internal/server -update
//
// Result arrays come back in Kùzu's engine order, so they are sorted before
// they are recorded; query_ms is zeroed.

// canonicalREST decodes a response body, sorts the arrays that have no defined
// order and re-encodes it with the volatile values normalised.
func canonicalREST(t *testing.T, env *analysisgolden.Env, status int, body []byte) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("response is not a JSON object: %v\n%s", err, body)
	}
	sortStrings := func(key string) {
		if arr, ok := v[key].([]any); ok {
			sort.Slice(arr, func(i, j int) bool { return arr[i].(string) < arr[j].(string) })
		}
	}
	for _, k := range []string{"direct_callers", "transitive_callers", "affected_files"} {
		sortStrings(k)
	}
	if arr, ok := v["results"].([]any); ok {
		sort.Slice(arr, func(i, j int) bool {
			return arr[i].(map[string]any)["fqn"].(string) < arr[j].(map[string]any)["fqn"].(string)
		})
	}
	out, err := json.Marshal(map[string]any{"status": status, "body": v})
	if err != nil {
		t.Fatal(err)
	}
	return env.CanonicalJSON(t, string(out))
}

func TestGraphRESTGolden(t *testing.T) {
	env := analysisgolden.BuildIndex(t)
	s := &Server{
		config:    gleann.Config{IndexDir: env.IndexDir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: newGraphDBPool(env.IndexDir),
	}
	t.Cleanup(s.graphPool.closeAll)
	name := analysisgolden.IndexName

	// GET /api/graph/{name}: stats.
	req := httptest.NewRequest(http.MethodGet, "/api/graph/"+name, nil)
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	s.handleGraphStats(rec, req)
	analysisgolden.Check(t, "rest_stats.json", canonicalREST(t, env, rec.Code, rec.Body.Bytes()))

	// GET /api/graph/{name}: an index without a graph.
	req = httptest.NewRequest(http.MethodGet, "/api/graph/absent", nil)
	req.SetPathValue("name", "absent")
	rec = httptest.NewRecorder()
	s.handleGraphStats(rec, req)
	analysisgolden.Check(t, "rest_stats_absent.json", canonicalREST(t, env, rec.Code, rec.Body.Bytes()))

	// POST /api/graph/{name}/query.
	for _, q := range []struct {
		golden string
		req    GraphQueryRequest
	}{
		{"rest_query_callers.json", GraphQueryRequest{Query: "callers", Symbol: "example.com/fixture/py.clean"}},
		{"rest_query_callers_short.json", GraphQueryRequest{Query: "callers", Symbol: "validate"}},
		{"rest_query_callees.json", GraphQueryRequest{Query: "callees", Symbol: "example.com/fixture/ts.slug"}},
		{"rest_query_callees_rust.json", GraphQueryRequest{Query: "callees", Symbol: "example.com/fixture/rs.Counter::bump"}},
		{"rest_query_symbols_in_file.json", GraphQueryRequest{Query: "symbols_in_file", File: "py/app.py"}},
		{"rest_query_symbols_in_file_go.json", GraphQueryRequest{Query: "symbols_in_file", File: "service/service.go"}},
		{"rest_query_impact.json", GraphQueryRequest{Query: "impact", Symbol: "example.com/fixture/store.NewMemStore", MaxDepth: 3}},
		{"rest_query_impact_default_depth.json", GraphQueryRequest{Query: "impact", Symbol: "example.com/fixture/py.clean"}},
		{"rest_query_toc.json", GraphQueryRequest{Query: "toc", File: "py/app.py"}},
		{"rest_query_unknown.json", GraphQueryRequest{Query: "nonsense"}},
		// Raw Cypher is retired by RFC 004 A2; recorded so its removal is visible.
		{"rest_query_cypher.json", GraphQueryRequest{Query: "cypher", Cypher: "MATCH (s:Symbol) WHERE s.file IS NOT NULL RETURN count(s) AS c"}},
	} {
		body, _ := json.Marshal(q.req)
		req := httptest.NewRequest(http.MethodPost, "/api/graph/"+name+"/query", bytes.NewReader(body))
		req.SetPathValue("name", name)
		rec := httptest.NewRecorder()
		s.handleGraphQuery(rec, req)
		analysisgolden.Check(t, q.golden, canonicalREST(t, env, rec.Code, rec.Body.Bytes()))
	}
}
