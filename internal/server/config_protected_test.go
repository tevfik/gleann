package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// allowed_roots, index_dir, auth and cors_origins are read-only over the API:
// an admin client must not widen the path policy or turn authentication off.
func TestUpdateConfigRefusesProtectedKeys(t *testing.T) {
	_, cfgPath := isolateHome(t)
	cfg := gleann.DefaultConfig()
	cfg.AllowedRoots = []string{"/srv/data"}
	cfg.CORSOrigins = []string{"https://app.example.com"}
	s := &Server{config: cfg, searchers: map[string]*gleann.LeannSearcher{}}

	for _, body := range []string{
		`{"allowed_roots":["/"]}`,
		`{"allowed_roots":[]}`,
		`{"index_dir":"/etc"}`,
		`{"auth":"off"}`,
		`{"cors_origins":["https://evil.example.com"]}`,
	} {
		w := postConfig(s, body)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cannot be changed through the API") {
			t.Errorf("%s: got %d %s", body, w.Code, w.Body.String())
		}
	}
	if got := s.cfg().AllowedRoots; len(got) != 1 || got[0] != "/srv/data" {
		t.Errorf("allowed_roots changed to %v", got)
	}
	if _, err := os.Stat(cfgPath); err == nil {
		t.Error("a refused update wrote config.json")
	}

	// A value sent back unchanged, and other keys, still work.
	if w := postConfig(s, `{"allowed_roots":["/srv/data"],"top_k":3}`); w.Code != http.StatusOK {
		t.Errorf("unchanged protected value: got %d %s", w.Code, w.Body.String())
	}
}

func TestSettingsPutRefusesProtectedKeys(t *testing.T) {
	reached := false
	h := guardSettingsPut(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(w, r)
		return w
	}
	if w := put(`{"values":{"allowed_roots":"/"}}`); w.Code != http.StatusForbidden || reached {
		t.Fatalf("allowed_roots: %d reached=%v", w.Code, reached)
	}
	if w := put(`{"values":{"auth":"off","top_k":3}}`); w.Code != http.StatusForbidden || reached {
		t.Fatalf("auth: %d reached=%v", w.Code, reached)
	}
	for _, body := range []string{
		`{"values":{"auth":"off"}} x`,
		`{"values":{"auth":"off"}}{}`,
		`{"values":{"auth":"off"}} {"values":{"top_k":1}}`,
		`{"values":`,
	} {
		if w := put(body); w.Code != http.StatusBadRequest || reached {
			t.Fatalf("%s: %d reached=%v", body, w.Code, reached)
		}
	}
	if w := put(`{"values":{"top_k":3}}`); w.Code != http.StatusOK || !reached {
		t.Fatalf("other key: %d reached=%v", w.Code, reached)
	}
}
