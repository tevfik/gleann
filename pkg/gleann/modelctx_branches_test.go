package gleann

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func clearContextEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "")
}

func TestChatContextWindowUnreachableServer(t *testing.T) {
	clearContextEnv(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()

	for _, p := range []LLMProvider{LLMOllama, LLMOpenAI} {
		n, src := ChatContextWindow(context.Background(), ChatConfig{Provider: p, Model: "m", BaseURL: url, MaxTokens: 4096})
		if n != 0 || src != "" {
			t.Errorf("%s: got %d %q from a server that is down, want nothing", p, n, src)
		}
	}
}

func TestChatContextWindowMalformedBaseURL(t *testing.T) {
	clearContextEnv(t)
	// A control character makes every request construction fail; the probes
	// must give up quietly instead of panicking or guessing.
	for _, p := range []LLMProvider{LLMOllama, LLMOpenAI} {
		n, src := ChatContextWindow(context.Background(), ChatConfig{Provider: p, Model: "m", BaseURL: "http://bad\x7fhost", MaxTokens: 4096})
		if n != 0 || src != "" {
			t.Errorf("%s: got %d %q", p, n, src)
		}
	}
}

func TestOllamaContextProbesTolerateBadAnswers(t *testing.T) {
	clearContextEnv(t)
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"show without num_ctx and ps with an unknown model", http.StatusOK, `{"parameters":"temperature 0.7\nnum_ctx abc\nstop x y z","models":[{"name":"other","context_length":9}]}`},
		{"non-JSON answers", http.StatusOK, `<html>`},
		{"error status", http.StatusInternalServerError, `{"error":"boom"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			// MaxTokens 4096 keeps num_ctx out of the request so the Modelfile
			// and loaded-model probes both run.
			n, src := ChatContextWindow(context.Background(), ChatConfig{Provider: LLMOllama, Model: "m", BaseURL: srv.URL, MaxTokens: 4096})
			if n != 0 || src != "" {
				t.Errorf("got %d %q, want the window reported as unknown", n, src)
			}
		})
	}
}

func TestOpenAIModelWindowFallsThroughBadEndpoints(t *testing.T) {
	clearContextEnv(t)
	// /models/<id> answers with an error, the list endpoint has the model.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models/m":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no such model"}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"other","context_length":1},{"id":"m","max_model_len":32768}]}`))
		}
	}))
	defer srv.Close()
	n, src := ChatContextWindow(context.Background(), ChatConfig{Provider: LLMOpenAI, Model: "m", BaseURL: srv.URL})
	if n != 32768 || src != "provider /models" {
		t.Errorf("got %d %q", n, src)
	}

	// A model listed without any window field, and a non-JSON body, give nothing.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/m" {
			_, _ = w.Write([]byte("not json"))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer empty.Close()
	if n, _ := ChatContextWindow(context.Background(), ChatConfig{Provider: LLMOpenAI, Model: "m", BaseURL: empty.URL + "/v1"}); n != 0 {
		t.Errorf("window = %d, want unknown", n)
	}
}
