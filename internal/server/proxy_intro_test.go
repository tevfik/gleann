package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPassthroughIntro(t *testing.T) {
	const rule = "Answer in the language of the user's question.\n\nContext:\n"
	cases := []struct {
		name, env, cfg string
		wantPrefix     string
	}{
		{"default", "", "", defaultPassthroughIntro},
		{"config", "", "Read these notes first.", "Read these notes first."},
		{"env wins over config", "From the env.", "From the config.", "From the env."},
		{"blank values fall back", "  ", "\n", defaultPassthroughIntro},
		{"whitespace trimmed", "", "  Padded intro.  \n", "Padded intro."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GLEANN_PROXY_CONTEXT_INTRO", c.env)
			s := newProxyTestServer(t)
			s.config.ProxyContextIntro = c.cfg
			got := s.passthroughIntro()
			if want := c.wantPrefix + " " + rule; got != want {
				t.Errorf("intro = %q, want %q", got, want)
			}
		})
	}
}

// The text the LLM server receives carries the configured intro, ends it with the language rule, and puts the
// passages after "Context:".
func TestPassthroughSendsIntroWithLanguageRule(t *testing.T) {
	t.Setenv("GLEANN_PROXY_CONTEXT_INTRO", "")
	up := &fakeLLM{status: http.StatusOK, body: "data: {}\n\n"}
	ts := httptest.NewServer(up)
	t.Cleanup(ts.Close)
	s, _, _ := newBuildServer(t)
	s.config.ProxyMode = "passthrough"
	s.config.LLMProvider = "openai"
	s.config.OpenAIBaseURL = ts.URL + "/v1"
	s.config.LLMModel = "llm"
	s.config.ProxyContextIntro = "These passages come from the handbook."
	buildFormat2DocsIndex(t, s, "handbook", map[string]string{
		"zebra.md": "# Zebra\n\nThe zebra has black and white stripes.\n",
	})

	for _, hdr := range []map[string]string{nil, {"X-Gleann-Context": "user"}} {
		up.bodies = nil
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"gleann/handbook","messages":[{"role":"user","content":"zebra stripes?"}]}`))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.handleChatCompletions(w, req)
		if w.Code != http.StatusOK || len(up.bodies) != 1 {
			t.Fatalf("status %d, upstream bodies %d", w.Code, len(up.bodies))
		}
		body := string(up.bodies[0])
		want := `These passages come from the handbook. Answer in the language of the user's question.\n\nContext:\n[1]`
		if !strings.Contains(body, want) {
			t.Errorf("headers %v: upstream body lacks the intro:\n%s", hdr, body)
		}
		if strings.Contains(body, "Use the following context") {
			t.Errorf("default intro leaked despite the configured one: %s", body)
		}
	}
}
