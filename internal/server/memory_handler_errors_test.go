package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// failingMemoryService is a gleann.MemoryService whose every call fails, used
// to reach the "service error -> 500" branches of the memory handlers.
type failingMemoryService struct{}

var errMemBoom = errors.New("memory boom")

func (failingMemoryService) InjectEntities(context.Context, gleann.GraphInjectionPayload) error {
	return errMemBoom
}
func (failingMemoryService) DeleteEntity(context.Context, string) error { return errMemBoom }
func (failingMemoryService) DeleteEdge(context.Context, string, string, string) error {
	return errMemBoom
}
func (failingMemoryService) Traverse(context.Context, string, int) ([]gleann.MemoryGraphNode, []gleann.MemoryGraphEdge, error) {
	return nil, nil, errMemBoom
}

// blockedMemoryDir points GLEANN_MEMORY_DIR below a regular file so that
// opening the memory store fails on every platform.
func blockedMemoryDir(t *testing.T) {
	t.Helper()
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_MEMORY_DIR", filepath.Join(blocker, "sub"))
}

func memReq(mux *http.ServeMux, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestMemoryPool_Get_Errors(t *testing.T) {
	p := newMemoryPool(t.TempDir())
	if _, err := p.get("bad name!"); err == nil {
		t.Error("invalid index name must be rejected")
	}

	blockedMemoryDir(t)
	_, err := p.get("idx")
	if err == nil || !strings.Contains(err.Error(), "open memory store") {
		t.Errorf("store open failure not wrapped: %v", err)
	}
}

func TestMemoryPool_Get_CachesAndUsesSyncerFactory(t *testing.T) {
	t.Setenv("GLEANN_MEMORY_DIR", t.TempDir())
	p := newMemoryPool(t.TempDir())
	defer p.closeAll()
	calls := 0
	p.syncerFactory = func(name string) gleann.VectorSyncer { calls++; return nil }

	a, err := p.get("idx")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.get("idx")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("second get must return the cached service")
	}
	if calls != 1 {
		t.Errorf("syncer factory called %d times, want 1", calls)
	}
}

func TestMemoryHandlers_RequestErrors(t *testing.T) {
	_, mux := newMemoryTestServer(t)
	deny := map[string]string{"X-Gleann-Target": "other-index"}
	tests := []struct {
		name, method, path, body string
		hdr                      map[string]string
		want                     int
	}{
		{"inject bad name", "POST", "/api/memory/bad%20name/inject", `{}`, nil, 400},
		{"inject forbidden", "POST", "/api/memory/idx/inject", `{}`, deny, 403},
		{"inject bad json", "POST", "/api/memory/idx/inject", `{`, nil, 400},
		{"delete node bad name", "DELETE", "/api/memory/bad%20name/nodes/n1", "", nil, 400},
		{"delete node forbidden", "DELETE", "/api/memory/idx/nodes/n1", "", deny, 403},
		{"delete edge bad name", "DELETE", "/api/memory/bad%20name/edges", `{}`, nil, 400},
		{"delete edge forbidden", "DELETE", "/api/memory/idx/edges", `{}`, deny, 403},
		{"delete edge bad json", "DELETE", "/api/memory/idx/edges", `[`, nil, 400},
		{"traverse bad name", "POST", "/api/memory/bad%20name/traverse", `{}`, nil, 400},
		{"traverse forbidden", "POST", "/api/memory/idx/traverse", `{}`, deny, 403},
		{"traverse bad json", "POST", "/api/memory/idx/traverse", `[`, nil, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := memReq(mux, tc.method, tc.path, tc.body, tc.hdr)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestMemoryHandlers_StoreOpenFailure(t *testing.T) {
	_, mux := newMemoryTestServer(t)
	blockedMemoryDir(t)
	tests := []struct{ name, method, path, body string }{
		{"inject", "POST", "/api/memory/idx/inject", `{}`},
		{"delete node", "DELETE", "/api/memory/idx/nodes/n1", ""},
		{"delete edge", "DELETE", "/api/memory/idx/edges", `{"from":"a","to":"b","relation_type":"R"}`},
		{"traverse", "POST", "/api/memory/idx/traverse", `{"start_id":"a"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := memReq(mux, tc.method, tc.path, tc.body, nil)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "open memory store") {
				t.Errorf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMemoryHandlers_ServiceFailure(t *testing.T) {
	s, mux := newMemoryTestServer(t)
	s.memoryPool.handles["idx"] = failingMemoryService{}
	tests := []struct{ name, method, path, body, want string }{
		{"inject", "POST", "/api/memory/idx/inject", `{}`, "inject entities"},
		{"delete node", "DELETE", "/api/memory/idx/nodes/n1", "", "delete entity"},
		{"delete edge", "DELETE", "/api/memory/idx/edges", `{"from":"a","to":"b","relation_type":"R"}`, "delete edge"},
		{"traverse", "POST", "/api/memory/idx/traverse", `{"start_id":"a"}`, "traverse"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := memReq(mux, tc.method, tc.path, tc.body, nil)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), tc.want) ||
				!strings.Contains(w.Body.String(), errMemBoom.Error()) {
				t.Errorf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}
