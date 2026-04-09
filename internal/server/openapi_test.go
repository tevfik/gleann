package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newTestServer() *Server {
	return &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      ":8080",
		version:   "test-1.0.0",
	}
}

func TestHandleOpenAPISpec(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	w := httptest.NewRecorder()

	s.handleOpenAPISpec(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}

	var spec map[string]any
	if err := json.NewDecoder(w.Body).Decode(&spec); err != nil {
		t.Fatalf("failed to decode spec: %v", err)
	}

	// Validate top-level fields.
	if spec["openapi"] != "3.0.3" {
		t.Errorf("expected openapi 3.0.3, got %v", spec["openapi"])
	}

	info, ok := spec["info"].(map[string]any)
	if !ok {
		t.Fatal("missing info field")
	}
	if info["title"] != "Gleann API" {
		t.Errorf("expected title 'Gleann API', got %v", info["title"])
	}
	if info["version"] != "test-1.0.0" {
		t.Errorf("expected version 'test-1.0.0', got %v", info["version"])
	}

	// Validate paths exist.
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("missing paths field")
	}

	expectedPaths := []string{
		"/health",
		"/api/indexes",
		"/api/indexes/{name}",
		"/api/indexes/{name}/search",
		"/api/indexes/{name}/ask",
		"/api/indexes/{name}/build",
		"/api/graph/{name}",
		"/api/graph/{name}/query",
		"/api/graph/{name}/index",
	}
	for _, p := range expectedPaths {
		if _, ok := paths[p]; !ok {
			t.Errorf("missing path %q in spec", p)
		}
	}

	// Validate components/schemas exist.
	components, ok := spec["components"].(map[string]any)
	if !ok {
		t.Fatal("missing components field")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("missing schemas field")
	}

	expectedSchemas := []string{
		"SearchRequest", "SearchResponse", "SearchResult",
		"AskRequest", "AskResponse",
		"BuildRequest", "BuildItem",
		"GraphQueryRequest", "GraphQueryResponse", "GraphNode",
		"GraphStatsResponse", "GraphIndexRequest", "Error",
	}
	for _, s := range expectedSchemas {
		if _, ok := schemas[s]; !ok {
			t.Errorf("missing schema %q", s)
		}
	}
}

func TestHandleOpenAPISpecCacheable(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil)
	w := httptest.NewRecorder()

	s.handleOpenAPISpec(w, req)

	cc := w.Header().Get("Cache-Control")
	if cc == "" {
		t.Error("expected Cache-Control header")
	}
	if !strings.Contains(cc, "max-age") {
		t.Errorf("expected max-age in Cache-Control, got %q", cc)
	}
}

func TestHandleSwaggerUI(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	w := httptest.NewRecorder()

	s.handleSwaggerUI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected Content-Type text/html, got %q", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "swagger-ui") {
		t.Error("expected swagger-ui in HTML body")
	}
	if !strings.Contains(body, "/api/openapi.json") {
		t.Error("expected reference to /api/openapi.json in HTML body")
	}
	if !strings.Contains(body, "Gleann API Documentation") {
		t.Error("expected Gleann API Documentation in HTML title")
	}
}

func TestOpenAPISpecVersionInjection(t *testing.T) {
	s := &Server{
		config:    gleann.DefaultConfig(),
		searchers: make(map[string]*gleann.LeannSearcher),
		addr:      ":9090",
		version:   "v2.5.0-beta",
	}

	spec := s.openAPISpec()
	info := spec["info"].(map[string]any)
	if info["version"] != "v2.5.0-beta" {
		t.Errorf("expected version v2.5.0-beta, got %v", info["version"])
	}

	servers := spec["servers"].([]map[string]any)
	if !strings.Contains(servers[0]["url"].(string), ":9090") {
		t.Errorf("expected server URL to contain port :9090, got %v", servers[0]["url"])
	}
}

func TestOpenAPISpecValidJSON(t *testing.T) {
	s := newTestServer()
	spec := s.openAPISpec()

	// Round-trip through JSON to validate structure.
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("openAPI spec is not valid JSON: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("openAPI spec round-trip failed: %v", err)
	}

	// Ensure all $ref paths point to existing schemas.
	components := decoded["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)

	refs := findAllRefs(decoded)
	for _, ref := range refs {
		schemaName := strings.TrimPrefix(ref, "#/components/schemas/")
		if _, ok := schemas[schemaName]; !ok {
			t.Errorf("dangling $ref: %q (schema %q not found)", ref, schemaName)
		}
	}
}

// findAllRefs recursively finds all $ref values in a JSON-like structure.
func findAllRefs(v any) []string {
	var refs []string
	switch val := v.(type) {
	case map[string]any:
		if ref, ok := val["$ref"].(string); ok {
			refs = append(refs, ref)
		}
		for _, child := range val {
			refs = append(refs, findAllRefs(child)...)
		}
	case []any:
		for _, child := range val {
			refs = append(refs, findAllRefs(child)...)
		}
	}
	return refs
}

func TestOpenAPISpec_ErrorSchemas(t *testing.T) {
	s := newTestServer()
	spec := s.openAPISpec()

	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)

	for _, name := range []string{"ErrorResponse", "RateLimitError", "TimeoutError"} {
		schema, ok := schemas[name]
		if !ok {
			t.Errorf("missing schema: %q", name)
			continue
		}
		obj, ok := schema.(map[string]any)
		if !ok {
			t.Errorf("schema %q is not an object", name)
			continue
		}
		props, ok := obj["properties"].(map[string]any)
		if !ok {
			t.Errorf("schema %q has no properties", name)
			continue
		}
		// All error schemas should have an "error" field.
		if _, ok := props["error"]; !ok {
			t.Errorf("schema %q missing 'error' property", name)
		}
	}
}

func TestOpenAPISpec_BlocksTag(t *testing.T) {
	s := newTestServer()
	spec := s.openAPISpec()

	tags := spec["tags"].([]map[string]any)
	found := false
	for _, tag := range tags {
		if tag["name"] == "blocks" {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing 'blocks' tag in OpenAPI spec")
	}
}
