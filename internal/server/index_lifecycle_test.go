package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

func doJSON(h http.Handler, method, path string, body any, hdr map[string]string) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	switch b := body.(type) {
	case string:
		rdr = bytes.NewReader([]byte(b))
	default:
		j, _ := json.Marshal(b)
		rdr = bytes.NewReader(j)
	}
	r := httptest.NewRequest(method, path, rdr)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func getMeta(t *testing.T, h http.Handler, name string) gleann.IndexMeta {
	t.Helper()
	w := doJSON(h, "GET", "/api/indexes/"+name, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get %s: %d %s", name, w.Code, w.Body.String())
	}
	var m gleann.IndexMeta
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBuildItemsWithGovernanceMetadataThenPatch(t *testing.T) {
	_, h, _ := newIndexedServer(t)

	w := doJSON(h, "POST", "/api/indexes/gov/build", map[string]any{
		"items":       []map[string]any{{"text": "first governed passage"}, {"text": "second governed passage"}},
		"tags":        []string{"team-a"},
		"description": "governed index",
		"mcp_exposed": false,
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("build: %d %s", w.Code, w.Body.String())
	}
	m := getMeta(t, h, "gov")
	if m.Description != "governed index" || len(m.Tags) != 1 || m.Tags[0] != "team-a" || m.MCPExposed == nil || *m.MCPExposed {
		t.Errorf("governance metadata from build not stored: %+v", m)
	}

	// PATCH replaces tags/description and flips exposure; the cached searcher is
	// dropped so the next GET sees it.
	w = doJSON(h, "PATCH", "/api/indexes/gov", `{"tags":["team-b","x"],"description":"renamed","mcp_exposed":true}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	m = getMeta(t, h, "gov")
	if m.Description != "renamed" || len(m.Tags) != 2 || m.MCPExposed == nil || !*m.MCPExposed {
		t.Errorf("patch not applied: %+v", m)
	}
}

func TestIndexMetaEndpoints_Errors(t *testing.T) {
	_, h, _ := newIndexedServer(t)
	tests := []struct {
		name, method, path, body string
		hdr                      map[string]string
		want                     int
	}{
		{"get forbidden for foreign target", "GET", "/api/indexes/idx", "", map[string]string{"X-Gleann-Target": "other"}, http.StatusForbidden},
		{"get unknown index", "GET", "/api/indexes/ghost", "", nil, http.StatusNotFound},
		{"patch bad json", "PATCH", "/api/indexes/idx", "{", nil, http.StatusBadRequest},
		{"patch unknown index", "PATCH", "/api/indexes/ghost", `{"description":"x"}`, nil, http.StatusNotFound},
		{"patch invalid name", "PATCH", "/api/indexes/bad%20name", `{}`, nil, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(h, tc.method, tc.path, tc.body, tc.hdr)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestBuild_ModelMismatchAndEmbedderFailure(t *testing.T) {
	s, h, _ := newIndexedServer(t)
	body := map[string]any{"texts": []string{"alpha beta"}}

	// "idx" was built with test-model; a server now embedding with another
	// model must refuse to extend it.
	s.cfgMu.Lock()
	s.embedder = embedding.NewComputer(embedding.Options{Provider: embedding.ProviderOllama, Model: "other-model", BaseURL: s.config.OllamaHost})
	s.cfgMu.Unlock()
	w := doJSON(h, "POST", "/api/indexes/idx/build", body, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "model mismatch") {
		t.Errorf("mismatch: %d %s", w.Code, w.Body.String())
	}

	// An embedder that cannot be reached makes the build itself fail: 500.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	s.cfgMu.Lock()
	s.embedder = embedding.NewComputer(embedding.Options{Provider: embedding.ProviderOllama, Model: "test-model", BaseURL: deadURL})
	s.cfgMu.Unlock()
	w = doJSON(h, "POST", "/api/indexes/fresh/build", body, nil)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "build failed") {
		t.Errorf("embedder down: %d %s", w.Code, w.Body.String())
	}
}

func TestAsk_ProviderFormatAndRoleOverrides(t *testing.T) {
	_, h, rec := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{
		"question":     "q",
		"llm_provider": "ollama",
		"format":       "json",
		"role":         "reviewer",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := rec.last(t)
	if body["format"] != "json" {
		t.Errorf("format not forwarded: %v", body["format"])
	}
	msgs, _ := body["messages"].([]any)
	var system string
	for _, m := range msgs {
		if mm, ok := m.(map[string]any); ok && mm["role"] == "system" {
			system, _ = mm["content"].(string)
		}
	}
	if !strings.Contains(system, "senior code reviewer") {
		t.Errorf("role prompt missing from system message: %q", system)
	}

	// An unknown role is ignored rather than failing the request.
	if w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q", "role": "no-such-role"}); w.Code != http.StatusOK {
		t.Errorf("unknown role: %d %s", w.Code, w.Body.String())
	}
}

// An index built with one embedding model, served by a server on another: the
// load refuses with 409 (the caller can rebuild or switch models), not 500.
func TestSearchModelMismatchIs409(t *testing.T) {
	s, h, _ := newIndexedServer(t)
	s.cfgMu.Lock()
	s.embedder = embedding.NewComputer(embedding.Options{Provider: embedding.ProviderOllama, Model: "other-model", BaseURL: s.config.OllamaHost})
	s.cfgMu.Unlock()
	w := doJSON(h, "POST", "/api/indexes/idx/search", `{"query":"alpha"}`, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "model mismatch") {
		t.Fatalf("search with a mismatched model: %d %s, want 409 model mismatch", w.Code, w.Body.String())
	}
}

// PATCH of a missing index is 404 and leaves no metadata lock file.
func TestPatchMissingIndexLeavesNoLockFile(t *testing.T) {
	_, h, cfg := newIndexedServer(t)
	for i := 0; i < 3; i++ {
		w := doJSON(h, "PATCH", "/api/indexes/nope", `{"description":"x"}`, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("PATCH missing: %d %s", w.Code, w.Body.String())
		}
	}
	if left, _ := filepath.Glob(filepath.Join(cfg.IndexDir, "nope*.lock")); len(left) != 0 {
		t.Fatalf("lock files left behind: %v", left)
	}
}
