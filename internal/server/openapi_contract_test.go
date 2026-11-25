package server

// Contract tests for the v1.20.2 OpenAPI work (cross-review v1202-release.qwen.md,
// findings F1/F2/F3): the spec builders must state what the handlers actually do,
// and the spec's security overrides must match the routes security.go serves
// without a token. Each test failed against the committed spec before the repair.

import (
	"io/fs"

	"encoding/json"
	"fmt"
	"github.com/tevfik/gleann/internal/metafault"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
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
// handlePatchIndex answers: 200, 400, 403 (restricted client), 404 (missing index) and 500 (any other
// metadata failure, exercised below with an unreadable metadata file).
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
	callHdr := func(name, body string, hdr map[string]string) int {
		return serve(mux, http.MethodPatch, "/api/indexes/"+name, body, hdr).Code
	}
	call := func(name, body string) int { return callHdr(name, body, nil) }
	observed := map[string]bool{}
	record := func(what string, code, want int) {
		if code != want {
			t.Fatalf("%s: handler answered %d, expected %d", what, code, want)
		}
		observed[strconv.Itoa(code)] = true
	}
	record("missing index", call("no-such-index", `{}`), http.StatusNotFound)
	record("restricted client", callHdr("okindex", `{}`, map[string]string{"X-Gleann-Target": "other"}), http.StatusForbidden)
	record("undecodable body", call("no-such-index", `{`), http.StatusBadRequest)

	idxDir := filepath.Join(dir, "okindex")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(gleann.IndexMeta{Name: "okindex"})
	if err := os.WriteFile(filepath.Join(idxDir, "okindex.meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	badDir := filepath.Join(dir, "badindex")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "badindex.meta.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	record("corrupt metadata", call("badindex", `{}`), http.StatusInternalServerError)
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

// patchTestServer builds a server over a temp index dir holding one index
// ("okindex") and a plain file named "afile", with the PATCH and GET routes.
func patchTestServer(t *testing.T) (*Server, *http.ServeMux, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	idxDir := filepath.Join(dir, "okindex")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(gleann.IndexMeta{Name: "okindex"})
	if err := os.WriteFile(filepath.Join(idxDir, "okindex.meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/indexes/{name}", s.handlePatchIndex)
	mux.HandleFunc("GET /api/indexes/{name}", s.handleGetIndex)
	return s, mux, dir
}

func serve(mux *http.ServeMux, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// Concurrent PATCHes of one index must all answer 200 and leave valid metadata.
func TestPatchIndexConcurrent(t *testing.T) {
	_, mux, dir := patchTestServer(t)
	const n = 100
	var wg sync.WaitGroup
	type result struct {
		code int
		body string
	}
	codes := make(chan result, n)
	var bodies []string
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := serve(mux, http.MethodPatch, "/api/indexes/okindex", fmt.Sprintf(`{"description":"d%d"}`, i), nil)
			codes <- result{w.Code, w.Body.String()}
		}(i)
	}
	wg.Wait()
	close(codes)
	got := map[int]int{}
	for r := range codes {
		got[r.code]++
		if r.code != http.StatusOK && len(bodies) < 5 {
			bodies = append(bodies, r.body)
		}
	}
	for _, b := range bodies {
		t.Logf("non-200 response body: %s", b)
	}
	if got[http.StatusOK] != n {
		t.Fatalf("status codes of %d concurrent PATCHes: %v, want all 200 (bodies logged above)", n, got)
	}
	m, err := gleann.GetIndexMeta(dir, "okindex")
	if err != nil {
		t.Fatalf("metadata invalid after concurrent PATCHes: %v", err)
	}
	if !strings.HasPrefix(m.Description, "d") {
		t.Errorf("final description %q is none of the patches", m.Description)
	}
}

// A plain file named like the index answers 404, as GET does.
func TestPatchIndexPlainFileIs404(t *testing.T) {
	_, mux, _ := patchTestServer(t)
	if c := serve(mux, http.MethodGet, "/api/indexes/afile", "", nil).Code; c != http.StatusNotFound {
		t.Fatalf("GET premise: %d", c)
	}
	if c := serve(mux, http.MethodPatch, "/api/indexes/afile", `{}`, nil).Code; c != http.StatusNotFound {
		t.Errorf("PATCH on a plain file: %d, want 404", c)
	}
}

// PATCH enforces the same access restriction as GET, before the existence
// check, so a restricted client learns nothing about other indexes.
func TestPatchIndexChecksAccess(t *testing.T) {
	_, mux, _ := patchTestServer(t)
	hdr := map[string]string{"X-Gleann-Target": "other"}
	for _, name := range []string{"okindex", "ghost"} {
		if c := serve(mux, http.MethodPatch, "/api/indexes/"+name, `{"description":"x"}`, hdr).Code; c != http.StatusForbidden {
			t.Errorf("PATCH %s with foreign target: %d, want 403", name, c)
		}
	}
	if c := serve(mux, http.MethodPatch, "/api/indexes/okindex", `{"description":"x"}`, map[string]string{"X-Gleann-Target": "okindex"}).Code; c != http.StatusOK {
		t.Errorf("PATCH with matching target: %d, want 200", c)
	}
}

// Pins the GLEANN_TAGS behaviour PATCH shares with GET (checkIndexAccess): an
// index with another tag is 403, a missing one 404 (so existence is visible to
// a tag-restricted client; a follow-up), an allowed one 200, a corrupt one 500.
func TestPatchIndexGleannTags(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "team")
	_, mux, dir := patchTestServer(t)
	write := func(name string, m gleann.IndexMeta) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, name, name+".meta.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mine", gleann.IndexMeta{Name: "mine", Tags: []string{"team"}})
	write("secret", gleann.IndexMeta{Name: "secret", Tags: []string{"other"}})
	if err := os.WriteFile(filepath.Join(dir, "okindex", "okindex.meta.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"mine", 200}, {"secret", 403}, {"ghost", 403}, {"okindex", 403}} {
		patch := serve(mux, http.MethodPatch, "/api/indexes/"+tc.name, `{"description":"x"}`, nil).Code
		if patch != tc.want {
			t.Errorf("PATCH %s: %d, want %d", tc.name, patch, tc.want)
		}
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"secret", 403}, {"ghost", 403}} {
		if got := serve(mux, http.MethodGet, "/api/indexes/"+tc.name, "", nil).Code; got != tc.want {
			t.Errorf("GET %s: %d, want %d (PATCH must match GET)", tc.name, got, tc.want)
		}
	}
}

// A client restricted by GLEANN_TAGS must not be able to tell a missing index
// from one it may not see: same status and same body, on every method and on
// the other index-scoped routes. An unrestricted client still gets 404.
func TestGleannTagsDoNotLeakIndexExistence(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "team")
	s, mux, dir := patchTestServer(t)
	mux.HandleFunc("POST /api/indexes/{name}/search", s.handleSearch)
	mux.HandleFunc("POST /api/indexes/{name}/ask", s.handleAsk)
	mux.HandleFunc("GET /api/indexes/{name}/passages", s.handleListPassages)
	mux.HandleFunc("GET /api/indexes/{name}/sections", s.handleListSections)
	if err := os.MkdirAll(filepath.Join(dir, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(gleann.IndexMeta{Name: "secret", Tags: []string{"other"}})
	if err := os.WriteFile(filepath.Join(dir, "secret", "secret.meta.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/indexes/%s", ""},
		{http.MethodPatch, "/api/indexes/%s", `{"description":"x"}`},
		{http.MethodPost, "/api/indexes/%s/search", `{"query":"q"}`},
		{http.MethodPost, "/api/indexes/%s/ask", `{"question":"q"}`},
		{http.MethodGet, "/api/indexes/%s/passages", ""},
		{http.MethodGet, "/api/indexes/%s/sections", ""},
	} {
		forbidden := serve(mux, rt.method, fmt.Sprintf(rt.path, "secret"), rt.body, nil)
		missing := serve(mux, rt.method, fmt.Sprintf(rt.path, "ghost"), rt.body, nil)
		if forbidden.Code != http.StatusForbidden {
			t.Errorf("%s %s secret: %d, want 403", rt.method, rt.path, forbidden.Code)
		}
		if missing.Code != forbidden.Code {
			t.Errorf("%s %s: missing index %d, forbidden index %d; must be equal", rt.method, rt.path, missing.Code, forbidden.Code)
		}
		// The names differ in the message; normalise them.
		fb := strings.ReplaceAll(forbidden.Body.String(), "secret", "NAME")
		mb := strings.ReplaceAll(missing.Body.String(), "ghost", "NAME")
		if fb != mb {
			t.Errorf("%s %s: bodies differ: forbidden %q, missing %q", rt.method, rt.path, fb, mb)
		}
	}
}

func TestMissingIndexIs404WhenUnrestricted(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, mux, _ := patchTestServer(t)
	for _, m := range []string{http.MethodGet, http.MethodPatch} {
		if c := serve(mux, m, "/api/indexes/ghost", `{"description":"x"}`, nil).Code; c != http.StatusNotFound {
			t.Errorf("%s ghost unrestricted: %d, want 404", m, c)
		}
	}
}

// A not-exist error from the metadata WRITE (temp file vanishing, directory
// removed mid-update) is a server fault, never "index not found".
func TestPatchIndexWriteNotExistIs500(t *testing.T) {
	_, mux, _ := patchTestServer(t)
	t.Cleanup(metafault.Set(func(string) error { return fs.ErrNotExist }))
	if c := serve(mux, http.MethodPatch, "/api/indexes/okindex", `{"description":"x"}`, nil).Code; c != http.StatusInternalServerError {
		t.Errorf("PATCH with a not-exist write error: %d, want 500", c)
	}
}

// TestSearchFilterSpecMatchesHandler: the SearchRequest metadata_filters items
// must document exactly the keys of gleann.MetadataFilter (the spec once said
// "op" while the handler reads "operator", so a spec-following request matched
// nothing), and the handler must refuse an unknown key or operator with 400.
func TestSearchFilterSpecMatchesHandler(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}

	schemas := s.openAPISpec()["components"].(map[string]any)["schemas"].(map[string]any)
	req := schemas["SearchRequest"].(map[string]any)["properties"].(map[string]any)
	items := req["metadata_filters"].(map[string]any)["items"].(map[string]any)
	props := items["properties"].(map[string]any)
	if got, want := sortedKeys(props), jsonFieldNames(reflect.TypeOf(gleann.MetadataFilter{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("spec filter keys %v, handler reads %v", got, want)
	}
	enum, _ := props["operator"].(map[string]any)["enum"].([]string)
	if len(enum) == 0 {
		t.Fatal("spec lists no operator enum")
	}
	for _, o := range enum {
		mf := gleann.MetadataFilter{Field: "f", Operator: gleann.FilterOperator(o)}
		if o == "in" || o == "nin" {
			mf.Value = []any{"x"}
		}
		if err := mf.Validate(); err != nil {
			t.Errorf("spec operator %q is refused by the server: %v", o, err)
		}
	}

	multi := schemas["MultiSearchRequest"].(map[string]any)["properties"].(map[string]any)["metadata_filters"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if got, want := sortedKeys(multi), jsonFieldNames(reflect.TypeOf(gleann.MetadataFilter{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("multi-search spec filter keys %v, handler reads %v", got, want)
	}
	mr := httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(`{"query":"q","metadata_filters":[{"field":"kind","op":"eq"}]}`))
	mw := httptest.NewRecorder()
	s.handleMultiSearch(mw, mr)
	if mw.Code != http.StatusBadRequest {
		t.Errorf("multi-search with op: status %d, want 400 (%s)", mw.Code, mw.Body.String())
	}
	mr = httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(`{"query":"q","metadata_filters":[{"field":"kind","value":"x"}]}`))
	mw = httptest.NewRecorder()
	s.handleMultiSearch(mw, mr)
	if mw.Code != http.StatusBadRequest {
		t.Errorf("multi-search without operator: status %d, want 400 (%s)", mw.Code, mw.Body.String())
	}

	for name, body := range map[string]string{
		"op instead of operator": `{"query":"q","metadata_filters":[{"field":"kind","op":"eq","value":"x"}]}`,
		"missing operator":       `{"query":"q","metadata_filters":[{"field":"kind","value":"x"}]}`,
		"unknown operator":       `{"query":"q","metadata_filters":[{"field":"kind","operator":"equals","value":"x"}]}`,
		"missing field":          `{"query":"q","metadata_filters":[{"operator":"eq","value":"x"}]}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/indexes/test/search", strings.NewReader(body))
		r.SetPathValue("name", "test")
		w := httptest.NewRecorder()
		s.handleSearch(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", name, w.Code, w.Body.String())
		}
	}
}

// REST rejects a null or non-array in/nin operand with 400, like the CLI.
func TestSearchRejectsNullInOperand(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	for name, body := range map[string]string{
		"null in":    `{"query":"q","metadata_filters":[{"field":"tags","operator":"in","value":null}]}`,
		"string nin": `{"query":"q","metadata_filters":[{"field":"tags","operator":"nin","value":"go"}]}`,
		"missing in": `{"query":"q","metadata_filters":[{"field":"tags","operator":"in"}]}`,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/indexes/test/search", strings.NewReader(body))
		r.SetPathValue("name", "test")
		w := httptest.NewRecorder()
		s.handleSearch(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (body %s)", name, w.Code, w.Body.String())
		}
	}
}
