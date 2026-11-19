package gleann

import (
	"errors"
	"os"
	"strings"
)

// ErrOpenAIEndpointUnset is returned when the openai provider is used without
// an endpoint. gleann never picks OpenAI's cloud on its own: indexed text and
// questions only leave the machine for api.openai.com when that URL was set
// explicitly (openai_base_url, --openai-base-url or OPENAI_BASE_URL).
var ErrOpenAIEndpointUnset = errors.New("openai endpoint not set: set openai_base_url " +
	"(gleann config set openai_base_url <url>) or OPENAI_BASE_URL to your OpenAI-compatible server; " +
	"use https://api.openai.com/v1 to send data to OpenAI's cloud")

// OpenAICloudURL is OpenAI's own API endpoint. It is only used when a user
// sets it; it is never a default.
const OpenAICloudURL = "https://api.openai.com"

// NormalizeOpenAIBaseURL trims whitespace, trailing slashes and a trailing
// "/v1" from an OpenAI-compatible base URL. Callers append "/v1/...", so both
// "http://host:8000" and "http://host:8000/v1" (the form most servers
// document) reach the same paths.
func NormalizeOpenAIBaseURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	u = strings.TrimSuffix(u, "/v1")
	return strings.TrimRight(u, "/")
}

// ResolveOpenAIBaseURL returns the normalised OpenAI-compatible endpoint:
// configured when non-empty, otherwise OPENAI_BASE_URL. It returns "" when
// neither is set, never OpenAI's cloud; callers must then fail with
// ErrOpenAIEndpointUnset.
func ResolveOpenAIBaseURL(configured string) string {
	if u := NormalizeOpenAIBaseURL(configured); u != "" {
		return u
	}
	return NormalizeOpenAIBaseURL(os.Getenv("OPENAI_BASE_URL"))
}

// IsOpenAICloud reports whether u points at OpenAI's own API.
func IsOpenAICloud(u string) bool {
	u = strings.ToLower(NormalizeOpenAIBaseURL(u))
	return u == OpenAICloudURL || u == "http://api.openai.com"
}

// cleanOllamaHost drops the onboarding placeholder "(auto-scan ...)" that a
// model-directory provider may leave in ollama_host.
func cleanOllamaHost(h string) string {
	if strings.Contains(h, "(auto-scan") {
		return ""
	}
	return strings.TrimSpace(h)
}

// ChatEndpoint returns the base URL and API key the chat client must use for
// provider p. Each provider only ever gets its own endpoint and its own key:
//
//   - ollama: ollama_host (else OLLAMA_HOST, else the local default) and the
//     Ollama key (or OLLAMA_API_KEY);
//   - openai: openai_base_url and the OpenAI key. An empty base URL stays
//     empty (NewChat then tries OPENAI_BASE_URL and otherwise fails); the
//     Ollama host is never used as an OpenAI endpoint;
//   - anthropic: empty, so NewChat applies api.anthropic.com and
//     ANTHROPIC_API_KEY (Config holds no Anthropic key);
//   - other providers (eif, mock, ...): empty.
func (c Config) ChatEndpoint(p LLMProvider) (baseURL, apiKey string) {
	switch p {
	case "", LLMOllama:
		host := cleanOllamaHost(c.OllamaHost)
		if host == "" {
			host = os.Getenv("OLLAMA_HOST")
		}
		if host == "" {
			host = DefaultOllamaHost
		}
		return host, ResolveOllamaAPIKey(c.OllamaAPIKey)
	case LLMOpenAI:
		return NormalizeOpenAIBaseURL(c.OpenAIBaseURL), c.OpenAIAPIKey
	default:
		return "", ""
	}
}

// ApplyChatEndpoint sets cc.BaseURL and cc.APIKey from c for cc.Provider.
// Call it after the provider is final (saved config, flags, request fields),
// so a provider switch never keeps the previous provider's endpoint or key.
func (c Config) ApplyChatEndpoint(cc *ChatConfig) {
	cc.BaseURL, cc.APIKey = c.ChatEndpoint(cc.Provider)
}

// EmbeddingEndpoint returns the base URL and API key for the configured
// embedding provider, under the same rule as ChatEndpoint: the openai
// provider gets openai_base_url (never the Ollama host) and only the OpenAI
// key; gemini, eif and local providers never get the OpenAI key.
func (c Config) EmbeddingEndpoint() (baseURL, apiKey string) {
	switch c.EmbeddingProvider {
	case "", "ollama":
		return cleanOllamaHost(c.OllamaHost), ResolveOllamaAPIKey(c.OllamaAPIKey)
	case "openai":
		return NormalizeOpenAIBaseURL(c.OpenAIBaseURL), c.OpenAIAPIKey
	case "llamacpp":
		// A started embedded server rewrites the provider to openai; a
		// configured llama-server is reached through openai_base_url, else
		// the embedding package's local default.
		u := NormalizeOpenAIBaseURL(c.OpenAIBaseURL)
		if u == "" {
			return "", ""
		}
		return u, c.OpenAIAPIKey
	case "eif", "gemini":
		// eif runs in process; gemini reads GEMINI_API_KEY / GOOGLE_API_KEY.
		return "", ""
	default:
		return cleanOllamaHost(c.OllamaHost), ""
	}
}
