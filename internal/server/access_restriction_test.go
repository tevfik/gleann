package server

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/metafault"
	"github.com/tevfik/gleann/internal/store"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// restrictedFixture is a server with GLEANN_TAGS=team and the indexes
//
//	mine    tags team   (allowed)
//	secret  tags other  (forbidden)
//	corrupt unreadable metadata (must fail closed)
//	ghost   does not exist
//
// every index-scoped route is registered on the mux.
func restrictedFixture(t *testing.T) (*Server, *http.ServeMux, string) {
	t.Helper()
	t.Setenv("GLEANN_TAGS", "team")
	t.Setenv("GLEANN_PROXY_MODE", "")
	s, _, dir := patchTestServer(t)
	mux := s.buildMux()
	for name, tags := range map[string][]string{"mine": {"team"}, "secret": {"other"}} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(gleann.IndexMeta{Name: name, Tags: tags})
		must(t, os.WriteFile(filepath.Join(dir, name, name+".meta.json"), b, 0o644))
		must(t, os.WriteFile(filepath.Join(dir, name, name+".index"), []byte("data-"+name), 0o644))
	}
	must(t, os.MkdirAll(filepath.Join(dir, "corrupt"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "corrupt", "corrupt.meta.json"), []byte("{"), 0o644))
	return s, mux, dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// treeDigest lists every file under dir with a content hash.
func treeDigest(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	must(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			lines = append(lines, rel+"/")
			return nil
		}
		b, _ := os.ReadFile(p)
		lines = append(lines, fmt.Sprintf("%s %x", rel, sha256.Sum256(b)))
		return nil
	}))
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

type routeCase struct{ method, path, body string }

// indexRoutes are the index-scoped routes of the router, %s is the index name.
var indexRoutes = []routeCase{
	{http.MethodGet, "/api/indexes/%s", ""},
	{http.MethodPatch, "/api/indexes/%s", `{"description":"x"}`},
	{http.MethodDelete, "/api/indexes/%s", ""},
	{http.MethodGet, "/api/indexes/%s/passages", ""},
	{http.MethodGet, "/api/indexes/%s/sections", ""},
	{http.MethodPost, "/api/indexes/%s/search", `{"query":"q"}`},
	{http.MethodPost, "/api/indexes/%s/ask", `{"question":"q"}`},
	{http.MethodPost, "/api/indexes/%s/build", `{}`},
	{http.MethodPost, "/api/indexes/%s/update", `{"items":[{"content":"x"}]}`},
	{http.MethodPost, "/api/indexes/%s/index-path", `{"path":"/nonexistent"}`},
	{http.MethodPost, "/api/indexes/%s/watch", `{"enable":false}`},
	{http.MethodPost, "/api/indexes/%s/upload", `x`},
	{http.MethodPost, "/api/indexes/%s/storage", `{"mode":"store"}`},
	{http.MethodGet, "/api/graph/%s", ""},
	{http.MethodGet, "/api/graph/%s/toc", ""},
	{http.MethodGet, "/api/graph/%s/documents", ""},
	{http.MethodPost, "/api/graph/%s/query", `{"query_type":"callers","symbol":"x"}`},
	{http.MethodPost, "/api/graph/%s/index", `{"docs_dir":"/nonexistent"}`},
	{http.MethodPost, "/api/memory/%s/inject", `{"nodes":[]}`},
	{http.MethodDelete, "/api/memory/%s/nodes/1", ""},
	{http.MethodDelete, "/api/memory/%s/edges", `{}`},
	{http.MethodPost, "/api/memory/%s/traverse", `{"start_id":"x"}`},
	{http.MethodPost, "/api/search", `{"query":"q","indexes":["%s"]}`},
}

func doRoute(s *Server, mux *http.ServeMux, rc routeCase, name string) (int, string) {
	path := rc.path
	body := rc.body
	if strings.Contains(path, "%s") {
		path = fmt.Sprintf(path, name)
	}
	if strings.Contains(body, "%s") {
		body = fmt.Sprintf(body, name)
	}
	w := serve(mux, rc.method, path, body, nil)
	return w.Code, w.Body.String()
}

