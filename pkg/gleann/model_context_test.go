package gleann

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatContextWindow_Ollama(t *testing.T) {
	loaded := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			json.NewEncoder(w).Encode(map[string]any{
				"model_info": map[string]any{"arch.context_length": 4096.0},
				"parameters": "temperature 0.7\nnum_ctx                        6000",
			})
		case "/api/ps":
			json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"name": "m", "context_length": loaded}}})
		}
	}))
	defer srv.Close()
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "")
	cfg := ChatConfig{Provider: LLMOllama, Model: "m", BaseURL: srv.URL, MaxTokens: 4096}

	// Budgeted mode sends no num_ctx: the Modelfile value is what Ollama uses.
	if n, src := ChatContextWindow(context.Background(), cfg); n != 6000 || src != "Modelfile num_ctx" {
		t.Errorf("modelfile: %d %s", n, src)
	}
	// --no-limit sends a num_ctx, capped by what the model supports.
	cfg.MaxTokens = 0
	if n, src := ChatContextWindow(context.Background(), cfg); n != 4096 || src != "model limit (/api/show)" {
		t.Errorf("capped: %d %s", n, src)
	}
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "2048")
	if n, _ := ChatContextWindow(context.Background(), cfg); n != 2048 {
		t.Errorf("explicit num_ctx: %d", n)
	}
	t.Setenv("GLEANN_CONTEXT_TOKENS", "999")
	if n, src := ChatContextWindow(context.Background(), cfg); n != 999 || src != "GLEANN_CONTEXT_TOKENS" {
		t.Errorf("override: %d %s", n, src)
	}
}

func TestChatContextWindow_LoadedModelAndUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			json.NewEncoder(w).Encode(map[string]any{"parameters": ""})
		case "/api/ps":
			json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"name": "m", "context_length": 8192}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "x"}}})
		}
	}))
	defer srv.Close()
	t.Setenv("GLEANN_CONTEXT_TOKENS", "")
	t.Setenv("GLEANN_OLLAMA_NUM_CTX", "")
	cfg := ChatConfig{Provider: LLMOllama, Model: "m", BaseURL: srv.URL, MaxTokens: 100}
	if n, src := ChatContextWindow(context.Background(), cfg); n != 8192 || src != "loaded model (/api/ps)" {
		t.Errorf("%d %s", n, src)
	}
	cfg.Model = "other"
	if n, _ := ChatContextWindow(context.Background(), cfg); n != 0 {
		t.Errorf("a model that is not loaded and has no Modelfile value is unknown, not guessed: %d", n)
	}
	cfg.Provider = LLMOpenAI
	cfg.Model = "x"
	if n, _ := ChatContextWindow(context.Background(), cfg); n != 0 {
		t.Errorf("OpenAI model without a window field: %d", n)
	}
}
