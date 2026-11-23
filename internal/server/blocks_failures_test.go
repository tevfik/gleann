package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/auth"
	"github.com/tevfik/gleann/pkg/memory"
)

// newFailuresTestServer builds the blocks test server and registers the failure
// routes on its mux.
func newFailuresTestServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	s, mux := newBlocksTestServer(t)
	mux.HandleFunc("POST /api/blocks/failures", s.handleAddFailure)
	mux.HandleFunc("GET /api/blocks/failures", s.handleListFailures)
	return s, mux
}

func doFailureAdd(t *testing.T, mux *http.ServeMux, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/blocks/failures", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func doFailureGet(t *testing.T, mux *http.ServeMux, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func decodeFailureList(t *testing.T, w *httptest.ResponseRecorder) ([]memory.Block, int) {
	t.Helper()
	var resp struct {
		Failures []memory.Block `json:"failures"`
		Count    int            `json:"count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode failure list: %v (body %s)", err, w.Body.String())
	}
	if resp.Failures == nil {
		t.Fatalf("failures must be a non-nil array, got null (body %s)", w.Body.String())
	}
	return resp.Failures, resp.Count
}

// ── POST /api/blocks/failures ─────────────────────────────────────────────────

func TestHandleAddFailure_CreatedAndScoped(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	w := doFailureAdd(t, mux, `{"command":"go test","error":"boom","fix":"retry","scope":"repo-a"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var b memory.Block
	if err := json.NewDecoder(w.Body).Decode(&b); err != nil {
		t.Fatalf("decode block: %v", err)
	}
	if b.Label != memory.FailureLabel || b.Scope != "repo-a" {
		t.Fatalf("block = %+v, want failure label scope repo-a", b)
	}
	if b.Confirms != 1 {
		t.Fatalf("Confirms = %d, want 1", b.Confirms)
	}
}

func TestHandleAddFailure_RequiresCommandAndError(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	for _, body := range []string{
		`{"error":"boom"}`,
		`{"command":"go test"}`,
		`{"command":"  ","error":"boom"}`,
		`{}`,
	} {
		w := doFailureAdd(t, mux, body, nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %s: expected 400, got %d", body, w.Code)
		}
	}
}

func TestHandleAddFailure_ConfirmReturns200(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	body := `{"command":"go test","error":"panic at /a/b.go:42","fix":"nil check","scope":"repo-a"}`
	w := doFailureAdd(t, mux, body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("first record: %d", w.Code)
	}
	// Same failure differing only in line number confirms.
	w = doFailureAdd(t, mux, `{"command":"go test","error":"panic at /x/y.go:99","fix":"nil check","scope":"repo-a"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("confirm expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var b memory.Block
	json.NewDecoder(w.Body).Decode(&b)
	if b.Confirms != 2 {
		t.Fatalf("Confirms = %d, want 2", b.Confirms)
	}
}

func TestHandleAddFailure_ScopeResolution(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	// Via query ?scope=.
	r := httptest.NewRequest(http.MethodPost, "/api/blocks/failures?scope=repo-q", strings.NewReader(`{"command":"go test","error":"boom q"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("query scope: %d %s", rec.Code, rec.Body.String())
	}
	var b memory.Block
	json.NewDecoder(rec.Body).Decode(&b)
	if b.Scope != "repo-q" {
		t.Fatalf("query scope = %q, want repo-q", b.Scope)
	}

	// Via X-Gleann-Target header.
	w := doFailureAdd(t, mux, `{"command":"go build","error":"bam"}`, map[string]string{"X-Gleann-Target": "repo-h"})
	if w.Code != http.StatusCreated {
		t.Fatalf("header scope: %d %s", w.Code, w.Body.String())
	}
	json.NewDecoder(w.Body).Decode(&b)
	if b.Scope != "repo-h" {
		t.Fatalf("header scope = %q, want repo-h", b.Scope)
	}
}

func TestHandleAddFailure_TargetMismatch403(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	// Header vs body mismatch.
	w := doFailureAdd(t, mux, `{"command":"go test","error":"boom","scope":"repo-b"}`, map[string]string{"X-Gleann-Target": "repo-a"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("header/body mismatch expected 403, got %d: %s", w.Code, w.Body.String())
	}
	// Header vs query mismatch.
	r := httptest.NewRequest(http.MethodPost, "/api/blocks/failures?scope=repo-b", strings.NewReader(`{"command":"go test","error":"boom"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Gleann-Target", "repo-a")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("header/query mismatch expected 403, got %d", rec.Code)
	}
}

func TestHandleAddFailure_RepoDefaultsScope(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	w := doFailureAdd(t, mux, `{"command":"go test","error":"boom","repo":"github.com/org/repo"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var b memory.Block
	json.NewDecoder(w.Body).Decode(&b)
	if b.Scope != "github.com/org/repo" {
		t.Fatalf("scope should default from repo, got %q", b.Scope)
	}
}

// ── GET /api/blocks/failures ──────────────────────────────────────────────────

func seedFailures(t *testing.T, mux *http.ServeMux) {
	t.Helper()
	for _, body := range []string{
		`{"command":"go test ./pkg/a","error":"boom a","fix":"fix a","scope":"repo-a","paths":["pkg/a/alpha.go"],"symbols":["github.com/org/repo-a/pkg.Alpha"]}`,
		`{"command":"go build ./pkg/b","error":"boom b","fix":"fix b","scope":"repo-b","paths":["pkg/b/beta.go"],"symbols":["github.com/org/repo-b/pkg.Beta"]}`,
	} {
		if w := doFailureAdd(t, mux, body, nil); w.Code != http.StatusCreated {
			t.Fatalf("seed: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestHandleListFailures_AllAndScope(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	seedFailures(t, mux)

	// No scope → all.
	w := doFailureGet(t, mux, "/api/blocks/failures", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list all: %d", w.Code)
	}
	fs, n := decodeFailureList(t, w)
	if n != 2 || len(fs) != 2 {
		t.Fatalf("all failures = %d, want 2", n)
	}

	// ?scope=repo-a → only repo-a.
	w = doFailureGet(t, mux, "/api/blocks/failures?scope=repo-a", nil)
	fs, n = decodeFailureList(t, w)
	if n != 1 || fs[0].Scope != "repo-a" {
		t.Fatalf("repo-a failures = %v, want 1 repo-a", fs)
	}

	// X-Gleann-Target → repo-b.
	w = doFailureGet(t, mux, "/api/blocks/failures", map[string]string{"X-Gleann-Target": "repo-b"})
	fs, n = decodeFailureList(t, w)
	if n != 1 || fs[0].Scope != "repo-b" {
		t.Fatalf("repo-b failures = %v, want 1 repo-b", fs)
	}
}

func TestHandleListFailures_Filters(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	seedFailures(t, mux)

	// command filter.
	w := doFailureGet(t, mux, "/api/blocks/failures?command=go%20test", nil)
	fs, _ := decodeFailureList(t, w)
	if len(fs) != 1 || fs[0].Scope != "repo-a" {
		t.Fatalf("command filter = %v", fs)
	}
	// paths (comma-separated).
	w = doFailureGet(t, mux, "/api/blocks/failures?paths=pkg/b/beta.go", nil)
	fs, _ = decodeFailureList(t, w)
	if len(fs) != 1 || fs[0].Scope != "repo-b" {
		t.Fatalf("paths filter = %v", fs)
	}
	// repeated path.
	w = doFailureGet(t, mux, "/api/blocks/failures?path=pkg/a/alpha.go", nil)
	fs, _ = decodeFailureList(t, w)
	if len(fs) != 1 || fs[0].Scope != "repo-a" {
		t.Fatalf("path filter = %v", fs)
	}
	// symbols (comma-separated) — distinct from paths.
	w = doFailureGet(t, mux, "/api/blocks/failures?symbols=Alpha", nil)
	fs, _ = decodeFailureList(t, w)
	if len(fs) != 1 || fs[0].Scope != "repo-a" {
		t.Fatalf("symbols filter = %v", fs)
	}
	// repeated symbol.
	w = doFailureGet(t, mux, "/api/blocks/failures?symbol=Beta", nil)
	fs, _ = decodeFailureList(t, w)
	if len(fs) != 1 || fs[0].Scope != "repo-b" {
		t.Fatalf("symbol filter = %v", fs)
	}
	// scope + symbol combined: repo-a must not see repo-b symbol.
	w = doFailureGet(t, mux, "/api/blocks/failures?scope=repo-a&symbol=Beta", nil)
	fs, _ = decodeFailureList(t, w)
	if len(fs) != 0 {
		t.Fatalf("repo-a + Beta symbol should be empty, got %v", fs)
	}
}

func TestHandleListFailures_TargetMismatch403(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	w := doFailureGet(t, mux, "/api/blocks/failures?scope=repo-b", map[string]string{"X-Gleann-Target": "repo-a"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestHandleListFailures_EmptyIsArray(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	w := doFailureGet(t, mux, "/api/blocks/failures?scope=repo-none", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	raw := w.Body.String()
	fs, n := decodeFailureList(t, w)
	if n != 0 || len(fs) != 0 {
		t.Fatalf("empty scope = %d, want 0", n)
	}
	if !strings.Contains(raw, `"failures":[]`) {
		t.Fatalf("failures should serialize as [], got %s", raw)
	}
}

// ── GET /api/blocks/context (task-scoped) ─────────────────────────────────────

func TestHandleBlockContext_TaskFailures(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	seedFailures(t, mux)

	getContext := func(target string) map[string]any {
		t.Helper()
		w := doFailureGet(t, mux, target, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("context %s: %d %s", target, w.Code, w.Body.String())
		}
		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode context: %v", err)
		}
		return resp
	}
	rendered := func(resp map[string]any) string {
		s, _ := resp["rendered"].(string)
		return s
	}

	// By command.
	resp := getContext("/api/blocks/context?command=go%20test&scope=repo-a")
	if !strings.Contains(rendered(resp), "<failure_memory>") || !strings.Contains(rendered(resp), "fix a") {
		t.Fatalf("command context missing failure:\n%s", rendered(resp))
	}
	// By symbols with paths empty.
	resp = getContext("/api/blocks/context?symbols=Alpha&scope=repo-a")
	if !strings.Contains(rendered(resp), "fix a") {
		t.Fatalf("symbols-only context missing failure:\n%s", rendered(resp))
	}
	// By paths.
	resp = getContext("/api/blocks/context?paths=pkg/b/beta.go&scope=repo-b")
	if !strings.Contains(rendered(resp), "fix b") {
		t.Fatalf("paths context missing failure:\n%s", rendered(resp))
	}
	// Scope isolation: repo-a context must not surface repo-b failure by symbol.
	resp = getContext("/api/blocks/context?symbols=Beta&scope=repo-a")
	if strings.Contains(rendered(resp), "fix b") {
		t.Fatalf("repo-a context leaked repo-b failure:\n%s", rendered(resp))
	}
	// No task → no failure section.
	resp = getContext("/api/blocks/context?scope=repo-a")
	if strings.Contains(rendered(resp), "<failure_memory>") {
		t.Fatalf("plain context should have no failure section:\n%s", rendered(resp))
	}
}

// ── auth (security middleware) ────────────────────────────────────────────────

func TestFailureRoutes_AuthScopes(t *testing.T) {
	s, mux := newFailuresTestServer(t)
	st, err := auth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admin, _, _ := st.EnsureAdmin()
	q, _ := st.Create("q", auth.ScopeQuery)
	h := securityMiddleware(newSecurityConfig("127.0.0.1:8081", st, nil), mux)

	doAuth := func(method, path, body, tok string) *httptest.ResponseRecorder {
		var r *http.Request
		if body != "" {
			r = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
			r.Header.Set("Content-Type", "application/json")
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Host = "127.0.0.1:8081"
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	addBody := `{"command":"go test","error":"boom","fix":"retry"}`
	// POST requires admin.
	if w := doAuth("POST", "/api/blocks/failures", addBody, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("POST no token = %d, want 401", w.Code)
	}
	if w := doAuth("POST", "/api/blocks/failures", addBody, q); w.Code != http.StatusForbidden {
		t.Errorf("POST query token = %d, want 403", w.Code)
	}
	if w := doAuth("POST", "/api/blocks/failures", addBody, admin); w.Code != http.StatusCreated {
		t.Errorf("POST admin = %d, want 201 (%s)", w.Code, w.Body.String())
	}
	// GET requires query scope (admin also allowed).
	if w := doAuth("GET", "/api/blocks/failures", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("GET no token = %d, want 401", w.Code)
	}
	if w := doAuth("GET", "/api/blocks/failures", "", q); w.Code != http.StatusOK {
		t.Errorf("GET query token = %d, want 200", w.Code)
	}
	if w := doAuth("GET", "/api/blocks/failures", "", admin); w.Code != http.StatusOK {
		t.Errorf("GET admin = %d, want 200", w.Code)
	}
	_ = s
}

// ── v2 repair, finding 3: POST target comparison after normalization ─────────

// Header/query target and body scope naming the same repo with different
// slash spellings must agree (201), while a genuine mismatch stays 403.
func TestV2AddFailureNormalizedTargetMatch(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	for _, c := range []struct{ hdr, body string }{
		{"repo-a", `{"command":"go build","error":"norm probe 1","scope":"repo-a/"}`},
		{"repo-a/", `{"command":"go build","error":"norm probe 2","scope":"repo-a"}`},
	} {
		w := doFailureAdd(t, mux, c.body, map[string]string{"X-Gleann-Target": c.hdr})
		if w.Code != http.StatusCreated {
			t.Fatalf("header %q + body %s: expected 201, got %d: %s", c.hdr, c.body, w.Code, w.Body.String())
		}
		var b memory.Block
		if err := json.NewDecoder(w.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if b.Scope != "repo-a" {
			t.Fatalf("stored scope = %q, want normalized repo-a", b.Scope)
		}
	}
	// Query spelling is normalized against the body too.
	r := httptest.NewRequest(http.MethodPost, "/api/blocks/failures?scope=repo-a%2F",
		strings.NewReader(`{"command":"go build","error":"norm probe 3","scope":"repo-a"}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusCreated {
		t.Fatalf("query repo-a/ + body repo-a: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	// Genuine mismatch still 403.
	w := doFailureAdd(t, mux, `{"command":"go build","error":"norm probe 4","scope":"repo-b"}`,
		map[string]string{"X-Gleann-Target": "repo-a"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("repo-a vs repo-b must stay 403, got %d", w.Code)
	}
}

// ── v2 repair, finding 2: slash-normalized read scopes over REST ─────────────

func v2ContextRendered(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("context: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Rendered string `json:"rendered"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode context: %v", err)
	}
	return resp.Rendered
}

func TestV2ListFailuresScopeSlashBasename(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	w := doFailureAdd(t, mux,
		`{"command":"go test ./pkg/x","error":"zz slash probe","fix":"retry","scope":"repo-a","paths":["pkg/main.go"]}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	for _, scope := range []string{"repo-a", "repo-a%2F", "%2Frepo-a"} {
		w := doFailureGet(t, mux, "/api/blocks/failures?scope="+scope+"&path=main.go", nil)
		fs, n := decodeFailureList(t, w)
		if n != 1 || len(fs) != 1 {
			t.Fatalf("GET failures?scope=%s&path=main.go count = %d, want 1 (basename fallback off?)", scope, n)
		}
		w = doFailureGet(t, mux, "/api/blocks/failures?scope="+scope, nil)
		if _, n := decodeFailureList(t, w); n != 1 {
			t.Fatalf("GET failures?scope=%s count = %d, want 1", scope, n)
		}
		w = doFailureGet(t, mux, "/api/blocks/context?scope="+scope+"&path=main.go", nil)
		if rendered := v2ContextRendered(t, w); !strings.Contains(rendered, "retry") {
			t.Fatalf("GET context?scope=%s&path=main.go missing failure:\n%s", scope, rendered)
		}
	}
}

// ── v2 repair, finding 4: all/* sentinel parity over REST ────────────────────

func TestV2FailureScopeAllSentinelREST(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	seedFailures(t, mux)

	// GET failures: all/* (case-insensitive, trimmed) lists every scope.
	for _, scope := range []string{"all", "*", "ALL", "%20all%20"} {
		w := doFailureGet(t, mux, "/api/blocks/failures?scope="+scope, nil)
		if _, n := decodeFailureList(t, w); n != 2 {
			t.Fatalf("GET failures?scope=%s count = %d, want 2 (all scopes)", scope, n)
		}
	}
	// GET context: all scopes surfaces any repo's failure by symbol.
	w := doFailureGet(t, mux, "/api/blocks/context?scope=all&symbol=Beta", nil)
	if rendered := v2ContextRendered(t, w); !strings.Contains(rendered, "fix b") {
		t.Fatalf("context?scope=all&symbol=Beta missing repo-b failure:\n%s", rendered)
	}

	// POST: sentinel scope records a GLOBAL block, never a literal "all" scope.
	for _, c := range []struct{ scope, errText string }{
		{"all", "all-sentinel probe"},
		{"*", "star-sentinel probe"},
		{"ALL", "upper-sentinel probe"},
	} {
		body := `{"command":"go vet","error":"` + c.errText + `","scope":"` + c.scope + `"}`
		w := doFailureAdd(t, mux, body, nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("POST scope=%s: %d %s", c.scope, w.Code, w.Body.String())
		}
		var b memory.Block
		if err := json.NewDecoder(w.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if b.Scope != "" {
			t.Fatalf("POST scope=%s stored scope %q, want global \"\"", c.scope, b.Scope)
		}
	}
	// The globals are visible to the unrestricted listing and to every repo.
	w = doFailureGet(t, mux, "/api/blocks/failures", nil)
	if _, n := decodeFailureList(t, w); n != 5 {
		t.Fatalf("unrestricted list count = %d, want 5", n)
	}
	w = doFailureGet(t, mux, "/api/blocks/failures?scope=repo-a", nil)
	if _, n := decodeFailureList(t, w); n != 4 {
		t.Fatalf("repo-a list count = %d, want 4 (own + 3 globals)", n)
	}
}

// The all/* sentinel must never bypass a restricted X-Gleann-Target/?target=.
func TestV2FailureAllSentinelCannotBypassTarget(t *testing.T) {
	_, mux := newFailuresTestServer(t)
	seedFailures(t, mux)

	for _, path := range []string{
		"/api/blocks/failures?scope=all",
		"/api/blocks/failures?scope=*",
		"/api/blocks/failures?scope=ALL",
		"/api/blocks/failures?target=all",
		"/api/blocks/context?scope=all&symbol=Alpha",
	} {
		w := doFailureGet(t, mux, path, map[string]string{"X-Gleann-Target": "repo-a"})
		if w.Code != http.StatusForbidden {
			t.Fatalf("GET %s with restricted header: expected 403, got %d: %s", path, w.Code, w.Body.String())
		}
	}
	// ?target= alone restricts too (no header).
	w := doFailureGet(t, mux, "/api/blocks/failures?scope=all&target=repo-a", nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("?scope=all&target=repo-a: expected 403, got %d", w.Code)
	}

	// POST: body/query sentinel vs restricted header/query target → 403.
	for _, c := range []struct{ path, body string }{
		{"/api/blocks/failures", `{"command":"go build","error":"bypass 1","scope":"all"}`},
		{"/api/blocks/failures", `{"command":"go build","error":"bypass 2","scope":"*"}`},
		{"/api/blocks/failures?scope=all", `{"command":"go build","error":"bypass 3"}`},
		{"/api/blocks/failures?target=repo-a", `{"command":"go build","error":"bypass 4","scope":"all"}`},
	} {
		r := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Gleann-Target", "repo-a")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("POST %s body %s with restricted target: expected 403, got %d: %s", c.path, c.body, w.Code, w.Body.String())
		}
	}

	// A wildcard header/target is unrestricted: sentinel and plain listing work.
	w = doFailureGet(t, mux, "/api/blocks/failures?scope=all", map[string]string{"X-Gleann-Target": "all"})
	if _, n := decodeFailureList(t, w); n != 2 {
		t.Fatalf("wildcard header + scope=all count = %d, want 2", n)
	}
	w = doFailureGet(t, mux, "/api/blocks/failures", map[string]string{"X-Gleann-Target": "*"})
	if _, n := decodeFailureList(t, w); n != 2 {
		t.Fatalf("wildcard header count = %d, want 2", n)
	}
	// A real restricted target still filters normally.
	w = doFailureGet(t, mux, "/api/blocks/failures", map[string]string{"X-Gleann-Target": "repo-a"})
	if _, n := decodeFailureList(t, w); n != 1 {
		t.Fatalf("restricted header count = %d, want 1", n)
	}
}

// ── v2 repair, finding 5: OpenAPI parameter parity ───────────────────────────

// v2OpParams maps parameter name → "in" for one operation of the served spec.
func v2OpParams(t *testing.T, s *Server, path, method string) map[string]string {
	t.Helper()
	spec := s.openAPISpec()
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("spec has no paths")
	}
	item, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("spec missing path %s", path)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("spec missing %s %s", method, path)
	}
	out := map[string]string{}
	if params, ok := op["parameters"].([]map[string]any); ok {
		for _, p := range params {
			name, _ := p["name"].(string)
			in, _ := p["in"].(string)
			out[name] = in
		}
	}
	return out
}

func TestV2OpenAPIFailureAndContextParams(t *testing.T) {
	s, _ := newFailuresTestServer(t)

	ctxParams := v2OpParams(t, s, "/api/blocks/context", "get")
	for _, name := range []string{"format", "scope", "target", "command", "paths", "path", "symbols", "symbol"} {
		if ctxParams[name] != "query" {
			t.Errorf("GET /api/blocks/context missing query parameter %q (have %v)", name, ctxParams)
		}
	}
	if ctxParams["X-Gleann-Target"] != "header" {
		t.Errorf("GET /api/blocks/context missing X-Gleann-Target header parameter (have %v)", ctxParams)
	}

	getParams := v2OpParams(t, s, "/api/blocks/failures", "get")
	for _, name := range []string{"scope", "target", "command", "paths", "path", "symbols", "symbol"} {
		if getParams[name] != "query" {
			t.Errorf("GET /api/blocks/failures missing query parameter %q (have %v)", name, getParams)
		}
	}
	if getParams["X-Gleann-Target"] != "header" {
		t.Errorf("GET /api/blocks/failures missing X-Gleann-Target header parameter (have %v)", getParams)
	}

	postParams := v2OpParams(t, s, "/api/blocks/failures", "post")
	if postParams["scope"] != "query" || postParams["X-Gleann-Target"] != "header" {
		t.Errorf("POST /api/blocks/failures lost scope/target parameters (have %v)", postParams)
	}
}

// ── v3 repair, finding 1: POST /api/blocks normalizes the scope ──────────────

func TestV3AddBlockNormalizesScope(t *testing.T) {
	s, mux := newBlocksTestServer(t)

	// Every padded spelling must land in the canonical scope.
	for i, spelling := range []string{"repo-a/", "/repo-a", " repo-a "} {
		w := doBlockAdd(t, mux, blockAddRequest{Content: fmt.Sprintf("zz v3 generic %d", i), Scope: spelling})
		if w.Code != http.StatusCreated {
			t.Fatalf("scope %q: expected 201, got %d: %s", spelling, w.Code, w.Body.String())
		}
		var block memory.Block
		if err := json.NewDecoder(w.Body).Decode(&block); err != nil {
			t.Fatal(err)
		}
		if block.Scope != "repo-a" {
			t.Fatalf("scope %q stored as %q, want repo-a", spelling, block.Scope)
		}
	}

	// A normalized body scope agrees with a normalized header target.
	body, _ := json.Marshal(blockAddRequest{Content: "zz v3 generic hdr", Scope: "repo-a/"})
	r := httptest.NewRequest(http.MethodPost, "/api/blocks", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Gleann-Target", "repo-a")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("padded body scope vs header target: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// The target alias is normalized too.
	w = doBlockAdd(t, mux, blockAddRequest{Content: "zz v3 generic alias", Target: "repo-a/"})
	if w.Code != http.StatusCreated {
		t.Fatalf("padded target alias: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// A sentinel scope is global when unrestricted, never a literal "all".
	w = doBlockAdd(t, mux, blockAddRequest{Content: "zz v3 generic global", Scope: " ALL "})
	if w.Code != http.StatusCreated {
		t.Fatalf("sentinel scope: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var global memory.Block
	json.NewDecoder(w.Body).Decode(&global)
	if global.Scope != "" {
		t.Fatalf("sentinel scope stored as %q, want global \"\"", global.Scope)
	}

	// A sentinel body scope cannot bypass a restricted header.
	body, _ = json.Marshal(blockAddRequest{Content: "zz v3 bypass", Scope: "*"})
	r = httptest.NewRequest(http.MethodPost, "/api/blocks", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Gleann-Target", "repo-a")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("sentinel body scope vs restricted header: expected 403, got %d: %s", w.Code, w.Body.String())
	}

	// Canonical reads find all five repo-a blocks plus the global one.
	r = httptest.NewRequest(http.MethodGet, "/api/blocks?scope=repo-a", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var resp struct {
		Count  int            `json:"count"`
		Blocks []memory.Block `json:"blocks"`
	}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Count != 6 {
		t.Fatalf("scope=repo-a count = %d, want 6 (5 repo-a + 1 global)", resp.Count)
	}
	_ = s
}

// ── v3 repair, finding 2: repo sentinel must not create a literal "all" scope ──

func TestV3AddFailureRepoSentinel(t *testing.T) {
	_, mux := newFailuresTestServer(t)

	for i, repo := range []string{"all", " ALL ", "*"} {
		body := fmt.Sprintf(`{"command":"go build","error":"zz v3 repo sentinel %d","repo":%q}`, i, repo)
		w := doFailureAdd(t, mux, body, nil)
		if w.Code != http.StatusCreated {
			t.Fatalf("repo %q: expected 201, got %d: %s", repo, w.Code, w.Body.String())
		}
		var b memory.Block
		if err := json.NewDecoder(w.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if b.Scope != "" {
			t.Fatalf("repo %q stored scope %q, want global \"\"", repo, b.Scope)
		}
	}

	// A padded repo defaults the scope, normalized.
	w := doFailureAdd(t, mux, `{"command":"go test","error":"zz v3 repo padded","repo":"/repo-b/"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("padded repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var b memory.Block
	json.NewDecoder(w.Body).Decode(&b)
	if b.Scope != "repo-b" {
		t.Fatalf("padded repo stored scope %q, want repo-b", b.Scope)
	}

	// repo-a sees the three global failures, never the repo-b one, and none
	// carries a literal "all" scope.
	w = doFailureGet(t, mux, "/api/blocks/failures?scope=repo-a", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	fs, count := decodeFailureList(t, w)
	if count != 3 {
		t.Fatalf("repo-a failure count = %d, want 3 globals", count)
	}
	for _, f := range fs {
		if f.Scope == "all" || f.Scope == "repo-b" {
			t.Fatalf("repo-a view leaked scope %q", f.Scope)
		}
	}
}

// ── v3 repair, finding 4: header and query sentinels behave the same ─────────

func TestV3GenericRoutesSentinelParity(t *testing.T) {
	_, mux := newBlocksTestServer(t)
	doBlockAdd(t, mux, blockAddRequest{Content: "zz v4 alpha fact", Scope: "repo-a"})
	doBlockAdd(t, mux, blockAddRequest{Content: "zz v4 beta fact", Scope: "repo-b"})
	doBlockAdd(t, mux, blockAddRequest{Content: "zz v4 global fact"})

	get := func(path string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	listCount := func(w *httptest.ResponseRecorder) int {
		var resp struct {
			Count int `json:"count"`
		}
		json.NewDecoder(w.Body).Decode(&resp)
		return resp.Count
	}

	for _, sentinel := range []string{"all", "ALL", "*"} {
		hdr := map[string]string{"X-Gleann-Target": sentinel}

		// List: query sentinel == header sentinel == all scopes.
		wq := get("/api/blocks?scope="+sentinel, nil)
		wh := get("/api/blocks", hdr)
		if wq.Code != 200 || wh.Code != 200 {
			t.Fatalf("sentinel %q list codes: query %d header %d", sentinel, wq.Code, wh.Code)
		}
		cq, ch := listCount(wq), listCount(wh)
		if cq != 3 || ch != 3 {
			t.Fatalf("sentinel %q list counts: query %d header %d, want 3/3", sentinel, cq, ch)
		}
		// The target alias behaves like scope.
		wt := get("/api/blocks?target="+sentinel, nil)
		if wt.Code != 200 || listCount(wt) != 3 {
			t.Fatalf("sentinel %q target alias: code %d count %d, want 200/3", sentinel, wt.Code, listCount(wt))
		}

		// Search: query sentinel == header sentinel == all scopes.
		wq = get("/api/blocks/search?q=zz+v4&scope="+sentinel, nil)
		wh = get("/api/blocks/search?q=zz+v4", hdr)
		for _, w := range []*httptest.ResponseRecorder{wq, wh} {
			if w.Code != 200 {
				t.Fatalf("sentinel %q search code %d: %s", sentinel, w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") || !strings.Contains(body, "global") {
				t.Fatalf("sentinel %q search must see every scope: %s", sentinel, body)
			}
		}

		// Context: query sentinel == header sentinel == all scopes.
		wq = get("/api/blocks/context?format=xml&scope="+sentinel, nil)
		wh = get("/api/blocks/context?format=xml", hdr)
		for _, w := range []*httptest.ResponseRecorder{wq, wh} {
			if w.Code != 200 {
				t.Fatalf("sentinel %q context code %d: %s", sentinel, w.Code, w.Body.String())
			}
			body := w.Body.String()
			if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
				t.Fatalf("sentinel %q context must see every scope: %s", sentinel, body)
			}
		}
	}

	// A sentinel may never bypass a restricted target, via header or query.
	if w := get("/api/blocks?scope=all", map[string]string{"X-Gleann-Target": "repo-a"}); w.Code != http.StatusForbidden {
		t.Fatalf("query sentinel vs restricted header: want 403, got %d", w.Code)
	}
	if w := get("/api/blocks?scope=all&target=repo-a", nil); w.Code != http.StatusForbidden {
		t.Fatalf("query sentinel vs restricted query target: want 403, got %d", w.Code)
	}
	if w := get("/api/blocks/search?q=zz&scope=*", map[string]string{"X-Gleann-Target": "repo-a"}); w.Code != http.StatusForbidden {
		t.Fatalf("search sentinel vs restricted header: want 403, got %d", w.Code)
	}
	if w := get("/api/blocks/context?scope=all", map[string]string{"X-Gleann-Target": "repo-a"}); w.Code != http.StatusForbidden {
		t.Fatalf("context sentinel vs restricted header: want 403, got %d", w.Code)
	}
}
