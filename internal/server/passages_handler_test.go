package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/auth"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/pkg/gleann"
)

const listIndexName = "demo"

// buildListIndex creates the format-2 index "demo" in a fresh index directory
// and seeds its record store through seed. It returns the index directory.
func buildListIndex(t *testing.T, seed func(t *testing.T, st *store.Store)) string {
	t.Helper()
	dir := t.TempDir()
	idxDir := filepath.Join(dir, listIndexName)
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"name":"demo","store_format":2,"tags":["code"]}`
	if err := os.WriteFile(filepath.Join(idxDir, listIndexName+".meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(idxDir, listIndexName+".db"), store.Options{})
	if err != nil {
		t.Fatalf("opening the test store: %v", err)
	}
	defer st.Close()
	if seed != nil {
		seed(t, st)
	}
	return dir
}

// seedItems applies one source whose passages are the given (text, meta) pairs.
func seedItems(t *testing.T, st *store.Store, source string, texts ...string) {
	t.Helper()
	ps := make([]store.NewPassage, len(texts))
	for i, text := range texts {
		ps[i] = store.NewPassage{Text: text}
	}
	if _, err := st.ApplySource(context.Background(), store.SourceDelta{Source: source, Passages: ps}); err != nil {
		t.Fatalf("seeding %s: %v", source, err)
	}
}

func newListServer(indexDir string) *Server {
	return &Server{
		config:    gleann.Config{IndexDir: indexDir},
		searchers: make(map[string]*gleann.LeannSearcher),
	}
}

func listRequest(s *Server, path, name string, h http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.SetPathValue("name", name)
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// ── GET /api/indexes/{name}/passages ─────────────────────────────────────

func TestHandleListPassages_DefaultAndPaging(t *testing.T) {
	dir := buildListIndex(t, func(t *testing.T, st *store.Store) {
		seedItems(t, st, "a.md", "one", "two")
		seedItems(t, st, "b.md", "three", "four", "five")
	})
	s := newListServer(dir)

	rec := listRequest(s, "/api/indexes/demo/passages", listIndexName, s.handleListPassages)
	if rec.Code != http.StatusOK {
		t.Fatalf("default: %d %s", rec.Code, rec.Body)
	}
	var all indexPassageListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if all.Count != 5 || len(all.Passages) != 5 {
		t.Fatalf("default page = %d passages (count %d), want 5", len(all.Passages), all.Count)
	}
	if all.Limit != gleann.DefaultPassageListLimit || all.Offset != 0 {
		t.Errorf("default page reports limit %d offset %d, want %d/0", all.Limit, all.Offset, gleann.DefaultPassageListLimit)
	}
	for i := 1; i < len(all.Passages); i++ {
		if all.Passages[i-1].ID >= all.Passages[i].ID {
			t.Fatalf("passages are not id-ordered: %d then %d", all.Passages[i-1].ID, all.Passages[i].ID)
		}
	}
	if all.Passages[0].Text != "one" || all.Passages[0].Source != "a.md" {
		t.Errorf("first passage = %+v, want a.md/one", all.Passages[0])
	}

	rec = listRequest(s, "/api/indexes/demo/passages?limit=2&offset=1", listIndexName, s.handleListPassages)
	if rec.Code != http.StatusOK {
		t.Fatalf("paged: %d %s", rec.Code, rec.Body)
	}
	var page indexPassageListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Passages) != 2 || page.Passages[0].Text != "two" || page.Passages[1].Text != "three" {
		t.Fatalf("page = %+v, want two, three", page.Passages)
	}
	if page.Limit != 2 || page.Offset != 1 {
		t.Errorf("page reports limit %d offset %d, want 2/1", page.Limit, page.Offset)
	}
}

func TestHandleListPassages_InvalidLimitAndOffset(t *testing.T) {
	dir := buildListIndex(t, func(t *testing.T, st *store.Store) { seedItems(t, st, "a.md", "one") })
	s := newListServer(dir)

	for _, q := range []string{
		"limit=0", "limit=-1", "limit=abc", "limit=",
		"limit=" + strconv.Itoa(gleann.MaxPassageListLimit+1),
		"offset=-1", "offset=abc",
	} {
		rec := listRequest(s, "/api/indexes/demo/passages?"+q, listIndexName, s.handleListPassages)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("?%s: got %d, want 400 (%s)", q, rec.Code, rec.Body)
		}
	}
}

func TestHandleListPassages_MissingIndex(t *testing.T) {
	s := newListServer(t.TempDir())
	rec := listRequest(s, "/api/indexes/ghost/passages", "ghost", s.handleListPassages)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404: %s", rec.Code, rec.Body)
	}
	// A listing must not create an empty database for a missing index.
	if _, err := os.Stat(filepath.Join(s.cfg().IndexDir, "ghost", "ghost.db")); !os.IsNotExist(err) {
		t.Errorf("a listing created a database for a missing index: %v", err)
	}
}

func TestHandleListPassages_InvalidName(t *testing.T) {
	s := newListServer(t.TempDir())
	for _, name := range []string{"", "../escape", "bad name"} {
		rec := listRequest(s, "/api/indexes/x/passages", name, s.handleListPassages)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("name %q: got %d, want 400", name, rec.Code)
		}
	}
}

func TestHandleListPassages_OldStoreFormat(t *testing.T) {
	dir := t.TempDir()
	idxDir := filepath.Join(dir, "old")
	if err := os.MkdirAll(idxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxDir, "old.meta.json"), []byte(`{"name":"old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxDir, "old.passages.db"), []byte("bbolt"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newListServer(dir)
	rec := listRequest(s, "/api/indexes/old/passages", "old", s.handleListPassages)
	if rec.Code != http.StatusConflict {
		t.Fatalf("old format: got %d, want 409: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "gleann index migrate old") {
		t.Errorf("the refusal must name the conversion: %s", rec.Body)
	}
}

// ── GET /api/indexes/{name}/sections ─────────────────────────────────────

func seedSections(t *testing.T, st *store.Store) {
	t.Helper()
	apply := func(source string, ps []store.NewPassage) {
		t.Helper()
		if _, err := st.ApplySource(context.Background(), store.SourceDelta{Source: source, Passages: ps}); err != nil {
			t.Fatalf("seeding %s: %v", source, err)
		}
	}
	apply("guide.md", []store.NewPassage{
		{Text: "intro one", Meta: `{"section_id":"s0","section_title":"Introduction","section_path":"Introduction","section_summary":"Overview."}`},
		{Text: "intro two", Meta: `{"section_id":"s0","section_title":"Introduction","section_path":"Introduction","section_summary":"Overview."}`},
		{Text: "detail", Meta: `{"section_id":"s1","section_title":"Details","section_path":"Introduction > Details"}`},
	})
	apply("notes.md", []store.NewPassage{
		{Text: "fallback", Meta: `{"heading":"Fallback heading"}`},
		{Text: "no section"},
	})
}

func TestHandleListSections_GroupsMetadata(t *testing.T) {
	dir := buildListIndex(t, seedSections)
	s := newListServer(dir)

	rec := listRequest(s, "/api/indexes/demo/sections", listIndexName, s.handleListSections)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d: %s", rec.Code, rec.Body)
	}
	var got indexSectionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if got.Count != 3 || len(got.Sections) != 3 {
		t.Fatalf("got %d sections (count %d), want 3: %+v", len(got.Sections), got.Count, got.Sections)
	}
	first := got.Sections[0]
	if first.Source != "guide.md" || first.SectionID != "s0" || first.Title != "Introduction" ||
		first.Path != "Introduction" || first.Summary != "Overview." ||
		first.PassageCount != 2 || first.FirstChunk != 0 || first.LastChunk != 1 {
		t.Errorf("first section = %+v", first)
	}
	if got.Sections[1].SectionID != "s1" || got.Sections[1].Title != "Details" {
		t.Errorf("second section = %+v", got.Sections[1])
	}
	if got.Sections[2].Source != "notes.md" || got.Sections[2].Title != "Fallback heading" {
		t.Errorf("third section = %+v", got.Sections[2])
	}
}

