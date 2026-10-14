package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── Path traversal via {name} ────────────────────────────────────────────

func TestNameTraversalRejected(t *testing.T) {
	root := t.TempDir()
	indexDir := filepath.Join(root, "indexes")
	sibling := filepath.Join(root, "sibling")
	for _, d := range []string{indexDir, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(sibling, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	pluginsDir := filepath.Join(home, ".gleann", "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = indexDir
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	h := withMiddleware(s.buildMux())

	cases := []struct {
		method, path string
	}{
		{http.MethodDelete, "/api/indexes/%2E%2E"},
		{http.MethodDelete, "/api/indexes/..%2Fsibling"},
		{http.MethodDelete, "/api/indexes/..%2F..%2Ftmp"},
		{http.MethodDelete, "/api/indexes/%2E%2E%2F%2E%2E%2Fsibling"},
		{http.MethodDelete, "/api/indexes/-x"},
		{http.MethodDelete, "/api/indexes/.hidden"},
		{http.MethodGet, "/api/indexes/%2E%2E"},
		{http.MethodPatch, "/api/indexes/%2E%2E"},
		{http.MethodPost, "/api/indexes/%2E%2E/search"},
		{http.MethodPost, "/api/indexes/..%2Fsibling/ask"},
		{http.MethodPost, "/api/indexes/-x/build"},
		{http.MethodPost, "/api/indexes/%2E%2E/update"},
		{http.MethodPost, "/api/indexes/--help/index-path"},
		{http.MethodPost, "/api/indexes/%2E%2E/watch"},
		{http.MethodPost, "/api/indexes/%2E%2E/upload"},
		{http.MethodGet, "/api/graph/%2E%2E"},
		{http.MethodPost, "/api/graph/..%2Fsibling/index"},
		{http.MethodDelete, "/api/plugins/%2E%2E"},
		{http.MethodDelete, "/api/plugins/..%2F..%2F.gleann"},
		{http.MethodDelete, "/api/plugins/-rf"},
		{http.MethodPost, "/api/plugins/%2E%2E/install"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}

	for _, p := range []string{keep, indexDir, pluginsDir} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s was removed: %v", p, err)
		}
	}
}

func TestNameValidation_HeaderAndBody(t *testing.T) {
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	h := s.buildMux()

	// X-Gleann-Index header.
	req := httptest.NewRequest(http.MethodGet, "/api/indexes", nil)
	req.Header.Set("X-Gleann-Index", "../etc")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("list with bad header: expected 400, got %d", w.Code)
	}

	// Multi-search body.
	body := `{"query":"q","indexes":["../etc"]}`
	req = httptest.NewRequest(http.MethodPost, "/api/search", strings.NewReader(body))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("multi-search with bad name: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// OpenAI proxy model name.
	body = `{"model":"gleann/..","messages":[{"role":"user","content":"hi"}]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("proxy with bad index: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Model download validation ────────────────────────────────────────────

func TestDownloadModelValidation(t *testing.T) {
	winHome := t.TempDir()
	t.Setenv("HOME", winHome)
	t.Setenv("USERPROFILE", winHome)
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "")
	s := &Server{}

	cases := []struct {
		name, url, file string
	}{
		{"traversal", "https://example.com/m.gguf", "../../.bashrc.gguf"},
		{"abs path", "https://example.com/m.gguf", "/etc/m.gguf"},
		{"subdir", "https://example.com/m.gguf", "sub/m.gguf"},
		{"backslash", "https://example.com/m.gguf", `..\m.gguf`},
		{"dotdot", "https://example.com/m.gguf", ".."},
		{"hidden", "https://example.com/m.gguf", ".m.gguf"},
		{"bad ext", "https://example.com/m.gguf", "config.json"},
		{"no ext", "https://example.com/m.gguf", "model"},
		{"file scheme", "file:///etc/passwd", "m.gguf"},
		{"ftp scheme", "ftp://example.com/m.gguf", "m.gguf"},
		{"plain http public", "http://example.com/m.gguf", "m.gguf"},
		{"loopback blocked", "http://127.0.0.1:1/m.gguf", "m.gguf"},
		{"metadata ip", "https://169.254.169.254/latest", "m.gguf"},
		{"private ip", "https://10.0.0.1/m.gguf", "m.gguf"},
		{"no host", "https:///m.gguf", "m.gguf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(DownloadModelRequest{URL: tc.url, Filename: tc.file})
			req := httptest.NewRequest(http.MethodPost, "/api/models/download", bytes.NewReader(body))
			w := httptest.NewRecorder()
			s.handleDownloadModel(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestValidateModelFilename_Allowed(t *testing.T) {
	for _, f := range []string{"bge-micro-v2-q4_k_m.gguf", "Llama-3.2-1B-Instruct-Q4_K_M.gguf", "m.EIFM", "w.safetensors", "w.bin"} {
		if err := validateModelFilename(f); err != nil {
			t.Errorf("validateModelFilename(%q) = %v", f, err)
		}
	}
}

func TestModelDownloadClient_BlocksRebindingAtDial(t *testing.T) {
	t.Setenv("GLEANN_MODEL_DOWNLOAD_ALLOW_PRIVATE", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer srv.Close()
	// Even if a URL slipped past validation (e.g. DNS changed after the
	// check), the dialer must refuse a loopback address.
	resp, err := modelDownloadClient.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected dial to loopback to be refused")
	}
}

func TestWebhookClient_NoRedirectsAndDialGuard(t *testing.T) {
	// Redirect target that must never be reached.
	var hit atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit.Store(true)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "1")
	req, _ := http.NewRequest(http.MethodPost, redirector.URL, strings.NewReader("{}"))
	resp, err := webhookHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Errorf("expected redirect response to be returned as-is, got %d", resp.StatusCode)
	}
	if hit.Load() {
		t.Error("redirect was followed")
	}

	// Without the override, loopback is refused at dial time even though
	// no validation ran (simulates DNS rebinding after registration).
	t.Setenv("GLEANN_WEBHOOK_ALLOW_PRIVATE", "")
	req, _ = http.NewRequest(http.MethodPost, target.URL, strings.NewReader("{}"))
	if resp, err := webhookHTTPClient.Do(req); err == nil {
		resp.Body.Close()
		t.Fatal("expected dial to loopback to be refused")
	}
	if hit.Load() {
		t.Error("restricted target was reached")
	}
}

// ── Config secrets ───────────────────────────────────────────────────────

func TestConfigSecretsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfgPath := filepath.Join(home, ".gleann", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing world-readable file with an unknown key that must survive.
	if err := os.WriteFile(cfgPath, []byte(`{"anthropic_api_key":"keep-me","openai_api_key":"sk-live"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.OpenAIAPIKey = "sk-live"
	cfg.OllamaAPIKey = "ol-live"
	cfg.SearchConfig.RerankerConfig.APIKey = "rr-live"
	s := &Server{config: cfg, searchers: make(map[string]*gleann.LeannSearcher)}
	h := s.buildMux()

	// GET redacts.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET config: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "sk-live") || strings.Contains(w.Body.String(), "rr-live") || strings.Contains(w.Body.String(), "ol-live") {
		t.Fatalf("GET /api/config leaked a secret: %s", w.Body.String())
	}
	var got gleann.Config
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OpenAIAPIKey != gleann.RedactedSecret || got.SearchConfig.RerankerConfig.APIKey != gleann.RedactedSecret {
		t.Fatalf("expected sentinel, got %q / %q", got.OpenAIAPIKey, got.SearchConfig.RerankerConfig.APIKey)
	}
	if got.OllamaAPIKey != gleann.RedactedSecret {
		t.Fatalf("ollama key not redacted: %q", got.OllamaAPIKey)
	}
	if s.cfg().OpenAIAPIKey != "sk-live" || s.cfg().OllamaAPIKey != "ol-live" {
		t.Fatal("GET mutated the live config")
	}

	post := func(c gleann.Config) {
		t.Helper()
		b, _ := json.Marshal(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(b)))
		if w.Code != http.StatusOK {
			t.Fatalf("POST config: %d %s", w.Code, w.Body.String())
		}
	}
	readFile := func() map[string]any {
		t.Helper()
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	// POST of the redacted config keeps the secrets.
	got.LLMModel = "changed"
	post(got)
	if c := s.cfg(); c.OpenAIAPIKey != "sk-live" || c.SearchConfig.RerankerConfig.APIKey != "rr-live" || c.LLMModel != "changed" {
		t.Fatalf("sentinel should keep secrets: %+v", c)
	}
	m := readFile()
	if m["openai_api_key"] != "sk-live" || m["anthropic_api_key"] != "keep-me" || m["completed"] != true {
		t.Fatalf("unexpected file contents: %v", m)
	}
	// Windows has no POSIX permission bits (Stat reports 0666), so the 0600
	// check only means something elsewhere.
	if fi, err := os.Stat(cfgPath); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) {
		t.Fatalf("config file mode = %v (err %v), want 0600", fi.Mode().Perm(), err)
	}
	if data, _ := os.ReadFile(cfgPath); strings.Contains(string(data), gleann.RedactedSecret) {
		t.Fatal("sentinel was persisted to disk")
	}

	// Empty string clears (in memory and on disk).
	got.OpenAIAPIKey = ""
	post(got)
	if s.cfg().OpenAIAPIKey != "" {
		t.Fatal("empty value should clear the secret")
	}
	if _, ok := readFile()["openai_api_key"]; ok {
		t.Fatal("cleared secret still on disk")
	}
	if s.cfg().SearchConfig.RerankerConfig.APIKey != "rr-live" {
		t.Fatal("untouched sentinel secret should be kept")
	}
}

func TestUpdateConfig_SaveErrorReturns500(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Make ~/.gleann a file so the config cannot be written.
	if err := os.WriteFile(filepath.Join(home, ".gleann"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}
	b, _ := json.Marshal(gleann.DefaultConfig())
	w := httptest.NewRecorder()
	s.handleUpdateConfig(w, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(b)))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestProxyModels_SentinelSubstitution(t *testing.T) {
	var gotAuth atomic.Value
	gotAuth.Store("")
	configured := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer configured.Close()
	var otherAuth atomic.Value
	otherAuth.Store("")
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherAuth.Store(r.Header.Get("Authorization"))
		w.Write([]byte(`{"data":[]}`))
	}))
	defer other.Close()

	cfg := gleann.DefaultConfig()
	cfg.OpenAIBaseURL = configured.URL + "/"
	cfg.OpenAIAPIKey = "sk-real"
	s := &Server{config: cfg}

	call := func(host, key string) int {
		u := "/api/proxy/models?provider=openai&host=" + host + "&apikey=" + key
		w := httptest.NewRecorder()
		s.handleProxyModels(w, httptest.NewRequest(http.MethodGet, u, nil))
		return w.Code
	}

	if code := call(configured.URL, gleann.RedactedSecret); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if gotAuth.Load() != "Bearer sk-real" {
		t.Errorf("configured key not substituted, got %q", gotAuth.Load())
	}
	if code := call(other.URL, gleann.RedactedSecret); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if otherAuth.Load() != "" {
		t.Errorf("configured key leaked to non-configured host: %q", otherAuth.Load())
	}
	call(configured.URL, "sk-typed")
	if gotAuth.Load() != "Bearer sk-typed" {
		t.Errorf("explicit key should be forwarded, got %q", gotAuth.Load())
	}
}

