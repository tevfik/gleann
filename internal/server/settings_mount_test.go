package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/auth"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestSettingsRouteMountedAndAdminOnly(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	var hits int
	s.SetSettingsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	h := s.buildMux()
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(m, "/api/settings", nil))
	}
	if hits != 2 {
		t.Fatalf("GET and PUT /api/settings must reach the settings handler, got %d calls", hits)
	}
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		if scope, public := requiredScope(m, "/api/settings"); public || scope != auth.ScopeAdmin {
			t.Errorf("%s /api/settings must need an admin token, got scope=%q public=%v", m, scope, public)
		}
	}
}

func TestApplyConfigSwapsEmbedderAndConfig(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	next := cfg
	next.EmbeddingModel = "new-model"
	next.SearchConfig.UseReranker = true
	s.ApplyConfig(next)
	if got := s.CurrentConfig(); got.EmbeddingModel != "new-model" || !got.SearchConfig.UseReranker {
		t.Fatalf("ApplyConfig did not take effect: %+v", got.SearchConfig)
	}
	if s.getEmbedder() == nil || s.getEmbedder().ModelName() != "new-model" {
		t.Fatal("ApplyConfig must rebuild the embedder for the new model")
	}
}

func TestRateLimiterConfigRatesYieldToEnv(t *testing.T) {
	t.Setenv("GLEANN_RATE_LIMIT", "")
	t.Setenv("GLEANN_RATE_BURST", "7")
	rl := newRateLimiter()
	rl.applyConfigRates(5, 300)
	if rl.rate != 5 {
		t.Errorf("config rate_limit must apply when the env is unset, got %v", rl.rate)
	}
	if rl.burst != 7 {
		t.Errorf("GLEANN_RATE_BURST must win over rate_burst, got %v", rl.burst)
	}
	rl.applyConfigRates(0, 0)
	if rl.rate != 5 {
		t.Errorf("zero keeps the current rate, got %v", rl.rate)
	}
}

func TestSetStorageEndpointValidates(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	h := s.buildMux()
	post := func(path, body string) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return w.Code
	}
	if code := post("/api/indexes/docs/storage", `{"mode":"store"}`); code != http.StatusNotImplemented {
		t.Errorf("without a switcher: want 501, got %d", code)
	}
	s.SetStorageSwitcher(func(gleann.Config, string, string) error { return nil })
	h = s.buildMux()
	if code := post("/api/indexes/docs/storage", `{"mode":"fast"}`); code != http.StatusBadRequest {
		t.Errorf("bad mode: want 400, got %d", code)
	}
	if code := post("/api/indexes/missing/storage", `{"mode":"store"}`); code != http.StatusNotFound {
		t.Errorf("unknown index: want 404, got %d", code)
	}
	if scope, _ := requiredScope(http.MethodPost, "/api/indexes/docs/storage"); scope != auth.ScopeAdmin {
		t.Errorf("converting an index needs an admin token, got %q", scope)
	}
}