// Under GLEANN_TAGS every index-scoped route, reads and writes, refuses an
// index with other tags, and refuses a missing or unreadable one in the very
// same way (status and body), without changing anything on disk.
func TestGleannTagsApplyToEveryIndexRoute(t *testing.T) {
	s, mux, dir := restrictedFixture(t)
	before := treeDigest(t, dir)
	for _, rc := range indexRoutes {
		rc := rc
		t.Run(rc.method+" "+rc.path, func(t *testing.T) {
			fc, fb := doRoute(s, mux, rc, "secret")
			if fc != http.StatusForbidden {
				t.Fatalf("other-tag index: %d %s, want 403", fc, fb)
			}
			for _, name := range []string{"ghost", "corrupt"} {
				c, b := doRoute(s, mux, rc, name)
				if c != fc {
					t.Errorf("%s: %d, forbidden index %d; must be equal", name, c, fc)
				}
				if got, want := strings.ReplaceAll(b, name, "NAME"), strings.ReplaceAll(fb, "secret", "NAME"); got != want {
					t.Errorf("%s: body %q differs from forbidden body %q", name, got, want)
				}
				if strings.Contains(b, dir) {
					t.Errorf("%s: body leaks the server path: %s", name, b)
				}
			}
		})
	}
	if after := treeDigest(t, dir); after != before {
		t.Errorf("a refused request changed the index directory:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// An allowed index can still be deleted under GLEANN_TAGS.
func TestGleannTagsAllowDeletingAnAllowedIndex(t *testing.T) {
	s, mux, dir := restrictedFixture(t)
	if c, b := doRoute(s, mux, routeCase{http.MethodDelete, "/api/indexes/%s", ""}, "mine"); c != http.StatusOK {
		t.Fatalf("DELETE mine: %d %s", c, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine")); !os.IsNotExist(err) {
		t.Errorf("index mine still on disk: %v", err)
	}
}

// Unrestricted: a missing index is 404 on every route that reports it, the
// body names no path, and deleting one is 404, not 200.
func TestMissingIndexUnrestricted(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s, _, dir := patchTestServer(t)
	mux := s.buildMux()
	for _, rc := range []routeCase{
		{http.MethodGet, "/api/indexes/%s", ""},
		{http.MethodPatch, "/api/indexes/%s", `{"description":"x"}`},
		{http.MethodDelete, "/api/indexes/%s", ""},
		{http.MethodPost, "/api/indexes/%s/search", `{"query":"q"}`},
		{http.MethodPost, "/api/search", `{"query":"q","indexes":["%s"]}`},
	} {
		c, b := doRoute(s, mux, rc, "ghost")
		if c != http.StatusNotFound {
			t.Errorf("%s %s ghost: %d %s, want 404", rc.method, rc.path, c, b)
		}
		if strings.Contains(b, dir) || strings.Contains(b, ".meta.json") {
			t.Errorf("%s %s ghost: body leaks a path: %s", rc.method, rc.path, b)
		}
	}
}

// The OpenAI proxy lists, and serves, only the indexes the restriction allows.
func TestProxyHonoursGleannTags(t *testing.T) {
	s, mux, _ := restrictedFixture(t)
	w := serve(mux, http.MethodGet, "/v1/models", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/v1/models: %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"gleann/mine", "gleann/@team"} {
		if !strings.Contains(body, want) {
			t.Errorf("/v1/models lacks %s: %s", want, body)
		}
	}
	for _, bad := range []string{"secret", "@other", "corrupt"} {
		if strings.Contains(body, bad) {
			t.Errorf("/v1/models lists %q under GLEANN_TAGS=team: %s", bad, body)
		}
	}

	chat := func(model string) (int, string) {
		w := serve(mux, http.MethodPost, "/v1/chat/completions",
			fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, model), nil)
		return w.Code, w.Body.String()
	}
	fc, fb := chat("gleann/secret")
	if fc != http.StatusForbidden {
		t.Fatalf("model gleann/secret: %d %s, want 403", fc, fb)
	}
	for _, name := range []string{"ghost", "corrupt"} {
		c, b := chat("gleann/" + name)
		if c != fc || strings.ReplaceAll(b, name, "NAME") != strings.ReplaceAll(fb, "secret", "NAME") {
			t.Errorf("model gleann/%s: %d %s; must equal the forbidden answer %d %s", name, c, b, fc, fb)
		}
	}
	// A list that names a forbidden index is refused as a whole.
	if c, _ := chat("gleann/mine,secret"); c != http.StatusForbidden {
		t.Errorf("model naming mine and secret: %d, want 403", c)
	}

	got, err := s.expandProxyIndexNames([]string{"@team"})
	if err != nil || len(got) != 1 || got[0] != "mine" {
		t.Errorf("@team expands to %v, %v; want [mine]", got, err)
	}
	if got, err := s.expandProxyIndexNames([]string{"@other"}); err == nil {
		t.Errorf("@other expands to %v under GLEANN_TAGS=team; want an error", got)
	}
}

// Same refusal on the pass-through path, before any upstream is contacted.
func TestProxyPassthroughHonoursGleannTags(t *testing.T) {
	s, mux, _ := restrictedFixture(t)
	t.Setenv("GLEANN_PROXY_MODE", "passthrough")
	_ = s
	for _, name := range []string{"secret", "ghost"} {
		w := serve(mux, http.MethodPost, "/v1/chat/completions",
			fmt.Sprintf(`{"model":"gleann/%s","messages":[{"role":"user","content":"hi"}]}`, name), nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("passthrough model gleann/%s: %d %s, want 403", name, w.Code, w.Body.String())
		}
	}
}

// Every restricted route documents the 403 it answers.
func TestSpecDocumentsAccessDenied(t *testing.T) {
	s, _, _ := patchTestServer(t)
	for path, methods := range accessRestrictedOps {
		for _, m := range methods {
			resp := specOperation(t, s, path, m)["responses"].(map[string]any)
			if _, ok := resp["403"]; !ok {
				t.Errorf("%s %s: spec lacks 403", strings.ToUpper(m), path)
			}
		}
	}
	resp := specOperation(t, s, "/api/search", "post")["responses"].(map[string]any)
	if _, ok := resp["404"]; !ok {
		t.Error("POST /api/search: spec lacks 404")
	}
	// Every route the router registers on an index path is in the table.
	routes := map[string]bool{}
	for p, ms := range accessRestrictedOps {
		for _, m := range ms {
			routes[strings.ToUpper(m)+" "+p] = true
		}
	}
	for _, rc := range indexRoutes {
		spec := strings.Replace(rc.path, "%s", "{name}", 1)
		if strings.HasPrefix(spec, "/api/memory/{name}/nodes/") {
			spec = "/api/memory/{name}/nodes/{id}"
		}
		if !routes[rc.method+" "+spec] {
			t.Errorf("%s %s answers 403 but accessRestrictedOps does not list it", rc.method, spec)
		}
	}
}

// Under GLEANN_TAGS a build may create a NEW index when the request assigns an
// allowed tag; the index is stored with it and is visible to the client after.
func TestBuildCreatesIndexUnderGleannTags(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, cfg := newIndexedServer(t)
	t.Setenv("GLEANN_TAGS", "team") // after the fixture's own build
	dir := cfg.IndexDir
	build := func(name, tags string) *httptest.ResponseRecorder {
		return doJSON(h, "POST", "/api/indexes/"+name+"/build",
			`{"items":[{"text":"hello world"}]`+tags+`}`, nil)
	}

	if w := build("fresh", `,"tags":["team"]`); w.Code != http.StatusOK {
		t.Fatalf("create with an allowed tag: %d %s", w.Code, w.Body.String())
	}
	if m := getMeta(t, h, "fresh"); len(m.Tags) != 1 || m.Tags[0] != "team" {
		t.Errorf("created index tags = %v, want the request's [team]", m.Tags)
	}
	// An index the client created is its own: rebuilding it needs no tags.
	if w := build("fresh", ``); w.Code != http.StatusOK {
		t.Errorf("rebuild of an allowed index: %d %s", w.Code, w.Body.String())
	}

	for name, tags := range map[string]string{"untagged": ``, "wrongtag": `,"tags":["other"]`, "emptytags": `,"tags":[]`} {
		w := build(name, tags)
		if w.Code != http.StatusForbidden {
			t.Errorf("create %s without an allowed tag: %d %s, want 403", name, w.Code, w.Body.String())
		}
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("a refused create left %s on disk (%v)", name, err)
		}
	}

	// An existing index with another tag stays closed, whatever tags the request carries.
	must(t, os.MkdirAll(filepath.Join(dir, "secret"), 0o755))
	b, _ := json.Marshal(gleann.IndexMeta{Name: "secret", Tags: []string{"other"}})
	must(t, os.WriteFile(filepath.Join(dir, "secret", "secret.meta.json"), b, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "secret", "secret.index"), []byte("data"), 0o644))
	before := treeDigest(t, dir)
	if w := build("secret", `,"tags":["team"]`); w.Code != http.StatusForbidden {
		t.Errorf("build over an other-tag index: %d %s, want 403", w.Code, w.Body.String())
	}
	if after := treeDigest(t, dir); after != before {
		t.Errorf("a refused build changed the directory:\n%s\n--\n%s", before, after)
	}
}

// Upload cannot carry tags, so under GLEANN_TAGS it cannot create an index.
func TestUploadCannotCreateIndexUnderGleannTags(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, cfg := newIndexedServer(t)
	t.Setenv("GLEANN_TAGS", "team") // after the fixture's own build
	w := doJSON(h, "POST", "/api/indexes/newup/upload", `x`, nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("upload creating an index: %d %s, want 403", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "newup")); !os.IsNotExist(err) {
		t.Errorf("refused upload left the index on disk (%v)", err)
	}
}

// A target-only restriction lets a client create the one name it is pinned to.
func TestBuildCreatesPinnedIndexUnderTarget(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, _ := newIndexedServer(t)
	hdr := map[string]string{"X-Gleann-Target": "pinned"}
	body := `{"items":[{"text":"hello world"}]}`
	if w := doJSON(h, "POST", "/api/indexes/pinned/build", body, hdr); w.Code != http.StatusOK {
		t.Errorf("create the pinned name: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(h, "POST", "/api/indexes/other/build", body, hdr); w.Code != http.StatusForbidden {
		t.Errorf("create another name: %d %s, want 403", w.Code, w.Body.String())
	}
}

// The older isIndexAccessible gate (unified memory recall and graph ingest,
// the A2A skills) fails closed under GLEANN_TAGS: an index whose metadata is
// corrupt or missing is not accessible, so its memory graph is never opened.
func TestIsIndexAccessibleFailsClosedUnderGleannTags(t *testing.T) {
	s, mux, dir := restrictedFixture(t)
	s.memoryPool = newMemoryPool(dir) // a real pool: a passed gate must not panic on nil
	t.Cleanup(s.memoryPool.closeAll)
	must(t, os.MkdirAll(filepath.Join(dir, "nometa"), 0o755))

	for name, want := range map[string]bool{"mine": true, "secret": false, "corrupt": false, "nometa": false, "ghost": false} {
		if got := s.isIndexAccessible(name); got != want {
			t.Errorf("isIndexAccessible(%s) = %v, want %v", name, got, want)
		}
	}

	before := treeDigest(t, dir)
	for _, name := range []string{"corrupt", "nometa", "secret"} {
		// Unified recall: neither layer may open the index.
		w := serve(mux, http.MethodPost, "/api/memory/recall",
			fmt.Sprintf(`{"query":"x","index":%q,"layers":["vector","graph"]}`, name), nil)
		if w.Code != http.StatusOK {
			t.Errorf("recall %s: %d %s", name, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), `"nodes":[`) {
			t.Errorf("recall %s returned graph nodes: %s", name, w.Body.String())
		}
		// Graph ingest: refused per relationship, nothing created.
		w = serve(mux, http.MethodPost, "/api/memory/ingest",
			fmt.Sprintf(`{"relationships":[{"from":"a","to":"b","relation":"r","index":%q}]}`, name), nil)
		if !strings.Contains(w.Body.String(), "access denied") || strings.Contains(w.Body.String(), `"edges_created":1`) {
			t.Errorf("ingest into %s: %d %s, want access denied", name, w.Code, w.Body.String())
		}
		// A2A skills.
		for skill, call := range map[string]func(a2a.SkillContext) (string, error){
			"search": s.a2aSearchHandler, "ask": s.a2aAskHandler, "code": s.a2aCodeHandler,
		} {
			_, err := call(a2a.SkillContext{Metadata: map[string]any{"index": name}})
			if err == nil || !strings.Contains(err.Error(), "not") {
				t.Errorf("a2a %s on %s: err = %v, want a refusal", skill, name, err)
			}
		}
	}
	if len(s.memoryPool.handles) != 0 {
		t.Errorf("memory pool opened %d index(es) for refused requests", len(s.memoryPool.handles))
	}
	if after := treeDigest(t, dir); after != before {
		t.Errorf("refused requests changed the directory:\n%s\n--\n%s", before, after)
	}
}

// A new name without an allowed tag and an existing other-tag name get the
// same refusal text.
func TestBuildRefusalWordingIsIdentical(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, cfg := newIndexedServer(t)
	t.Setenv("GLEANN_TAGS", "team")
	b, _ := json.Marshal(gleann.IndexMeta{Name: "secret", Tags: []string{"other"}})
	must(t, os.MkdirAll(filepath.Join(cfg.IndexDir, "secret"), 0o755))
	must(t, os.WriteFile(filepath.Join(cfg.IndexDir, "secret", "secret.meta.json"), b, 0o644))
	body := `{"items":[{"text":"x"}],"tags":["other"]}`
	a := doJSON(h, "POST", "/api/indexes/secret/build", body, nil)
	n := doJSON(h, "POST", "/api/indexes/brandnew/build", body, nil)
	if a.Code != 403 || n.Code != 403 ||
		strings.ReplaceAll(a.Body.String(), "secret", "N") != strings.ReplaceAll(n.Body.String(), "brandnew", "N") {
		t.Errorf("existing other-tag: %d %s; new untagged: %d %s", a.Code, a.Body.String(), n.Code, n.Body.String())
	}
}

// A shutdown cancels an in-flight load for every waiting request. Only a
// request whose own context is done is a client that went away (499); a live
// one is told to retry (503).
func TestSearcherErrorCanceledDuringShutdown(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/indexes/idx", nil)
	w := httptest.NewRecorder()
	writeSearcherError(w, r, "idx", fmt.Errorf("load: %w", context.Canceled))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Errorf("live client, canceled load: %d retry=%q, want 503 with Retry-After", w.Code, w.Header().Get("Retry-After"))
	}
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w = httptest.NewRecorder()
	writeSearcherError(w, r.WithContext(ctx), "idx", fmt.Errorf("load: %w", context.Canceled))
	if w.Code != 499 || w.Body.Len() != 0 {
		t.Errorf("gone client: %d %q, want an empty 499", w.Code, w.Body.String())
	}
}

// A restricted client may only set tags inside its allowed set, on build
// (create and rebuild) and on PATCH, and at least one must stay: it can neither
// move an index into another tenant nor publish into one.
func TestRestrictedClientCannotSetForeignTags(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, cfg := newIndexedServer(t)
	t.Setenv("GLEANN_TAGS", "team")
	build := func(name, tags string) int {
		return doJSON(h, "POST", "/api/indexes/"+name+"/build", `{"items":[{"text":"hello"}],"tags":`+tags+`}`, nil).Code
	}
	patch := func(name, tags string) int {
		return doJSON(h, "PATCH", "/api/indexes/"+name, `{"tags":`+tags+`}`, nil).Code
	}

	if c := build("both", `["team","other"]`); c != http.StatusForbidden {
		t.Errorf("create straight into another tenant: %d, want 403", c)
	}
	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "both")); !os.IsNotExist(err) {
		t.Errorf("a refused create left the index on disk (%v)", err)
	}
	if c := build("mine", `["team"]`); c != http.StatusOK {
		t.Fatalf("create mine: %d", c)
	}
	for _, tags := range []string{`["other"]`, `["team","other"]`, `["@Other"]`} {
		if c := build("mine", tags); c != http.StatusForbidden {
			t.Errorf("rebuild mine with %s: %d, want 403", tags, c)
		}
		if c := patch("mine", tags); c != http.StatusForbidden {
			t.Errorf("PATCH mine with %s: %d, want 403", tags, c)
		}
	}
	// index-path appends tags to the index: foreign ones are refused too (the
	// path is inside allowed_roots, so only the tag rule can answer 403).
	docs := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", docs)
	for _, tags := range []string{`["other"]`, `["team","other"]`, `["@Other"]`} {
		w := doJSON(h, "POST", "/api/indexes/mine/index-path", `{"path":`+strconv.Quote(docs)+`,"tags":`+tags+`}`, nil)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "tags must stay within") {
			t.Errorf("index-path on mine with %s: %d %s, want the tag refusal (403)", tags, w.Code, w.Body.String())
		}
	}
	if c := patch("mine", `[]`); c != http.StatusForbidden {
		t.Errorf("PATCH mine dropping every tag: %d, want 403", c)
	}
	if m := getMeta(t, h, "mine"); len(m.Tags) != 1 || m.Tags[0] != "team" {
		t.Errorf("tags after the refusals = %v, want [team]", m.Tags)
	}
	// "@Team" is the allowed tag written the tag-reference way: it is stored
	// normalised, so the client still sees its own index afterwards.
	get := func(name string) int { return doJSON(h, "GET", "/api/indexes/"+name, "", nil).Code }
	if c := build("atcreate", `["@Team"]`); c != http.StatusOK || get("atcreate") != http.StatusOK {
		t.Errorf("create with [@Team]: build %d, GET %d, want 200 and 200", c, get("atcreate"))
	}
	if c := build("mine", `["@Team"]`); c != http.StatusOK || get("mine") != http.StatusOK {
		t.Errorf("rebuild with [@Team]: build %d, GET %d, want 200 and 200", c, get("mine"))
	}
	if c := patch("mine", `["@Team"]`); c != http.StatusOK || get("mine") != http.StatusOK {
		t.Errorf("PATCH with [@Team]: %d, GET %d, want 200 and 200", c, get("mine"))
	}
	if m := getMeta(t, h, "mine"); len(m.Tags) != 1 || m.Tags[0] != "Team" {
		t.Errorf("stored tags = %v, want [Team] (no @)", m.Tags)
	}
	if c := patch("mine", `["Team"]`); c != http.StatusOK {
		t.Errorf("PATCH mine with an allowed tag: %d, want 200", c)
	}

	// Unrestricted: any tags.
	t.Setenv("GLEANN_TAGS", "")
	if c := patch("mine", `["other"]`); c != http.StatusOK {
		t.Errorf("unrestricted PATCH: %d, want 200", c)
	}
	if c := build("mine", `["a","b"]`); c != http.StatusOK {
		t.Errorf("unrestricted rebuild: %d, want 200", c)
	}
}

