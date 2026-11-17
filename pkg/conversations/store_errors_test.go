package conversations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without GLEANN_CONVERSATIONS_DIR the store lives under $HOME/.gleann.
func TestDefaultStore_HomeFallbackAndEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_CONVERSATIONS_DIR", "")
	if got, want := DefaultStore().dir, filepath.Join(home, ".gleann", "conversations"); got != want {
		t.Fatalf("dir = %q, want %q", got, want)
	}
	custom := filepath.Join(home, "elsewhere")
	t.Setenv("GLEANN_CONVERSATIONS_DIR", custom)
	if got := DefaultStore().dir; got != custom {
		t.Fatalf("dir = %q, want %q", got, custom)
	}
}

// A store whose directory path is taken by a regular file cannot save, list
// or find anything, and says so instead of silently returning nothing.
func TestStore_DirIsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "conversations")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore(f)
	if err := s.Save(&Conversation{Title: "t"}); err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("Save = %v, want mkdir error", err)
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List succeeded on a file")
	}
	if _, err := s.Load("abc"); err == nil {
		t.Fatal("Load succeeded on a file")
	}
	if _, err := s.Latest(); err == nil {
		t.Fatal("Latest succeeded on a file")
	}
	if _, err := s.DeleteOlderThan(0); err == nil {
		t.Fatal("DeleteOlderThan succeeded on a file")
	}
}

// A damaged conversation file is skipped by List and reported by a direct
// load; the healthy conversations stay reachable.
func TestStore_CorruptFileIsSkipped(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	good := &Conversation{Title: "good one", Messages: []Message{{Role: "user", Content: "hi"}}}
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory named like a conversation cannot be read: skipped too.
	if err := os.Mkdir(filepath.Join(dir, "adir.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	convs, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(convs) != 1 || convs[0].ID != good.ID {
		t.Fatalf("List = %+v, want only the good conversation", convs)
	}
	if _, err := s.loadByID("broken"); err == nil {
		t.Fatal("loading a corrupt file succeeded")
	}
	// Load("broken") falls back to prefix/title matching, which finds nothing.
	if _, err := s.Load("broken"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Load(broken) = %v, want not found", err)
	}
	if err := s.Delete("no-such-conversation"); err == nil {
		t.Fatal("Delete of an unknown conversation succeeded")
	}
	latest, err := s.Latest()
	if err != nil || latest == nil || latest.ID != good.ID {
		t.Fatalf("Latest = %+v, %v", latest, err)
	}
}

// Only the first four non-system messages go into the prompt, each cut to
// 200 bytes; an unknown provider is served by the Ollama API and an API key
// is sent as a bearer token.
func TestSummarizer_PromptShapeAndUnknownProvider(t *testing.T) {
	var prompt, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path %s, want the Ollama endpoint", r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt, _ = body["prompt"].(string)
		_ = json.NewEncoder(w).Encode(map[string]string{"response": "  \"Short Title.\"  "})
	}))
	defer srv.Close()

	long := strings.Repeat("x", 300)
	conv := &Conversation{Messages: []Message{
		{Role: "system", Content: "SYSTEM-PROMPT"},
		{Role: "user", Content: long},
		{Role: "assistant", Content: "m2"},
		{Role: "user", Content: "m3"},
		{Role: "assistant", Content: "m4"},
		{Role: "user", Content: "FIFTH"},
	}}
	s := &Summarizer{Provider: "mystery", Model: "m", BaseURL: srv.URL, APIKey: "k"}
	if got := s.SummarizeTitle(context.Background(), conv); got != "Short Title" {
		t.Fatalf("title = %q", got)
	}
	if auth != "Bearer k" {
		t.Errorf("Authorization = %q", auth)
	}
	if strings.Contains(prompt, "SYSTEM-PROMPT") || strings.Contains(prompt, "FIFTH") {
		t.Errorf("prompt includes system or fifth message: %q", prompt)
	}
	if !strings.Contains(prompt, strings.Repeat("x", 200)+"...") || strings.Contains(prompt, strings.Repeat("x", 201)) {
		t.Errorf("long message not cut to 200 bytes")
	}
}

// Malformed or empty LLM replies fall back to the first user message.
func TestSummarizer_BadRepliesFallBack(t *testing.T) {
	conv := &Conversation{Messages: []Message{{Role: "user", Content: "fallback title"}}}
	cases := []struct {
		name, provider, reply string
	}{
		{"ollama bad json", "ollama", "{"},
		{"openai bad json", "openai", "{"},
		{"openai no choices", "openai", `{"choices":[]}`},
		{"openai blank content", "openai", `{"choices":[{"message":{"content":"   "}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var auth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				auth = r.Header.Get("Authorization")
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer srv.Close()
			s := &Summarizer{Provider: tc.provider, BaseURL: srv.URL}
			if got := s.SummarizeTitle(context.Background(), conv); got != "fallback title" {
				t.Fatalf("title = %q, want the fallback", got)
			}
			if auth != "" {
				t.Errorf("Authorization sent without an API key: %q", auth)
			}
		})
	}
}

// An unreachable or invalid endpoint falls back too (no network: the URLs
// are rejected before any connection).
func TestSummarizer_RequestErrorsFallBack(t *testing.T) {
	conv := &Conversation{Messages: []Message{{Role: "user", Content: "fb"}}}
	for _, p := range []string{"ollama", "openai"} {
		s := &Summarizer{Provider: p, BaseURL: "http://bad host\x7f"}
		if got := s.SummarizeTitle(context.Background(), conv); got != "fb" {
			t.Errorf("%s invalid URL: title %q", p, got)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		s = &Summarizer{Provider: p, BaseURL: "http://127.0.0.1:1"}
		if got := s.SummarizeTitle(ctx, conv); got != "fb" {
			t.Errorf("%s cancelled: title %q", p, got)
		}
	}
}
