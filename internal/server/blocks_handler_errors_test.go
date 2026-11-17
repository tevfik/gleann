package server

import (
	"net/http"
	"strings"
	"testing"
)

// blocksFullMux registers every /api/blocks route (the shared test helper
// omits compact) against s.
func blocksFullMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/blocks/search", s.handleSearchBlocks)
	mux.HandleFunc("GET /api/blocks/context", s.handleBlockContext)
	mux.HandleFunc("GET /api/blocks/stats", s.handleBlockStats)
	mux.HandleFunc("GET /api/blocks", s.handleListBlocks)
	mux.HandleFunc("POST /api/blocks", s.handleAddBlock)
	mux.HandleFunc("POST /api/blocks/compact", s.handleCompactBlocks)
	mux.HandleFunc("DELETE /api/blocks/{id}", s.handleDeleteBlock)
	mux.HandleFunc("DELETE /api/blocks", s.handleClearBlocks)
	return mux
}

type blockCall struct {
	name, method, path, body string
	hdr                      map[string]string
}

func TestBlocks_RequestValidationErrors(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	mux := blocksFullMux(s)
	mismatch := map[string]string{"X-Gleann-Target": "a"}
	tests := []struct {
		blockCall
		want int
	}{
		{blockCall{"list scope mismatch", "GET", "/api/blocks?scope=b", "", mismatch}, 403},
		{blockCall{"list bad tier", "GET", "/api/blocks?tier=forever", "", nil}, 400},
		{blockCall{"add bad json", "POST", "/api/blocks", "{", nil}, 400},
		{blockCall{"add header/body mismatch", "POST", "/api/blocks", `{"content":"x","scope":"b"}`, mismatch}, 403},
		{blockCall{"add bad tier", "POST", "/api/blocks", `{"content":"x","tier":"forever"}`, nil}, 400},
		{blockCall{"add bad expires_in", "POST", "/api/blocks", `{"content":"x","expires_in":"soon"}`, nil}, 400},
		{blockCall{"clear bad tier", "DELETE", "/api/blocks?tier=forever", "", nil}, 400},
		{blockCall{"search missing q", "GET", "/api/blocks/search", "", nil}, 400},
		{blockCall{"search scope mismatch", "GET", "/api/blocks/search?q=x&scope=b", "", mismatch}, 403},
		{blockCall{"context scope mismatch", "GET", "/api/blocks/context?scope=b", "", mismatch}, 403},
		{blockCall{"delete unknown id", "DELETE", "/api/blocks/does-not-exist", "", nil}, 404},
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

func TestBlocks_HeaderTargetBecomesScope(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	mux := blocksFullMux(s)
	hdr := map[string]string{"X-Gleann-Index": "proj"}

	w := memReq(mux, "POST", "/api/blocks", `{"content":"scoped fact"}`, hdr)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"scope":"proj"`) {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	// Scoped list/search see it; other scopes do not.
	for _, path := range []string{"/api/blocks", "/api/blocks/search?q=scoped"} {
		w = memReq(mux, "GET", path, "", hdr)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "scoped fact") {
			t.Errorf("%s with scope: %d %s", path, w.Code, w.Body.String())
		}
	}
	w = memReq(mux, "GET", "/api/blocks?scope=other", "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "scoped fact") {
		t.Errorf("foreign scope leaked block: %d %s", w.Code, w.Body.String())
	}
	w = memReq(mux, "GET", "/api/blocks/context?target=proj&format=xml", "", nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/xml") {
		t.Errorf("scoped xml context: %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	// target alias in the body acts as scope.
	w = memReq(mux, "POST", "/api/blocks", `{"content":"alias fact","target":"viaalias","expires_in":"1h"}`, nil)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"scope":"viaalias"`) ||
		!strings.Contains(w.Body.String(), "expires_at") {
		t.Errorf("target alias/expiry: %d %s", w.Code, w.Body.String())
	}
}

func TestBlocks_Compact(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	mux := blocksFullMux(s)
	if w := memReq(mux, "POST", "/api/blocks/compact?min_validity=0.5", "", nil); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"min_validity":0.5`) {
		t.Errorf("compact: %d %s", w.Code, w.Body.String())
	}
	// An unparsable min_validity falls back to the default 0.2.
	if w := memReq(mux, "POST", "/api/blocks/compact?min_validity=abc", "", nil); w.Code != 200 ||
		!strings.Contains(w.Body.String(), `"min_validity":0.2`) {
		t.Errorf("compact default: %d %s", w.Code, w.Body.String())
	}
}

func allBlockCalls() []blockCall {
	return []blockCall{
		{"list", "GET", "/api/blocks", "", nil},
		{"list scoped", "GET", "/api/blocks?scope=s", "", nil},
		{"add", "POST", "/api/blocks", `{"content":"x"}`, nil},
		{"delete", "DELETE", "/api/blocks/abc", "", nil},
		{"clear", "DELETE", "/api/blocks", "", nil},
		{"clear tier", "DELETE", "/api/blocks?tier=long", "", nil},
		{"search", "GET", "/api/blocks/search?q=x", "", nil},
		{"search scoped", "GET", "/api/blocks/search?q=x&scope=s", "", nil},
		{"context", "GET", "/api/blocks/context", "", nil},
		{"context scoped", "GET", "/api/blocks/context?scope=s", "", nil},
		{"stats", "GET", "/api/blocks/stats", "", nil},
		{"compact", "POST", "/api/blocks/compact", "", nil},
	}
}

// With the memory directory unusable, every handler reports that the store
// could not be opened.
func TestBlocks_StoreOpenFailure(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	s.blockMem = nil
	blockedMemoryDir(t)
	mux := blocksFullMux(s)
	for _, tc := range allBlockCalls() {
		t.Run(tc.name, func(t *testing.T) {
			w := memReq(mux, tc.method, tc.path, tc.body, tc.hdr)
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "open memory store") {
				t.Errorf("got %d %s", w.Code, w.Body.String())
			}
		})
	}
}

// With the underlying store closed, every handler maps the storage error to 500
// (404 for forget, which reports every failure as not found; compact is
// best-effort and still answers 200).
func TestBlocks_StoreClosedFailure(t *testing.T) {
	s, _ := newBlocksTestServer(t)
	if err := s.blockMem.Close(); err != nil {
		t.Fatal(err)
	}
	mux := blocksFullMux(s)
	for _, tc := range allBlockCalls() {
		t.Run(tc.name, func(t *testing.T) {
			w := memReq(mux, tc.method, tc.path, tc.body, tc.hdr)
			want := http.StatusInternalServerError
			switch tc.name {
			case "delete":
				want = http.StatusNotFound
			case "compact":
				want = http.StatusOK // Compact tolerates an unreadable store
			}
			if w.Code != want {
				t.Errorf("got %d, want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
}