// Metadata writes failing during a restricted create (the index is built, a
// later update cannot be stored) must not leave an index the client can
// neither see nor delete.
func TestRestrictedCreateWithFailingMetaWriteLeavesNothing(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, cfg := newIndexedServer(t)
	t.Setenv("GLEANN_TAGS", "team")
	before := treeDigest(t, cfg.IndexDir)
	t.Cleanup(metafault.Set(func(string) error { return fs.ErrPermission })) // every UpdateIndexMeta write fails
	w := doJSON(h, "POST", "/api/indexes/atomic/build", `{"items":[{"text":"hello"}],"tags":["team"]}`, nil)
	if w.Code == http.StatusOK {
		t.Fatalf("create succeeded although every metadata update fails: %s", w.Body.String())
	}
	if after := treeDigest(t, cfg.IndexDir); after != before {
		t.Errorf("a failed create left files behind (status %d):\nbefore:\n%s\nafter:\n%s", w.Code, before, after)
	}
}

// A restricted client's create that fails leaves nothing behind (no index it
// could neither see nor delete).
func TestFailedRestrictedCreateLeavesNothing(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s, h, cfg := newIndexedServer(t)
	t.Setenv("GLEANN_TAGS", "team")
	s.embedder = embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOllama, Model: "test-model", BaseURL: "http://127.0.0.1:1",
	})
	before := treeDigest(t, cfg.IndexDir)
	w := doJSON(h, "POST", "/api/indexes/doomed/build", `{"items":[{"text":"hello"}],"tags":["team"]}`, nil)
	if w.Code == http.StatusOK {
		t.Skipf("the build did not fail with an unreachable embedder: %s", w.Body.String())
	}
	if after := treeDigest(t, cfg.IndexDir); after != before {
		t.Errorf("a failed create left files behind (status %d):\nbefore:\n%s\nafter:\n%s", w.Code, before, after)
	}
}

