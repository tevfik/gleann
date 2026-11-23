package server

// Contract tests for the v1.20.2 OpenAPI work (cross-review v1202-release.qwen.md,
// findings F1/F2/F3): the spec builders must state what the handlers actually do,
// and the spec's security overrides must match the routes security.go serves
// without a token. Each test failed against the committed spec before the repair.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/auth"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
)

// jsonFieldNames returns the sorted JSON field names of a struct type: the
// names a handler's request/response struct actually accepts or emits.
func jsonFieldNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// specOperation returns one operation of the served spec map.
func specOperation(t *testing.T, s *Server, path, method string) map[string]any {
	t.Helper()
	paths, ok := s.openAPISpec()["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec has no paths map")
	}
	item, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("spec has no path %q", path)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("spec path %q has no %q operation", path, method)
	}
	return op
}

// responseSchema returns the application/json schema of a response, resolving
// a $ref against the spec's component schemas. It returns nil when the response
// documents no JSON body.
func responseSchema(t *testing.T, s *Server, responses map[string]any, code string) map[string]any {
	t.Helper()
	resp, ok := responses[code].(map[string]any)
	if !ok {
		t.Fatalf("spec lists no %s response", code)
	}
	content, ok := resp["content"].(map[string]any)
	if !ok {
		return nil
	}
	jsonContent, ok := content["application/json"].(map[string]any)
	if !ok {
		return nil
	}
	schema, ok := jsonContent["schema"].(map[string]any)
	if !ok {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		name, found := strings.CutPrefix(ref, "#/components/schemas/")
		if !found {
			t.Fatalf("unsupported $ref %q", ref)
		}
		schemas := s.openAPISpec()["components"].(map[string]any)["schemas"].(map[string]any)
		resolved, ok := schemas[name].(map[string]any)
		if !ok {
			t.Fatalf("dangling $ref %q", ref)
		}
		return resolved
	}
	return schema
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPatchIndexSpecMatchesHandler (review F1): PATCH /api/indexes/{name} must
// document the fields patchIndexRequest decodes (tags, description,
// mcp_exposed — there is no title field) and exactly the status codes
// handlePatchIndex answers: 200, 400 and 500. A missing index answers 500
// (UpdateIndexMeta fails), never 404.
func TestPatchIndexSpecMatchesHandler(t *testing.T) {
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}

	op := specOperation(t, s, "/api/indexes/{name}", "patch")

	// Request body: exactly the handler's fields, no phantom ones.
	body, ok := op["requestBody"].(map[string]any)
	if !ok {
		t.Fatal("PATCH operation documents no requestBody")
	}
	schema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("PATCH requestBody schema has no properties")
	}
	got, want := sortedKeys(props), jsonFieldNames(reflect.TypeOf(patchIndexRequest{}))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PATCH body fields: spec documents %v, handler accepts %v", got, want)
	}

	// Status codes: exercise the handler and require the spec to list exactly
	// the codes it can answer.
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/indexes/{name}", s.handlePatchIndex)
	call := func(name, body string) int {
		req := httptest.NewRequest(http.MethodPatch, "/api/indexes/"+name, strings.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code
	}
	observed := map[string]bool{}
	record := func(what string, code, want int) {
		if code != want {
			t.Fatalf("%s: handler answered %d, expected %d", what, code, want)
		}
		observed[strconv.Itoa(code)] = true
	}
	record("missing index", call("no-such-index", `{}`), http.StatusInternalServerError)
	record("undecodable body", call("no-such-index", `{`), http.StatusBadRequest)

	idxDir := filepath.Join(dir, "okindex")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(gleann.IndexMeta{Name: "okindex"})
	if err := os.WriteFile(filepath.Join(idxDir, "okindex.meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	record("valid patch", call("okindex", `{"tags":["review"],"mcp_exposed":true}`), http.StatusOK)

	responses, ok := op["responses"].(map[string]any)
	if !ok {
		t.Fatal("PATCH operation documents no responses")
	}
	specCodes := sortedKeys(responses)
	var obsCodes []string
	for k := range observed {
		obsCodes = append(obsCodes, k)
	}
	sort.Strings(obsCodes)
	if strings.Join(specCodes, ",") != strings.Join(obsCodes, ",") {
		t.Errorf("PATCH responses: spec lists %v, handler answers %v", specCodes, obsCodes)
	}
}

// specPlaceholder matches OpenAPI path templates such as {name} or {id}.
var specPlaceholder = regexp.MustCompile(`\{[^}]*\}`)

// isSpecMethod reports whether k is an OpenAPI operation key.
func isSpecMethod(k string) bool {
	switch strings.ToLower(k) {
	case "get", "put", "post", "delete", "patch", "head", "options", "trace":
		return true
	}
	return false
}

// TestSpecSecurityMatchesPublicRoutes (review F3): every route the server
// serves without a token must carry an empty security list in the spec, and no
// protected route may carry one. The public set is derived from the serving
// code itself, not a hand list: each spec route runs through securityMiddleware
// (which consults requiredScope, security.go) with a real token store and no
// credentials — reaching the inner handler means the route is public.
func TestSpecSecurityMatchesPublicRoutes(t *testing.T) {
	s := &Server{version: "test", addr: ":8080"}
	paths, ok := s.openAPISpec()["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec has no paths map")
	}

	st, err := auth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := securityMiddleware(newSecurityConfig("127.0.0.1:8081", st, nil), inner)

	publicCount := 0
	for path, item := range paths {
		ops, ok := item.(map[string]any)
		if !ok {
			t.Errorf("path %q is not a map", path)
			continue
		}
		for method, op := range ops {
			if !isSpecMethod(method) {
				continue
			}
			concrete := specPlaceholder.ReplaceAllString(path, "x")
			req := httptest.NewRequest(strings.ToUpper(method), concrete, nil)
			req.Host = "127.0.0.1:8081"
			w := httptest.NewRecorder()
			mw.ServeHTTP(w, req)
			public := w.Code == http.StatusOK
			if public {
				publicCount++
			}

			opm, ok := op.(map[string]any)
			if !ok {
				t.Errorf("%s %s: operation is not a map", method, path)
				continue
			}
			sec, hasSecurity := opm["security"]
			empty := false
			if hasSecurity {
				switch v := sec.(type) {
				case []map[string]any:
					empty = len(v) == 0
				case []any:
					empty = len(v) == 0
				default:
					t.Errorf("%s %s: security has unexpected type %T", method, path, sec)
				}
			}
			switch {
			case public && !empty:
				t.Errorf("%s %s is served without a token (security.go) but has no empty security list; it inherits the global bearerAuth",
					strings.ToUpper(method), path)
			case !public && empty:
				t.Errorf("%s %s requires a token but the spec marks it public with an empty security list",
					strings.ToUpper(method), path)
			}
		}
	}
	if publicCount == 0 {
		t.Fatal("no public route found in the spec; the walker is broken")
	}
}

// TestSpecConversationAndConfigContracts (review F2): the smaller contract gaps
// of the new operations — missing status codes and 200 body schemas, and a
// plugin status enum value the handler never emits.
func TestSpecConversationAndConfigContracts(t *testing.T) {
	s := &Server{version: "test", addr: ":8080"}

	// DELETE /api/conversations: handleClearConversations answers 500 when the
	// store listing fails and 200 with {"status":"deleted","count":N}.
	del := specOperation(t, s, "/api/conversations", "delete")
	delResp := del["responses"].(map[string]any)
	if _, ok := delResp["500"]; !ok {
		t.Error("DELETE /api/conversations: spec omits 500 (handleClearConversations answers it when store.List fails)")
	}
	if schema := responseSchema(t, s, delResp, "200"); schema == nil {
		t.Error("DELETE /api/conversations: 200 documents no body; handler returns {status, count}")
	} else {
		props, _ := schema["properties"].(map[string]any)
		for _, k := range []string{"status", "count"} {
			if _, ok := props[k]; !ok {
				t.Errorf("DELETE /api/conversations: 200 body schema lacks %q", k)
			}
		}
	}

	// PUT /api/conversations/{id}: handleUpdateConversation answers 400 for an
	// undecodable body, 500 when saving fails, and 200 with the full
	// conversation object.
	put := specOperation(t, s, "/api/conversations/{id}", "put")
	putResp := put["responses"].(map[string]any)
	for _, code := range []string{"400", "500"} {
		if _, ok := putResp[code]; !ok {
			t.Errorf("PUT /api/conversations/{id}: spec omits %s (handleUpdateConversation answers it)", code)
		}
	}
	checkConversationBody := func(what string, responses map[string]any) {
		schema := responseSchema(t, s, responses, "200")
		if schema == nil {
			t.Errorf("%s: 200 documents no body; handler returns the full conversation", what)
			return
		}
		props, _ := schema["properties"].(map[string]any)
		got, want := sortedKeys(props), jsonFieldNames(reflect.TypeOf(conversations.Conversation{}))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: 200 body fields %v, conversations.Conversation emits %v", what, got, want)
		}
	}
	checkConversationBody("PUT /api/conversations/{id}", putResp)
	get := specOperation(t, s, "/api/conversations/{id}", "get")
	checkConversationBody("GET /api/conversations/{id}", get["responses"].(map[string]any))

	// GET /api/config: handleGetConfig answers 500 when encoding fails.
	cfgOp := specOperation(t, s, "/api/config", "get")
	cfgResp := cfgOp["responses"].(map[string]any)
	if _, ok := cfgResp["500"]; !ok {
		t.Error("GET /api/config: spec omits 500 (handleGetConfig answers it when json.Marshal fails)")
	}

	// GET /api/plugins: handleListPlugins only ever sets not_installed or
	// installed (plugin_handler.go); "running" is never emitted.
	plug := specOperation(t, s, "/api/plugins", "get")
	plugSchema := responseSchema(t, s, plug["responses"].(map[string]any), "200")
	if plugSchema == nil {
		t.Fatal("GET /api/plugins: 200 documents no body")
	}
	status := plugSchema["properties"].(map[string]any)["plugins"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["status"].(map[string]any)
	enum, _ := status["enum"].([]string)
	sorted := append([]string(nil), enum...)
	sort.Strings(sorted)
	if strings.Join(sorted, ",") != "installed,not_installed" {
		t.Errorf("GET /api/plugins: status enum %v; handleListPlugins only emits not_installed or installed", enum)
	}
}
