package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func multiSearchServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	return &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}, dir
}

func postMultiSearch(s *Server, url, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.handleMultiSearch(w, r)
	return w
}

func multiSearchCount(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var resp multiSearchResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	if resp.Results == nil {
		t.Error("results must be an empty array, not null")
	}
	return resp.Count
}

func TestMultiSearch_TargetResolutionErrors(t *testing.T) {
	s, dir := multiSearchServer(t)
	createMockIndexMeta(t, dir, "pub", true, []string{"work"})
	createMockIndexMeta(t, dir, "priv", false, nil)

	tests := []struct {
		name, body string
		want       int
	}{
		{"invalid index name", `{"query":"q","target":"bad name!"}`, 400},
		{"unknown index", `{"query":"q","target":"ghost"}`, 404},
		{"private index", `{"query":"q","target":"priv"}`, 403},
		{"private among list", `{"query":"q","targets":["pub","priv"]}`, 403},
		{"project shorthand unknown", `{"query":"q","project":"ghost"}`, 404},
		{"comma list in index field", `{"query":"q","index":"pub,ghost"}`, 404},
		{"indexes list", `{"query":"q","indexes":["ghost"]}`, 404},
		{"target as json array", `{"query":"q","target":["pub","ghost"]}`, 404},
		{"non-string target entries ignored, falls back to all accessible", `{"query":"q","target":[1,2]}`, 500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := postMultiSearch(s, "/api/search", tc.body, nil)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestMultiSearch_ClientTargetConstraint(t *testing.T) {
	s, dir := multiSearchServer(t)
	createMockIndexMeta(t, dir, "alpha", true, nil)
	createMockIndexMeta(t, dir, "beta", true, nil)

	// The constraint arrives via header, X-Gleann-Index, ?target= or ?index=.
	for name, tc := range map[string]struct {
		url string
		hdr map[string]string
	}{
		"X-Gleann-Target": {"/api/search", map[string]string{"X-Gleann-Target": "alpha"}},
		"X-Gleann-Index":  {"/api/search", map[string]string{"X-Gleann-Index": "alpha"}},
		"target query":    {"/api/search?target=alpha", nil},
		"index query":     {"/api/search?index=alpha", nil},
	} {
		t.Run(name, func(t *testing.T) {
			w := postMultiSearch(s, tc.url, `{"query":"q","target":"beta"}`, tc.hdr)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `restricted to \"alpha\"`) {
				t.Errorf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
	// Case-insensitive match is accepted (it then proceeds to the search).
	w := postMultiSearch(s, "/api/search", `{"query":"q","target":"alpha"}`, map[string]string{"X-Gleann-Target": "ALPHA"})
	if w.Code == http.StatusForbidden {
		t.Errorf("matching target (case-insensitive) rejected: %s", w.Body.String())
	}
}

func TestMultiSearch_NoAccessibleIndexesYieldsEmptyResult(t *testing.T) {
	s, dir := multiSearchServer(t)
	createMockIndexMeta(t, dir, "priv", false, nil)
	createMockIndexMeta(t, dir, "tagged", true, []string{"team"})

	t.Run("no target, only private index", func(t *testing.T) {
		// Make "tagged" inaccessible too via GLEANN_TAGS.
		t.Setenv("GLEANN_TAGS", "other")
		w := postMultiSearch(s, "/api/search", `{"query":"q"}`, nil)
		if w.Code != 200 || multiSearchCount(t, w) != 0 {
			t.Errorf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("tag target with no accessible match", func(t *testing.T) {
		w := postMultiSearch(s, "/api/search", `{"query":"q","target":"@nosuchtag"}`, nil)
		if w.Code != 200 || multiSearchCount(t, w) != 0 {
			t.Errorf("got %d %s", w.Code, w.Body.String())
		}
	})
	t.Run("tag target excluded by GLEANN_TAGS", func(t *testing.T) {
		t.Setenv("GLEANN_TAGS", "other")
		w := postMultiSearch(s, "/api/search", `{"query":"q","target":"@team"}`, nil)
		if w.Code != 200 || multiSearchCount(t, w) != 0 {
			t.Errorf("got %d %s", w.Code, w.Body.String())
		}
	})
}

func TestMultiSearch_ListingErrorsAreServerErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = filepath.Join(file, "idx") // parent is a regular file
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}

	for name, body := range map[string]string{
		"all indexes": `{"query":"q"}`,
		"by tag":      `{"query":"q","target":"@work"}`,
	} {
		t.Run(name, func(t *testing.T) {
			w := postMultiSearch(s, "/api/search", body, nil)
			// Either the listing surfaces an error (500) or treats the
			// unreadable directory as having no indexes (empty 200); it must
			// never fall through to a search.
			switch w.Code {
			case http.StatusInternalServerError:
			case http.StatusOK:
				if multiSearchCount(t, w) != 0 {
					t.Errorf("unexpected results: %s", w.Body.String())
				}
			default:
				t.Errorf("status = %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

// A search over a real (but unbuilt) index fails inside gleann.SearchMultiple;
// every request option must be accepted on the way there and the failure
// reported as a 500.
func TestMultiSearch_SearchFailureIsReported(t *testing.T) {
	s, dir := multiSearchServer(t)
	createMockIndexMeta(t, dir, "pub", true, nil)
	body := `{"query":"q","target":"pub","top_k":3,"hybrid_alpha":0.5,"min_score":0.1,` +
		`"metadata_filters":[{"field":"lang","operator":"eq","value":"go"}],"filter_logic":"and",` +
		`"rerank":true,"include_tests":true,"kind":"function"}`
	w := postMultiSearch(s, "/api/search", body, nil)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "multi-search failed") {
		t.Errorf("got %d %s", w.Code, w.Body.String())
	}
}