// ── Concurrency ──────────────────────────────────────────────────────────

// newFakeOllama returns an httptest server implementing /api/embed with
// deterministic 8-dim embeddings.
func newFakeOllama(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input any `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var texts []string
		switch v := req.Input.(type) {
		case []any:
			for _, s := range v {
				str, _ := s.(string)
				texts = append(texts, str)
			}
		case string:
			texts = []string{v}
		}
		embs := make([][]float32, len(texts))
		for i, txt := range texts {
			e := make([]float32, 8)
			for j := range e {
				e[j] = float32((len(txt)+j*7)%13) / 13
			}
			e[i%8] += 1
			embs[i] = e
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newIndexedServer builds a server with a real index named "idx".
func newIndexedServer(t *testing.T) (*Server, http.Handler, gleann.Config) {
	t.Helper()
	winHome := t.TempDir()
	t.Setenv("HOME", winHome)
	t.Setenv("USERPROFILE", winHome)
	ollama := newFakeOllama(t)
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.EmbeddingProvider = "ollama"
	cfg.EmbeddingModel = "test-model"
	cfg.OllamaHost = ollama.URL
	emb := embedding.NewComputer(embedding.Options{
		Provider: embedding.ProviderOllama,
		Model:    cfg.EmbeddingModel,
		BaseURL:  cfg.OllamaHost,
	})
	s := &Server{config: cfg, embedder: emb, searchers: make(map[string]*gleann.LeannSearcher)}
	// Registered after the TempDirs above, so it runs before they are removed:
	// Windows cannot delete index files a cached searcher still holds open.
	t.Cleanup(s.invalidateAllSearchers)
	h := s.buildMux()

	texts := []string{}
	for i := 0; i < 20; i++ {
		texts = append(texts, strings.Repeat("alpha beta gamma ", i+1))
	}
	b, _ := json.Marshal(map[string]any{"texts": texts})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/idx/build", bytes.NewReader(b)))
	if w.Code != http.StatusOK {
		t.Fatalf("build: %d %s", w.Code, w.Body.String())
	}
	return s, h, cfg
}

func doSearch(h http.Handler) int {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/idx/search", strings.NewReader(`{"query":"alpha","top_k":3}`))
	h.ServeHTTP(w, req)
	return w.Code
}

func TestRace_UpdateConfigDuringSearch(t *testing.T) {
	s, h, cfg := newIndexedServer(t)
	if code := doSearch(h); code != http.StatusOK {
		t.Fatalf("initial search: %d", code)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var ok atomic.Int32
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if doSearch(h) == http.StatusOK {
					ok.Add(1)
				}
			}
		}()
	}
	for i := 0; i < 10; i++ {
		c := cfg
		c.LLMModel = "m" + string(rune('a'+i))
		b, _ := json.Marshal(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/config", bytes.NewReader(b)))
		if w.Code != http.StatusOK {
			t.Fatalf("update config: %d %s", w.Code, w.Body.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if ok.Load() == 0 {
		t.Fatal("no search succeeded")
	}
	if got := s.cfg().LLMModel; got != "mj" {
		t.Errorf("final config not applied: %q", got)
	}
	// Config change must have dropped stale searchers; a new search reloads.
	if code := doSearch(h); code != http.StatusOK {
		t.Fatalf("search after updates: %d", code)
	}
}

func TestRace_DeleteIndexDuringSearch(t *testing.T) {
	s, h, cfg := newIndexedServer(t)
	if code := doSearch(h); code != http.StatusOK {
		t.Fatalf("initial search: %d", code)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				code := doSearch(h)
				if code != http.StatusOK && code != http.StatusNotFound && code != http.StatusInternalServerError {
					t.Errorf("unexpected status %d", code)
				}
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/indexes/idx", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "idx")); !os.IsNotExist(err) {
		t.Errorf("index dir should be gone, stat err = %v", err)
	}
	s.mu.RLock()
	n, refs := len(s.searchers), len(s.searcherRefs)
	s.mu.RUnlock()
	if n != 0 || refs != 0 {
		t.Errorf("searcher cache not drained: searchers=%d refs=%d", n, refs)
	}
}

func TestSearcherRefcount_CloseDeferredUntilRelease(t *testing.T) {
	s, _, _ := newIndexedServer(t)
	ctx := context.Background()

	sr, release, err := s.acquireSearcher(ctx, "idx")
	if err != nil {
		t.Fatal(err)
	}
	s.invalidateSearcher("idx")

	s.mu.RLock()
	ref := s.searcherRefs[sr]
	_, cached := s.searchers["idx"]
	s.mu.RUnlock()
	if cached {
		t.Fatal("invalidated searcher still cached")
	}
	if ref == nil || !ref.retired || ref.refs != 1 {
		t.Fatalf("expected retired ref with 1 borrower, got %+v", ref)
	}
	// Still usable by the borrower.
	if _, err := sr.Search(ctx, "alpha"); err != nil {
		t.Fatalf("search on borrowed searcher after invalidation: %v", err)
	}
	release()
	release() // idempotent
	s.mu.RLock()
	_, still := s.searcherRefs[sr]
	s.mu.RUnlock()
	if still {
		t.Fatal("ref not dropped after release")
	}

	// Next acquire loads a fresh searcher.
	sr2, release2, err := s.acquireSearcher(ctx, "idx")
	if err != nil {
		t.Fatal(err)
	}
	defer release2()
	if sr2 == sr {
		t.Fatal("expected a new searcher after invalidation")
	}
}

func TestAcquireSearcher_LoadDetachedFromRequest(t *testing.T) {
	s, _, _ := newIndexedServer(t)
	// A cancelled request context fails that request but must not poison
	// the cache for others.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.acquireSearcher(ctx, "idx"); err == nil {
		// The load may already have finished; that's fine too.
		t.Log("load completed before cancellation was observed")
	}
	sr, release, err := s.acquireSearcher(context.Background(), "idx")
	if err != nil {
		t.Fatalf("acquire after cancelled request: %v", err)
	}
	release()
	if sr == nil {
		t.Fatal("nil searcher")
	}
}

func TestStop_CancelsAndWaitsForBackground(t *testing.T) {
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}
	ctx := s.lifetime()
	var finished atomic.Bool
	s.goBackground(func() {
		<-ctx.Done()
		time.Sleep(10 * time.Millisecond)
		finished.Store(true)
	})
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !finished.Load() {
		t.Fatal("Stop returned before background goroutine finished")
	}
}
