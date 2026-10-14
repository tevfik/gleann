package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/tui"
	"github.com/tevfik/gleann/pkg/gleann"
)

// isolateHome points HOME at a fresh temp dir and clears env vars that would
// otherwise leak host configuration into config resolution.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, k := range []string{"OLLAMA_HOST", "OPENAI_BASE_URL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(k, "")
	}
	return home
}

func TestBuildChatConfig_ProviderEndpointsAndKeys(t *testing.T) {
	tests := []struct {
		name     string
		cfg      gleann.Config
		saved    *tui.OnboardResult
		args     []string
		provider gleann.LLMProvider
		baseURL  string // value in ChatConfig before NewChat defaults
		apiKey   string
	}{
		{
			name:     "ollama uses configured host",
			cfg:      gleann.Config{OllamaHost: "http://gpu:11435"},
			provider: gleann.LLMOllama, baseURL: "http://gpu:11435",
		},
		{
			name:     "ollama default host",
			provider: gleann.LLMOllama, baseURL: gleann.DefaultOllamaHost,
		},
		{
			// Regression: the Ollama host was used as the OpenAI endpoint.
			name:     "openai without base url is not sent to ollama",
			cfg:      gleann.Config{LLMProvider: "openai", OllamaHost: "http://localhost:11434", OpenAIAPIKey: "sk-1"},
			provider: gleann.LLMOpenAI, baseURL: "", apiKey: "sk-1",
		},
		{
			name:     "openai custom base url",
			cfg:      gleann.Config{LLMProvider: "openai", OpenAIBaseURL: "https://llm.example/v1", OllamaHost: "http://localhost:11434"},
			provider: gleann.LLMOpenAI, baseURL: "https://llm.example/v1",
		},
		{
			// Regression: the provider flag was applied after the base URL was
			// chosen, so the configured OpenAI base URL was ignored.
			name:     "llm-provider flag picks configured openai base url",
			cfg:      gleann.Config{LLMProvider: "ollama", OllamaHost: "http://localhost:11434", OpenAIBaseURL: "https://llm.example/v1"},
			args:     []string{"--llm-provider", "openai"},
			provider: gleann.LLMOpenAI, baseURL: "https://llm.example/v1",
		},
		{
			// Regression: anthropic got the Ollama host and the OpenAI key.
			name:     "anthropic uses saved anthropic key and default endpoint",
			cfg:      gleann.Config{LLMProvider: "anthropic", OllamaHost: "http://localhost:11434", OpenAIAPIKey: "sk-openai"},
			saved:    &tui.OnboardResult{AnthropicKey: "sk-ant"},
			provider: gleann.LLMAnthropic, baseURL: "", apiKey: "sk-ant",
		},
		{
			name:     "explicit flags override everything",
			cfg:      gleann.Config{LLMProvider: "anthropic"},
			saved:    &tui.OnboardResult{AnthropicKey: "sk-ant"},
			args:     []string{"--api-key", "k", "--base-url", "http://proxy"},
			provider: gleann.LLMAnthropic, baseURL: "http://proxy", apiKey: "k",
		},
		{
			name:     "ollama optional bearer key",
			cfg:      gleann.Config{OllamaAPIKey: "tok"},
			provider: gleann.LLMOllama, baseURL: gleann.DefaultOllamaHost, apiKey: "tok",
		},
		{
			name:     "openai key is never sent to ollama",
			cfg:      gleann.Config{OpenAIAPIKey: "sk-openai"},
			provider: gleann.LLMOllama, baseURL: gleann.DefaultOllamaHost, apiKey: "",
		},
		{
			name:     "openai-base-url flag",
			cfg:      gleann.Config{LLMProvider: "openai"},
			args:     []string{"--openai-base-url", "http://local:8080"},
			provider: gleann.LLMOpenAI, baseURL: "http://local:8080",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateHome(t)
			t.Setenv(gleann.OllamaAPIKeyEnv, "")
			if tt.saved != nil {
				if err := tui.SaveConfig(*tt.saved); err != nil {
					t.Fatal(err)
				}
			}
			cc := buildChatConfig(tt.args, tt.cfg)
			if cc.Provider != tt.provider || cc.BaseURL != tt.baseURL || cc.APIKey != tt.apiKey {
				t.Fatalf("got provider=%q base=%q key=%q, want %q %q %q",
					cc.Provider, cc.BaseURL, cc.APIKey, tt.provider, tt.baseURL, tt.apiKey)
			}
		})
	}
}

