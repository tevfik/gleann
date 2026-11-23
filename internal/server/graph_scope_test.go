package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/auth"
	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/pkg/gleann"
)

// TestGraphQueryScope tests that POST /api/graph/{name}/query accepts ScopeQuery tokens
// while POST /api/graph/{name}/index requires ScopeAdmin.
func TestGraphQueryScope(t *testing.T) {
	// Setup auth store with query and admin tokens
	st, err := auth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admin, _, _ := st.EnsureAdmin()
	queryTok, _ := st.Create("query-token", auth.ScopeQuery)

	// Build a test index with a graph using the fixture index
	env := analysisgolden.BuildIndexFormat2(t)
	name := analysisgolden.IndexName

	// Set up allowed roots to allow the fixture directory
	t.Setenv("GLEANN_ALLOWED_ROOTS", env.Fixture)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = env.IndexDir
	s := &Server{
		config:    cfg,
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: newGraphDBPool(env.IndexDir),
	}
	t.Cleanup(s.graphPool.closeAll)

	// Wrap with both middleware layers: security (auth) then body limit/rate/etc
	h := securityMiddleware(newSecurityConfig("127.0.0.1:8080", st, nil), withMiddleware(s.buildMux()))

	// Test cases for graph endpoints
	cases := []struct {
		name       string
		method     string
		path       string
		body       []byte
		authTok    string
		wantStatus int
	}{
		{
			name:       "query-token can GET /api/graph/{name}",
			method:     http.MethodGet,
			path:       "/api/graph/" + name,
			body:       nil,
			authTok:    queryTok,
			wantStatus: http.StatusOK,
		},
		{
			name:       "query-token can POST /api/graph/{name}/query (read-only)",
			method:     http.MethodPost,
			path:       "/api/graph/" + name + "/query",
			body:       mustMarshal(t, GraphQueryRequest{Query: "callers", Symbol: "x"}),
			authTok:    queryTok,
			wantStatus: http.StatusOK,
		},
		{
			name:       "query-token CANNOT POST /api/graph/{name}/index (write)",
			method:     http.MethodPost,
			path:       "/api/graph/" + name + "/index",
			body:       mustMarshal(t, map[string]string{"docs_dir": env.Fixture}),
			authTok:    queryTok,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "admin-token can POST /api/graph/{name}/query",
			method:     http.MethodPost,
			path:       "/api/graph/" + name + "/query",
			body:       mustMarshal(t, GraphQueryRequest{Query: "callers", Symbol: "x"}),
			authTok:    admin,
			wantStatus: http.StatusOK,
		},
		{
			name:       "admin-token can POST /api/graph/{name}/index",
			method:     http.MethodPost,
			path:       "/api/graph/" + name + "/index",
			body:       mustMarshal(t, map[string]string{"docs_dir": env.Fixture}),
			authTok:    admin,
			wantStatus: http.StatusOK,
		},
		{
			name:       "no token gets 401 on graph query",
			method:     http.MethodPost,
			path:       "/api/graph/" + name + "/query",
			body:       mustMarshal(t, GraphQueryRequest{Query: "callers", Symbol: "x"}),
			authTok:    "",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
			req.Host = "127.0.0.1:8080"
			req.Header.Set("Content-Type", "application/json")
			if tc.authTok != "" {
				req.Header.Set("Authorization", "Bearer "+tc.authTok)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("got %d, want %d: %s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

// TestGraphQueryQueryTypes tests that every valid query type succeeds with a
// ScopeQuery token (HTTP 200, not merely "not 403") and returns the fixture's
// actual results. A broken endpoint (503/500/404) or a wrong/empty body fails
// the test. The symbol FQNs are real declarations of the analysis fixture, so
// resolution does not rely on the base-name fallback.
func TestGraphQueryQueryTypes(t *testing.T) {
	t.Parallel()

	st, err := auth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	queryTok, _ := st.Create("query", auth.ScopeQuery)

	env := analysisgolden.BuildIndexFormat2(t)
	name := analysisgolden.IndexName

	// The fixture is code-only and declares no documents, so a toc query would
	// otherwise 404. Seed one markdown document into this test's own index so
	// the toc query type can be exercised end-to-end. This touches neither the
	// shared fixture nor the code graph the other query types assert on.
	seedScopeDocument(t, env.IndexDir, name)

	s := &Server{
		config:    gleann.Config{IndexDir: env.IndexDir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: newGraphDBPool(env.IndexDir),
	}
	t.Cleanup(s.graphPool.closeAll)

	h := securityMiddleware(newSecurityConfig("127.0.0.1:8080", st, nil), withMiddleware(s.buildMux()))

	// Real fixture FQNs (see analysis_golden/sqlite_lite/rest_query_*.json).
	const (
		validateFQN = "example.com/fixture/service.validate"     // package-level func
		saveFQN     = "example.com/fixture/service.Service.Save" // method, calls validate
		newMemFQN   = "example.com/fixture/store.NewMemStore"    // called by main
		mainFQN     = "example.com/fixture.main"                 // calls NewMemStore
		serviceFile = "service/service.go"                       // declares 9 symbols
		docPath     = "docs/guide.md"                            // seeded document
	)

	cases := []struct {
		name  string
		req   GraphQueryRequest
		check func(t *testing.T, body []byte)
	}{
		{
			name: "callers",
			req:  GraphQueryRequest{Query: "callers", Symbol: validateFQN},
			check: func(t *testing.T, body []byte) {
				resp := decodeGraphQuery(t, body)
				// validate is called only by Service.Save.
				if resp.Count != 1 || len(resp.Results) != 1 {
					t.Fatalf("callers of %s: count = %d (%d results), want 1: %s", validateFQN, resp.Count, len(resp.Results), body)
				}
				if got := resp.Results[0].FQN; got != saveFQN {
					t.Fatalf("callers of %s = %q, want %q: %s", validateFQN, got, saveFQN, body)
				}
			},
		},
		{
			name: "callees",
			req:  GraphQueryRequest{Query: "callees", Symbol: saveFQN},
			check: func(t *testing.T, body []byte) {
				resp := decodeGraphQuery(t, body)
				// Save calls validate and the store.Store interface (s.st.Put).
				want := map[string]bool{validateFQN: true, "example.com/fixture/store.Store": true}
				assertSymbolSet(t, "callees of "+saveFQN, resp, want, body)
			},
		},
		{
			name: "symbols_in_file",
			req:  GraphQueryRequest{Query: "symbols_in_file", File: serviceFile},
			check: func(t *testing.T, body []byte) {
				resp := decodeGraphQuery(t, body)
				// The nine declarations of service/service.go.
				want := map[string]bool{
					"example.com/fixture/service.New":            true,
					"example.com/fixture/service.Service":        true,
					"example.com/fixture/service.Service.Load":   true,
					"example.com/fixture/service.Service.Report": true,
					saveFQN:                                  true,
					"example.com/fixture/service.emptyError": true,
					"example.com/fixture/service.emptyError.Error": true,
					"example.com/fixture/service.errEmpty":         true,
					validateFQN:                                    true,
				}
				assertSymbolSet(t, "symbols_in_file "+serviceFile, resp, want, body)
			},
		},
		{
			name: "impact",
			req:  GraphQueryRequest{Query: "impact", Symbol: newMemFQN, MaxDepth: 3},
			check: func(t *testing.T, body []byte) {
				var resp ImpactResponse
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("decoding impact response: %v\n%s", err, body)
				}
				if resp.Symbol != newMemFQN {
					t.Fatalf("impact symbol = %q, want %q: %s", resp.Symbol, newMemFQN, body)
				}
				if resp.Depth != 3 {
					t.Fatalf("impact depth = %d, want 3: %s", resp.Depth, body)
				}
				if !hasString(resp.DirectCallers, mainFQN) {
					t.Fatalf("impact direct_callers missing %q: %s", mainFQN, body)
				}
				if !hasString(resp.AffectedFiles, "main.go") {
					t.Fatalf("impact affected_files missing main.go: %s", body)
				}
				if resp.TotalAffected != len(resp.DirectCallers)+len(resp.TransitiveCallers) {
					t.Fatalf("impact total_affected = %d, want direct+transitive = %d: %s",
						resp.TotalAffected, len(resp.DirectCallers)+len(resp.TransitiveCallers), body)
				}
			},
		},
		{
			name: "toc",
			req:  GraphQueryRequest{Query: "toc", File: docPath},
			check: func(t *testing.T, body []byte) {
				var resp struct {
					Toc gleann.DocumentTOCInfo `json:"toc"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("decoding toc response: %v\n%s", err, body)
				}
				if resp.Toc.RPath != docPath {
					t.Fatalf("toc rpath = %q, want %q: %s", resp.Toc.RPath, docPath, body)
				}
				if resp.Toc.Title != "Guide" {
					t.Fatalf("toc title = %q, want Guide: %s", resp.Toc.Title, body)
				}
				if len(resp.Toc.Headings) != 2 ||
					resp.Toc.Headings[0].Title != "Intro" || resp.Toc.Headings[1].Title != "Usage" {
					t.Fatalf("toc headings = %+v, want [Intro Usage]: %s", resp.Toc.Headings, body)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reqHTTP := httptest.NewRequest(http.MethodPost, "/api/graph/"+name+"/query", bytes.NewReader(mustMarshal(t, tc.req)))
			reqHTTP.Host = "127.0.0.1:8080"
			reqHTTP.Header.Set("Content-Type", "application/json")
			reqHTTP.Header.Set("Authorization", "Bearer "+queryTok)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, reqHTTP)

			// Every query type must succeed, not merely avoid 403.
			if w.Code != http.StatusOK {
				t.Fatalf("query type %s: got %d, want %d: %s", tc.name, w.Code, http.StatusOK, w.Body.String())
			}
			tc.check(t, w.Body.Bytes())
		})
	}
}

// TestA2AGraphScope tests that the A2A code-analysis skill returns the fixture's
// real graph results under both query and admin scope. It asserts err == nil and
// the known caller in the output (the way graph_rest_sqlite_test.go does), so a
// failing or empty handler fails the test instead of being logged and ignored.
func TestA2AGraphScope(t *testing.T) {
	t.Parallel()

	env := analysisgolden.BuildIndexFormat2(t)
	name := analysisgolden.IndexName

	s := &Server{
		config:    gleann.Config{IndexDir: env.IndexDir},
		searchers: make(map[string]*gleann.LeannSearcher),
		graphPool: newGraphDBPool(env.IndexDir),
	}
	t.Cleanup(s.graphPool.closeAll)

	// A real fixture symbol with a callers intent, so the handler returns the
	// known caller (Service.Save) rather than an empty "no callers" message.
	const symbol = "example.com/fixture/service.validate"
	const wantCaller = "example.com/fixture/service.Service.Save"

	for _, scope := range []auth.Scope{auth.ScopeQuery, auth.ScopeAdmin} {
		ctx := a2a.SkillContext{
			Metadata: map[string]interface{}{"index": name, "symbol": symbol},
			Query:    "who calls " + symbol,
			Scope:    string(scope),
		}
		out, err := s.a2aCodeHandler(ctx)
		if err != nil {
			t.Fatalf("a2aCodeHandler with %s scope: %v", scope, err)
		}
		if !strings.Contains(out, wantCaller) {
			t.Fatalf("a2aCodeHandler with %s scope: output missing known caller %q:\n%s", scope, wantCaller, out)
		}
	}
}

// seedScopeDocument adds one markdown document (folder > document > two
// headings) to the index's document graph so the toc query type has a real
// document to return. It writes only doc_nodes, leaving the code graph intact.
func seedScopeDocument(t *testing.T, indexDir, name string) {
	t.Helper()
	gs, err := gleann.OpenGraphStore(indexDir, name)
	if err != nil {
		t.Fatalf("opening graph store to seed document: %v", err)
	}
	if gs == nil {
		t.Fatal("fixture index has no graph store to seed a document into")
	}
	defer gs.Close()
	err = gs.ApplyDocDelta(context.Background(), graphstore.DocDelta{
		RPath: "docs/guide.md",
		Nodes: []graphstore.DocNode{
			{ID: "scope-fold", Type: "folder", Name: "docs"},
			{ID: "scope-doc", Type: "document", Parent: "scope-fold", Name: "Guide", RPath: "docs/guide.md", Summary: "scope test document"},
			{ID: "scope-h1", Type: "heading", Parent: "scope-doc", Name: "Intro", Level: 1},
			{ID: "scope-h2", Type: "heading", Parent: "scope-doc", Name: "Usage", Level: 1},
		},
	})
	if err != nil {
		t.Fatalf("seeding document: %v", err)
	}
}

// decodeGraphQuery unmarshals a callers/callees/symbols_in_file response body.
func decodeGraphQuery(t *testing.T, body []byte) GraphQueryResponse {
	t.Helper()
	var resp GraphQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decoding graph query response: %v\n%s", err, body)
	}
	return resp
}

// assertSymbolSet checks that resp.Count and the result FQNs exactly match want.
func assertSymbolSet(t *testing.T, label string, resp GraphQueryResponse, want map[string]bool, body []byte) {
	t.Helper()
	if resp.Count != len(want) || len(resp.Results) != len(want) {
		t.Fatalf("%s: count = %d (%d results), want %d: %s", label, resp.Count, len(resp.Results), len(want), body)
	}
	remaining := make(map[string]bool, len(want))
	for fqn := range want {
		remaining[fqn] = true
	}
	for _, n := range resp.Results {
		if !remaining[n.FQN] {
			t.Fatalf("%s: unexpected symbol %q: %s", label, n.FQN, body)
		}
		delete(remaining, n.FQN)
	}
	if len(remaining) != 0 {
		t.Fatalf("%s: missing symbols %v: %s", label, remaining, body)
	}
}

// hasString reports whether list contains s.
func hasString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// mustMarshal marshals a value to JSON, fataling on error.
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
