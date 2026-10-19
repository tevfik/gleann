package gleann

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// ChatContextWindow returns the number of tokens the configured chat model can
// actually be given, and where the number came from. Nothing is guessed from
// the model name; (0, "") means the provider did not say.
//
// Order: GLEANN_CONTEXT_TOKENS, then for Ollama the num_ctx this chat sends
// (GLEANN_OLLAMA_NUM_CTX / --no-limit), the num_ctx in the model's Modelfile
// and the context length of the loaded model (/api/ps); for OpenAI-compatible
// servers the window fields of /v1/models.
func ChatContextWindow(ctx context.Context, cfg ChatConfig) (int, string) {
	ollamaKey := cfg.APIKey
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("GLEANN_CONTEXT_TOKENS"))); err == nil && v > 0 {
		return v, "GLEANN_CONTEXT_TOKENS"
	}
	if cfg.Model == "" || cfg.BaseURL == "" {
		return 0, ""
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	base := strings.TrimRight(cfg.BaseURL, "/")
	client := &http.Client{Timeout: 4 * time.Second}

	switch cfg.Provider {
	case LLMOllama, "":
		if n := ollamaContextWindow(cfg.MaxTokens); n > 0 {
			if trained := ollamaShowInt(ctx, client, base, cfg.Model, ollamaKey, ".context_length"); trained > 0 && trained < n {
				return trained, "model limit (/api/show)"
			}
			return n, "num_ctx sent with each request"
		}
		if n := ollamaModelfileNumCtx(ctx, client, base, cfg.Model, ollamaKey); n > 0 {
			return n, "Modelfile num_ctx"
		}
		if n := ollamaLoadedContext(ctx, client, base, cfg.Model, ollamaKey); n > 0 {
			return n, "loaded model (/api/ps)"
		}
	case LLMOpenAI:
		if n := openAIModelWindow(ctx, client, base, cfg.Model, cfg.APIKey); n > 0 {
			return n, "provider /models"
		}
	}
	return 0, ""
}

func ollamaPostJSON(ctx context.Context, client *http.Client, url, key string, body any, out any) bool {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	SetBearerAuth(req, ResolveOllamaAPIKey(key))
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(out) == nil
}

// ollamaShowInt returns the first model_info entry whose key ends in suffix.
func ollamaShowInt(ctx context.Context, client *http.Client, base, model, key, suffix string) int {
	var out struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if !ollamaPostJSON(ctx, client, base+"/api/show", key, map[string]string{"model": model}, &out) {
		return 0
	}
	for k, v := range out.ModelInfo {
		if f, ok := v.(float64); ok && strings.HasSuffix(k, suffix) {
			return int(f)
		}
	}
	return 0
}

// ollamaModelfileNumCtx reads "PARAMETER num_ctx N" from the model's settings.
func ollamaModelfileNumCtx(ctx context.Context, client *http.Client, base, model, key string) int {
	var out struct {
		Parameters string `json:"parameters"`
	}
	if !ollamaPostJSON(ctx, client, base+"/api/show", key, map[string]string{"model": model}, &out) {
		return 0
	}
	for _, line := range strings.Split(out.Parameters, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "num_ctx" {
			if n, err := strconv.Atoi(f[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// ollamaLoadedContext reads the context length of the model if it is loaded.
func ollamaLoadedContext(ctx context.Context, client *http.Client, base, model, key string) int {
	resp, err := OllamaGet(client, base+"/api/ps", ResolveOllamaAPIKey(key))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var out struct {
		Models []struct {
			Name          string `json:"name"`
			Model         string `json:"model"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&out) != nil {
		return 0
	}
	for _, m := range out.Models {
		if m.Name == model || m.Model == model {
			return m.ContextLength
		}
	}
	return 0
}

func openAIModelWindow(ctx context.Context, client *http.Client, base, model, apiKey string) int {
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	pick := func(m map[string]any) int {
		for _, k := range []string{"context_length", "context_window", "max_model_len", "max_context_length", "max_input_tokens", "n_ctx"} {
			if f, ok := m[k].(float64); ok && f > 0 {
				return int(f)
			}
		}
		return 0
	}
	for _, u := range []string{base + "/models/" + url.PathEscape(model), base + "/models"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		SetBearerAuth(req, apiKey)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		if list, ok := body["data"].([]any); ok {
			for _, e := range list {
				if m, ok := e.(map[string]any); ok && m["id"] == model {
					if n := pick(m); n > 0 {
						return n
					}
				}
			}
			continue
		}
		if n := pick(body); n > 0 {
			return n
		}
	}
	return 0
}
