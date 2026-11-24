package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// writeSearcherError maps a failed searcher load by class: a corrupt or
// unreadable index is a server fault, not "not found".
func TestWriteSearcherErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		want  int
		retry bool
	}{
		{"not exist", fmt.Errorf("load: %w", fs.ErrNotExist), http.StatusNotFound, false},
		{"not a directory", fmt.Errorf("load: %w", syscall.ENOTDIR), http.StatusNotFound, false},
		{"old format", fmt.Errorf("x: %w", gleann.ErrOldStoreFormat), http.StatusConflict, false},
		{"busy", fmt.Errorf("x: %w", errSearcherBusy), http.StatusServiceUnavailable, true},
		{"corrupt", errors.New("unmarshal index metadata /tmp/x/idx.meta.json: unexpected end of JSON input"), http.StatusInternalServerError, false},
		{"model mismatch", errors.New("model mismatch: index uses 'a', current is 'b'"), http.StatusInternalServerError, false},
		{"permission", fmt.Errorf("open: %w", fs.ErrPermission), http.StatusInternalServerError, false},
		{"deadline", fmt.Errorf("load: %w", context.DeadlineExceeded), http.StatusServiceUnavailable, true},
		{"client gone", fmt.Errorf("load: %w", context.Canceled), 499, false},
		{"shutdown, client alive", fmt.Errorf("load: %w", context.Canceled), http.StatusServiceUnavailable, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.name == "client gone" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			writeSearcherError(w, req, "idx", tc.err)
			if w.Code != tc.want {
				t.Errorf("status %d, want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
			if tc.want >= 500 || tc.want == 404 {
				if strings.Contains(w.Body.String(), "/tmp") || strings.Contains(w.Body.String(), "unmarshal") {
					t.Errorf("body leaks internal detail: %s", w.Body.String())
				}
			}
			if tc.want == 499 && w.Body.Len() != 0 {
				t.Errorf("client-gone answer has a body: %s", w.Body.String())
			}
			if got := w.Header().Get("Retry-After") != ""; got != tc.retry {
				t.Errorf("Retry-After set = %v, want %v", got, tc.retry)
			}
		})
	}
}

// Every handler that answers through writeSearcherError documents the codes
// it can produce, and produces them for real indexes.
func TestSearcherErrorRoutesSpecAndBehaviour(t *testing.T) {
	t.Setenv("GLEANN_TAGS", "")
	s, mux, dir := patchTestServer(t)
	mux.HandleFunc("POST /api/indexes/{name}/search", s.handleSearch)
	mux.HandleFunc("POST /api/indexes/{name}/ask", s.handleAsk)
	routes := []struct{ method, spec, path, body string }{
		{http.MethodGet, "/api/indexes/{name}", "/api/indexes/%s", ""},
		{http.MethodPost, "/api/indexes/{name}/search", "/api/indexes/%s/search", `{"query":"q"}`},
		{http.MethodPost, "/api/indexes/{name}/ask", "/api/indexes/%s/ask", `{"question":"q"}`},
	}
	// "okindex" has valid metadata and no store: old format (409).
	// "broken" is a format-2 index whose database file is garbage: a server
	// fault (500).
	if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	bm, _ := json.Marshal(map[string]any{"name": "broken", "store_format": 2})
	if err := os.WriteFile(filepath.Join(dir, "broken", "broken.meta.json"), bm, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken", "broken.db"), []byte("this is not a sqlite database, just bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rt := range routes {
		op := specOperation(t, s, rt.spec, strings.ToLower(rt.method))
		responses := op["responses"].(map[string]any)
		for _, code := range []string{"403", "404", "409", "500", "503"} {
			if _, ok := responses[code]; !ok {
				t.Errorf("%s %s: spec does not document %s", rt.method, rt.spec, code)
			}
		}
		for name, want := range map[string]int{"ghost": 404, "okindex": 409, "broken": 500} {
			got := serve(mux, rt.method, fmt.Sprintf(rt.path, name), rt.body, nil)
			if got.Code != want {
				t.Errorf("%s %s: %d, want %d (%s)", rt.method, name, got.Code, want, got.Body.String())
			}
			if _, ok := responses[fmt.Sprint(got.Code)]; !ok {
				t.Errorf("%s %s: answers %d, which the spec does not document", rt.method, rt.spec, got.Code)
			}
		}
	}
}
