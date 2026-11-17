package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestPassthrough_RequestValidation(t *testing.T) {
	up := &fakeLLM{status: http.StatusOK}
	s := newPassthroughServer(t, up)
	tests := []struct {
		name, body string
		want       int
	}{
		{"not json", `{`, 400},
		{"no messages", `{"model":"llm"}`, 400},
		{"empty messages", `{"model":"llm","messages":[]}`, 400},
		{"messages of wrong type", `{"model":"llm","messages":"hi"}`, 400},
		{"invalid index name", `{"model":"gleann/bad name!","messages":[{"role":"user","content":"hi"}]}`, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.passthroughChatCompletions(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tc.body)))
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if len(up.paths) != 0 {
		t.Errorf("invalid requests reached the upstream: %v", up.paths)
	}
}

func TestPassthrough_UpstreamUnreachableIs502(t *testing.T) {
	s := newProxyTestServer(t)
	s.config.ProxyMode = "passthrough"
	s.config.LLMProvider = "openai"
	dead := httptest.NewServer(http.NotFoundHandler())
	s.config.OpenAIBaseURL = dead.URL
	dead.Close()

	w := httptest.NewRecorder()
	s.passthroughChatCompletions(w, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"llm","messages":[{"role":"user","content":"hi"}]}`)))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "unreachable") {
		t.Errorf("got %d %s", w.Code, w.Body.String())
	}
}

func TestForwardPassthrough_SendsAcceptAndAuthorization(t *testing.T) {
	var gotAccept, gotAuth, gotCT string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept, gotAuth, gotCT = r.Header.Get("Accept"), r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brewing"))
	}))
	defer ts.Close()
	s := newProxyTestServer(t)

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()
	s.forwardPassthrough(w, r, ts.URL+"/x", []byte(`{}`), "sekret")
	if gotAccept != "text/event-stream" || gotAuth != "Bearer sekret" || gotCT != "application/json" {
		t.Errorf("upstream headers: accept=%q auth=%q ct=%q", gotAccept, gotAuth, gotCT)
	}
	if w.Code != http.StatusTeapot || w.Body.String() != "brewing" || w.Header().Get("Cache-Control") != "no-cache" ||
		w.Header().Get("X-Accel-Buffering") != "no" {
		t.Errorf("response = %d %q %v", w.Code, w.Body.String(), w.Header())
	}

	// A body-less forward (POST /stop) sets no content type and no auth header without a key.
	gotCT, gotAuth = "unset", "unset"
	s.forwardPassthrough(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/stop", nil), ts.URL+"/stop", nil, "")
	if gotCT != "" || gotAuth != "" {
		t.Errorf("body-less forward: ct=%q auth=%q", gotCT, gotAuth)
	}

	// An invalid target URL is a 500, not a panic.
	w = httptest.NewRecorder()
	s.forwardPassthrough(w, httptest.NewRequest(http.MethodPost, "/x", nil), "http://bad host/", nil, "")
	if w.Code != http.StatusInternalServerError {
		t.Errorf("invalid target: %d", w.Code)
	}
}

// Every retrieval header is honoured on the way to the upstream: the request
// succeeds, the context reaches the LLM and the sources are reported.
func TestPassthrough_RetrievalHeaders(t *testing.T) {
	t.Setenv("GLEANN_PROXY_CONTEXT_INTRO", "")
	up := &fakeLLM{status: http.StatusOK, body: "data: {}\n\n"}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	s, _, _ := newBuildServer(t)
	s.config.ProxyMode = "passthrough"
	s.config.LLMProvider = "openai"
	s.config.OpenAIBaseURL = ts.URL + "/v1"
	s.config.LLMModel = "upstream-model"
	s.config.SearchConfig.TopK = 2
	// A reranker that cannot be reached must degrade to the unreranked order.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	s.config.SearchConfig.RerankerConfig.Provider = gleann.RerankerOllama
	s.config.SearchConfig.RerankerConfig.BaseURL = deadURL
	buildFormat2DocsIndex(t, s, "handbook", map[string]string{
		"zebra.md": "# Zebra\n\nThe zebra has black and white stripes.\n",
		"lion.md":  "# Lion\n\nThe lion has a mane and lives in prides.\n",
	})

	tests := []struct {
		name string
		hdr  map[string]string
	}{
		{"defaults use config top_k", nil},
		{"explicit top-k", map[string]string{"X-Gleann-Top-K": "1"}},
		{"invalid top-k ignored", map[string]string{"X-Gleann-Top-K": "many"}},
		{"min score", map[string]string{"X-Gleann-Min-Score": "0"}},
		{"unparsable min score ignored", map[string]string{"X-Gleann-Min-Score": "high"}},
		{"index header replaces model index", map[string]string{"X-Gleann-Index": "handbook"}},
		{"context cap", map[string]string{"X-Gleann-Max-Context-Chars": "400"}},
		{"context as user message", map[string]string{"X-Gleann-Context": "USER"}},
		{"rerank configured reranker with pool", map[string]string{"X-Gleann-Rerank": "true", "X-Gleann-Rerank-Pool": "5"}},
		{"rerank with a named model", map[string]string{"X-Gleann-Rerank": "some-reranker"}},
		{"rerank off spellings", map[string]string{"X-Gleann-Rerank": "false"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			up.bodies = nil
			model := "gleann/handbook"
			if tc.hdr["X-Gleann-Index"] != "" {
				model = "gleann/ignored-because-header-wins"
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"zebra stripes?"}]}`))
			for k, v := range tc.hdr {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			s.handleChatCompletions(w, req)
			if w.Code != http.StatusOK || len(up.bodies) != 1 {
				t.Fatalf("status %d, upstream calls %d: %s", w.Code, len(up.bodies), w.Body.String())
			}
			var sent map[string]any
			if err := json.Unmarshal(up.bodies[0], &sent); err != nil {
				t.Fatal(err)
			}
			if sent["model"] != "upstream-model" {
				t.Errorf("model = %v, want the configured upstream model", sent["model"])
			}
			if !strings.Contains(string(up.bodies[0]), "Context:") {
				t.Errorf("no retrieved context in %s", up.bodies[0])
			}
			raw, err := url.QueryUnescape(w.Header().Get("X-Gleann-Sources"))
			if err != nil || !strings.Contains(raw, "zebra.md") {
				t.Errorf("X-Gleann-Sources = %q (%v)", raw, err)
			}
		})
	}
}
