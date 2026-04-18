package main

import (
	"testing"

	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── valueOrDefault ─────────────────────────────────────────────

func TestValueOrDefault(t *testing.T) {
	tests := []struct {
		s, def, want string
	}{
		{"hello", "default", "hello"},
		{"", "default", "default"},
		{"", "", ""},
		{"value", "", "value"},
	}
	for _, tt := range tests {
		if got := valueOrDefault(tt.s, tt.def); got != tt.want {
			t.Errorf("valueOrDefault(%q, %q) = %q, want %q", tt.s, tt.def, got, tt.want)
		}
	}
}

// ── configFilePath ─────────────────────────────────────────────

func TestConfigFilePath(t *testing.T) {
	p := configFilePath()
	if p == "" {
		t.Error("expected non-empty path")
	}
	// Should end with config.json.
	if len(p) < 11 || p[len(p)-11:] != "config.json" {
		t.Errorf("path = %q, expected to end with config.json", p)
	}
}

// ── autoTitleFallback ──────────────────────────────────────────

func TestAutoTitleFallback(t *testing.T) {
	conv := &conversations.Conversation{
		Messages: []conversations.Message{
			{Role: "system", Content: "You are helpful"},
			{Role: "user", Content: "What is Go?"},
			{Role: "assistant", Content: "Go is a language"},
		},
	}
	title := autoTitleFallback(conv)
	if title != "What is Go?" {
		t.Errorf("title = %q", title)
	}
}

func TestAutoTitleFallbackLong(t *testing.T) {
	longMsg := "This is a very long question that exceeds sixty characters and should be truncated"
	conv := &conversations.Conversation{
		Messages: []conversations.Message{
			{Role: "user", Content: longMsg},
		},
	}
	title := autoTitleFallback(conv)
	if len(title) > 60 {
		t.Errorf("title too long: %d chars", len(title))
	}
	if title[len(title)-3:] != "..." {
		t.Error("truncated title should end with ...")
	}
}

func TestAutoTitleFallbackNoUser(t *testing.T) {
	conv := &conversations.Conversation{
		Messages: []conversations.Message{
			{Role: "system", Content: "prompt"},
		},
	}
	title := autoTitleFallback(conv)
	if title != "untitled" {
		t.Errorf("title = %q, want untitled", title)
	}
}

func TestAutoTitleFallbackEmpty(t *testing.T) {
	conv := &conversations.Conversation{}
	title := autoTitleFallback(conv)
	if title != "untitled" {
		t.Errorf("title = %q, want untitled", title)
	}
}

// ── buildSummarizer ────────────────────────────────────────────

func TestBuildSummarizer(t *testing.T) {
	chatCfg := gleann.ChatConfig{
		Provider: gleann.LLMOllama,
		Model:    "llama3.2",
		BaseURL:  "http://localhost:11434",
	}
	config := gleann.DefaultConfig()
	s := buildSummarizer(chatCfg, config)
	if s == nil {
		t.Fatal("expected non-nil summarizer")
	}
	if s.Model != "llama3.2" {
		t.Errorf("Model = %q", s.Model)
	}
	if s.Provider != "ollama" {
		t.Errorf("Provider = %q", s.Provider)
	}
}

func TestBuildSummarizerNoModel(t *testing.T) {
	chatCfg := gleann.ChatConfig{Provider: gleann.LLMOllama}
	config := gleann.DefaultConfig()
	s := buildSummarizer(chatCfg, config)
	if s != nil {
		t.Error("should return nil without model")
	}
}
