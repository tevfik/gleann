package gleann

import (
	"io"
	"net/http"
	"os"
)

// OllamaAPIKeyEnv is the environment variable consulted for an Ollama API key
// when none is configured explicitly. Plain local Ollama does not need a key;
// it is only required when Ollama sits behind an authenticating proxy or is a
// hosted, OpenAI-compatible Ollama endpoint.
const OllamaAPIKeyEnv = "OLLAMA_API_KEY"

// ResolveOllamaAPIKey returns key when non-empty, otherwise the value of the
// OLLAMA_API_KEY environment variable (which may also be empty).
func ResolveOllamaAPIKey(key string) string {
	if key != "" {
		return key
	}
	return os.Getenv(OllamaAPIKeyEnv)
}

// APIKeyForProvider returns the API key that should be sent to the given
// LLM or embedding provider: the Ollama key (or OLLAMA_API_KEY) for "ollama"
// and the empty/default provider, and OpenAIAPIKey for everything else.
// This keeps an OpenAI key from ever being sent to an Ollama endpoint.
func (c Config) APIKeyForProvider(provider string) string {
	switch provider {
	case "", string(LLMOllama):
		return ResolveOllamaAPIKey(c.OllamaAPIKey)
	default:
		return c.OpenAIAPIKey
	}
}

// SetBearerAuth sets "Authorization: Bearer <key>" on req when key is non-empty.
func SetBearerAuth(req *http.Request, key string) {
	if req != nil && key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// OllamaGet performs a GET request against an Ollama endpoint, attaching the
// API key as a Bearer token when one is set.
func OllamaGet(client *http.Client, url, apiKey string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	SetBearerAuth(req, apiKey)
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

// OllamaPost performs a POST request against an Ollama endpoint, attaching
// the API key as a Bearer token when one is set. It mirrors http.Client.Post.
func OllamaPost(client *http.Client, url, contentType string, body io.Reader, apiKey string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	SetBearerAuth(req, apiKey)
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

// MaskSecret returns a display-safe representation of a secret: the first
// three characters followed by an ellipsis for longer values, or "********"
// for short ones. Empty input yields an empty string.
func MaskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "********"
	}
	return s[:3] + "…"
}