// The cleanup of a failed restricted create removes only an index that is
// still ours: one another tenant created in between survives.
func TestFailedRestrictedCreateKeepsForeignIndex(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, cfg := newIndexedServer(t)
	t.Setenv("GLEANN_TAGS", "team")
	foreign, _ := json.Marshal(gleann.IndexMeta{Name: "raced", Tags: []string{"other"}})
	t.Cleanup(metafault.Set(func(path string) error {
		// Another tenant publishes its index under this name just before our
		// build fails.
		_ = os.WriteFile(path, foreign, 0o644)
		return fs.ErrPermission
	}))
	w := doJSON(h, "POST", "/api/indexes/raced/build", `{"items":[{"text":"hello"}],"tags":["team"]}`, nil)
	if w.Code == http.StatusOK {
		t.Fatalf("build succeeded: %s", w.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(cfg.IndexDir, "raced", "raced.meta.json"))
	if err != nil || !strings.Contains(string(b), `"other"`) {
		t.Errorf("the other tenant's index was removed or changed: %v %s", err, b)
	}
}

// Two creates of one new name race, with and without GLEANN_TAGS. Both used
// to pass the existence check and build into one directory (one failed with
// "assigning slots"; under GLEANN_TAGS the cleanup could then remove the
// winner's index). The server's build takes the index lock, so they run one
// after the other: the second is a rebuild of the first one's index. Neither
// fails, and the index is intact with the tags of the request that ran last.
func TestConcurrentCreatesOfOneName(t *testing.T) {
	for _, tags := range []string{"", "a,b"} {
		t.Run("GLEANN_TAGS="+tags, func(t *testing.T) {
			t.Setenv("GLEANN_TAGS", "")
			_, h, cfg := newIndexedServer(t)
			t.Setenv("GLEANN_TAGS", tags)
			for i := 0; i < 6; i++ {
				name := fmt.Sprintf("race%d", i)
				var wg sync.WaitGroup
				codes := make([]int, 2)
				bodies := make([]string, 2)
				for j, tag := range []string{"a", "b"} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						w := doJSON(h, "POST", "/api/indexes/"+name+"/build",
							`{"items":[{"text":"hello world"},{"text":"second passage"}],"tags":["`+tag+`"]}`, nil)
						codes[j], bodies[j] = w.Code, w.Body.String()
					}()
				}
				wg.Wait()
				for j := range codes {
					if codes[j] != http.StatusOK {
						t.Fatalf("round %d request %d: %d %s, want 200 (serialised)", i, j, codes[j], bodies[j])
					}
				}
				m, err := gleann.GetIndexMeta(cfg.IndexDir, name)
				if err != nil {
					t.Fatalf("round %d: the winner's index is gone: %v", i, err)
				}
				if len(m.Tags) != 1 || (m.Tags[0] != "a" && m.Tags[0] != "b") {
					t.Fatalf("round %d: tags = %v", i, m.Tags)
				}
			}
		})
	}
}