func TestHandleListSections_InvalidLimit(t *testing.T) {
	dir := buildListIndex(t, seedSections)
	s := newListServer(dir)
	for _, q := range []string{"limit=0", "limit=-3", "limit=x", "limit=" + strconv.Itoa(gleann.MaxPassageListLimit+1), "offset=-1"} {
		rec := listRequest(s, "/api/indexes/demo/sections?"+q, listIndexName, s.handleListSections)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("?%s: got %d, want 400", q, rec.Code)
		}
	}
	// A valid page size is accepted and echoed.
	rec := listRequest(s, "/api/indexes/demo/sections?limit=1", listIndexName, s.handleListSections)
	var page indexSectionListResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil {
		t.Fatalf("limit=1: %d %s", rec.Code, rec.Body)
	}
	if len(page.Sections) != 1 || page.Limit != 1 {
		t.Errorf("limit=1 returned %d sections (limit %d)", len(page.Sections), page.Limit)
	}
}

func TestHandleListSections_MissingAndInvalid(t *testing.T) {
	s := newListServer(t.TempDir())
	if rec := listRequest(s, "/api/indexes/ghost/sections", "ghost", s.handleListSections); rec.Code != http.StatusNotFound {
		t.Errorf("missing index: got %d, want 404", rec.Code)
	}
	if rec := listRequest(s, "/api/indexes/x/sections", "../escape", s.handleListSections); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid name: got %d, want 400", rec.Code)
	}
}

