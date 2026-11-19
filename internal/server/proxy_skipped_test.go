package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In a multi-index pass-through request, an index that cannot be searched
// (here one in the old store format, and one that does not exist) used to be
// dropped silently: the LLM answered without its documents and the client
// could not tell. The passages of the good index still go out, and the reply
// lists the others in X-Gleann-Skipped-Indexes with a short reason.
func TestPassthroughListsSkippedIndexes(t *testing.T) {
	t.Setenv("GLEANN_PROXY_CONTEXT_INTRO", "")
	up := &fakeLLM{status: http.StatusOK, body: "data: {}\n\n"}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	s, _, _ := newBuildServer(t)
	s.config.ProxyMode = "passthrough"
	s.config.LLMProvider = "openai"
	s.config.OpenAIBaseURL = ts.URL + "/v1"
	s.config.LLMModel = "llm"

	buildFormat2DocsIndex(t, s, "good", map[string]string{"a.md": "# A\n\nzebra stripes on the savanna\n"})
	old := filepath.Join(s.config.IndexDir, "legacy")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "legacy.passages.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"llm","messages":[{"role":"user","content":"zebra"}]}`))
	req.Header.Set("X-Gleann-Index", "good,legacy,missing")
	w := httptest.NewRecorder()
	s.handleChatCompletions(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	raw, err := url.QueryUnescape(w.Header().Get("X-Gleann-Skipped-Indexes"))
	if err != nil {
		t.Fatal(err)
	}
	var skipped []proxySkip
	if err := json.Unmarshal([]byte(raw), &skipped); err != nil {
		t.Fatalf("X-Gleann-Skipped-Indexes %q: %v", raw, err)
	}
	got := map[string]string{}
	for _, sk := range skipped {
		got[sk.Index] = sk.Reason
	}
	if len(got) != 2 || !strings.Contains(got["legacy"], "gleann index migrate") || got["missing"] != "not found" {
		t.Errorf("skipped = %+v", skipped)
	}
	if strings.Contains(raw, s.config.IndexDir) {
		t.Errorf("the reasons must not reveal server paths: %s", raw)
	}
	var sources []proxySource
	rawSrc, _ := url.QueryUnescape(w.Header().Get("X-Gleann-Sources"))
	if err := json.Unmarshal([]byte(rawSrc), &sources); err != nil || len(sources) == 0 || sources[0].Index != "good" {
		t.Errorf("sources = %+v (%v)", sources, err)
	}

	// All indexes usable: no header.
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"llm","messages":[{"role":"user","content":"zebra"}]}`))
	req.Header.Set("X-Gleann-Index", "good")
	w = httptest.NewRecorder()
	s.handleChatCompletions(w, req)
	if h := w.Header().Get("X-Gleann-Skipped-Indexes"); w.Code != http.StatusOK || h != "" {
		t.Errorf("status %d, skipped header %q", w.Code, h)
	}
}

func TestSkipReason(t *testing.T) {
	if r := skipReason(os.ErrNotExist, false); r != "not found" {
		t.Errorf("missing: %q", r)
	}
	if r := skipReason(errSearcherBusy, false); !strings.Contains(r, "retry") {
		t.Errorf("busy: %q", r)
	}
	if r := skipReason(os.ErrPermission, true); r != "search failed" {
		t.Errorf("search: %q", r)
	}
	if r := skipReason(os.ErrPermission, false); r != "could not be loaded" {
		t.Errorf("load: %q", r)
	}
}