// Another process holds <name>.lock (a CLI build or sync): the build route
// answers 409 and changes nothing.
func TestBuildAnswers409WhenOtherProcessHoldsIndexLock(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	_, h, cfg := newIndexedServer(t)

	cmd := exec.Command(os.Args[0], "-test.run=^TestIndexLockHelperProcess$", "-test.timeout=60s")
	cmd.Env = append(os.Environ(), "GLEANN_INDEX_LOCK_HELPER="+filepath.Join(cfg.IndexDir, "idx.lock"))
	out, err := cmd.StdoutPipe()
	must(t, err)
	must(t, cmd.Start())
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	sc := bufio.NewScanner(out)
	for sc.Scan() && sc.Text() != "locked" {
	}

	w := doJSON(h, "POST", "/api/indexes/idx/build", `{"items":[{"text":"hello"}]}`, nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "another gleann process") {
		t.Fatalf("build while locked: %d %s, want 409", w.Code, w.Body.String())
	}
}

// TestIndexLockHelperProcess is the child of the test above.
func TestIndexLockHelperProcess(t *testing.T) {
	path := os.Getenv("GLEANN_INDEX_LOCK_HELPER")
	if path == "" {
		t.Skip("helper process only")
	}
	l, err := store.AcquireLock(path)
	if err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(2)
	}
	os.Stdout.WriteString("locked\n")
	time.Sleep(time.Minute)
	_ = l.Release()
}

// No test of this package may leave a lock file in the package directory (an
// empty IndexDir once made the build route lock "<name>.lock" in the working
// directory); the build lock refuses an empty directory.
func TestIndexLockRefusesEmptyDirAndLeavesNoFile(t *testing.T) {
	if _, err := indexing.LockIndex(context.Background(), "", "test"); err == nil {
		t.Fatal("LockIndex with an empty index dir must fail")
	}
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	s.config.IndexDir = ""
	w := doJSON(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("name", "test")
		s.handleBuild(w, r)
	}), "POST", "/api/indexes/test/build", `{"texts":["x"]}`, nil)
	if w.Code < 400 {
		t.Fatalf("build with no index dir: %d", w.Code)
	}
	if left, _ := filepath.Glob("*.lock"); len(left) != 0 {
		t.Fatalf("lock files in the package directory: %v", left)
	}
}