// ── access control and scope ─────────────────────────────────────────────

// TestListIndexAccess_TargetAndTags: both listing routes run checkIndexAccess
// like the other index routes: a client target restriction (403) and the
// server-side tag isolation (403) apply.
func TestListIndexAccess_TargetAndTags(t *testing.T) {
	dir := buildListIndex(t, seedSections)
	s := newListServer(dir)

	for _, c := range []struct {
		name string
		h    http.HandlerFunc
		path string
	}{
		{"passages", s.handleListPassages, "/api/indexes/demo/passages"},
		{"sections", s.handleListSections, "/api/indexes/demo/sections"},
	} {
		req := httptest.NewRequest(http.MethodGet, c.path, nil)
		req.SetPathValue("name", listIndexName)
		req.Header.Set("X-Gleann-Target", "other-index")
		rec := httptest.NewRecorder()
		c.h(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with a foreign client target: got %d, want 403", c.name, rec.Code)
		}

		t.Setenv("GLEANN_TAGS", "finance")
		req = httptest.NewRequest(http.MethodGet, c.path, nil)
		req.SetPathValue("name", listIndexName)
		rec = httptest.NewRecorder()
		c.h(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with a mismatching GLEANN_TAGS: got %d, want 403", c.name, rec.Code)
		}

		t.Setenv("GLEANN_TAGS", "code")
		req = httptest.NewRequest(http.MethodGet, c.path, nil)
		req.SetPathValue("name", listIndexName)
		rec = httptest.NewRecorder()
		c.h(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s with a matching GLEANN_TAGS: got %d, want 200 (%s)", c.name, rec.Code, rec.Body)
		}
	}
}

// TestRequiredScope_PassageListing: both routes need ScopeQuery (not admin),
// like GET /api/indexes/{name}, and a query token passes securityMiddleware.
func TestRequiredScope_PassageListing(t *testing.T) {
	for _, path := range []string{"/api/indexes/demo/passages", "/api/indexes/demo/sections"} {
		scope, public := requiredScope(http.MethodGet, path)
		if public || scope != auth.ScopeQuery {
			t.Errorf("%s: scope %q public %v, want query/false", path, scope, public)
		}
	}

	h, _, queryToken := authFixture(t, "127.0.0.1:8099", nil)
	bearer := map[string]string{"Authorization": "Bearer " + queryToken}
	for _, path := range []string{"/api/indexes/demo/passages?limit=10", "/api/indexes/demo/sections"} {
		if rec := do(h, http.MethodGet, path, "127.0.0.1:8099", bearer); rec.Code != http.StatusOK {
			t.Errorf("query token GET %s: got %d, want 200", path, rec.Code)
		}
	}
	if rec := do(h, http.MethodDelete, "/api/indexes/demo", "127.0.0.1:8099", bearer); rec.Code != http.StatusForbidden {
		t.Errorf("query token DELETE: got %d, want 403", rec.Code)
	}
}