func TestBuildChatConfig_EffectiveEndpointAfterNewChat(t *testing.T) {
	isolateHome(t)
	cc := buildChatConfig(nil, gleann.Config{LLMProvider: "openai", OllamaHost: "http://localhost:11434"})
	chat := gleann.NewChat(nil, cc)
	if got := chat.Config().BaseURL; got != "https://api.openai.com" {
		t.Fatalf("effective OpenAI endpoint = %q", got)
	}
	cc = buildChatConfig(nil, gleann.Config{LLMProvider: "anthropic", OllamaHost: "http://localhost:11434"})
	if got := gleann.NewChat(nil, cc).Config().BaseURL; got != "https://api.anthropic.com" {
		t.Fatalf("effective Anthropic endpoint = %q", got)
	}
}

func TestBuildChatConfig_RepeatPenaltyFlags(t *testing.T) {
	tests := []struct {
		args []string
		want float64
		set  bool
	}{
		{[]string{"--repeat-penalty", "1.3"}, 1.3, true},
		{[]string{"--repetition-penalty", "1.1"}, 1.1, true},
		{[]string{"--repeat-penalty", "1.2", "--repetition-penalty", "1.9"}, 1.2, true},
		{[]string{"--repeat-penalty", "0"}, 0, true},
		{[]string{"--repeat-penalty", "-1"}, 0, false},
		{[]string{"--repeat-penalty", "abc"}, 0, false},
	}
	for _, tt := range tests {
		isolateHome(t)
		def := buildChatConfig(nil, gleann.Config{}).RepeatPenalty
		got := buildChatConfig(tt.args, gleann.Config{}).RepeatPenalty
		want := def
		if tt.set {
			want = tt.want
		}
		if got != want {
			t.Errorf("%v: RepeatPenalty = %v, want %v", tt.args, got, want)
		}
	}
}

func TestCmdConfigSet_Aliases(t *testing.T) {
	tests := []struct {
		key, val string
		check    func(*tui.OnboardResult) string
		want     string
	}{
		{"port", "11435", func(c *tui.OnboardResult) string { return c.OllamaHost }, "http://localhost:11435"},
		{"ollama_host", "gpu:11434", func(c *tui.OnboardResult) string { return c.OllamaHost }, "http://gpu:11434"},
		{"host", "https://ollama.example", func(c *tui.OnboardResult) string { return c.OllamaHost }, "https://ollama.example"},
		{"CODE-MODEL", "jina", func(c *tui.OnboardResult) string { return c.CodeEmbeddingModel }, "jina"},
		{"docs_model", "bge", func(c *tui.OnboardResult) string { return c.DocEmbeddingModel }, "bge"},
		{"api-key", "sk-x", func(c *tui.OnboardResult) string { return c.OpenAIKey }, "sk-x"},
		{"base_url", "http://b", func(c *tui.OnboardResult) string { return c.OpenAIBaseURL }, "http://b"},
		{"anthropic_key", "sk-a", func(c *tui.OnboardResult) string { return c.AnthropicKey }, "sk-a"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			home := isolateHome(t)
			// Pre-existing settings must survive an unrelated `config set`.
			if err := tui.SaveConfig(tui.OnboardResult{LLMModel: "keep-me"}); err != nil {
				t.Fatal(err)
			}
			cmdConfigSet([]string{tt.key, tt.val})
			if _, err := os.Stat(filepath.Join(home, ".gleann", "config.json")); err != nil {
				t.Fatalf("config not written: %v", err)
			}
			cfg := tui.LoadSavedConfig()
			if cfg == nil {
				t.Fatal("config not loadable")
			}
			if got := tt.check(cfg); got != tt.want {
				t.Fatalf("%s=%s stored %q, want %q", tt.key, tt.val, got, tt.want)
			}
			if cfg.LLMModel != "keep-me" || !cfg.Completed {
				t.Fatalf("existing fields lost or Completed unset: %+v", cfg)
			}
		})
	}
}
