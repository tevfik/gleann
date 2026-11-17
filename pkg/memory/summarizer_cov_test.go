package memory

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/conversations"
)

// roundTripFunc lets a test answer requests without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func stubResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

// A conversation ID shorter than eight characters (imported or hand-made
// conversations) must not panic the tag construction.
func TestExtractMemories_ShortConversationID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":"fact: Go is the language"}`))
	}))
	defer srv.Close()

	s := NewSummarizer(SummarizerConfig{Provider: "ollama", BaseURL: srv.URL})
	conv := &conversations.Conversation{ID: "abc", Messages: []conversations.Message{{Role: "user", Content: "we use Go"}}}
	blocks, err := s.ExtractMemories(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Tags[1] != "conversation:abc" {
		t.Fatalf("blocks = %+v, want one block tagged conversation:abc", blocks)
	}
}

// Long messages are cut for the extraction prompt, blank and NONE lines in the
// reply are skipped, and an unreachable model surfaces as an error.
func TestExtractMemories_PromptAndReplyHandling(t *testing.T) {
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		prompt = string(b)
		_, _ = w.Write([]byte(`{"response":"decision: use SQLite\n\nNONE\n   \ntodo: write docs"}`))
	}))
	conv := &conversations.Conversation{
		ID: "conv-12345678",
		Messages: []conversations.Message{
			{Role: "user", Content: strings.Repeat("x", 600)},
		},
	}
	s := NewSummarizer(SummarizerConfig{Provider: "ollama", BaseURL: srv.URL})
	blocks, err := s.ExtractMemories(context.Background(), conv)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, strings.Repeat("x", 501)) || !strings.Contains(prompt, "...") {
		t.Errorf("prompt was not truncated to 500 characters")
	}
	if len(blocks) != 2 || blocks[0].Label != "decision" || blocks[1].Label != "todo" {
		t.Fatalf("blocks = %+v, want decision and todo only", blocks)
	}
	srv.Close()

	// Server gone: the connection error is returned, not swallowed.
	if _, err := s.ExtractMemories(context.Background(), conv); err == nil {
		t.Fatal("ExtractMemories against a closed server returned nil error")
	}
}

// Each provider reports an invalid URL, a connection failure and an
// undecodable body, sends its credentials in its own header, and falls back
// to its public endpoint when no base URL is configured.
func TestCallProviders_Failures(t *testing.T) {
	type call func(*Summarizer, context.Context, *http.Client) (string, error)
	providers := []struct {
		name       string
		call       call
		authHeader string
		authValue  string
		defaultURL string
		goodBody   string
		want       string
	}{
		{"ollama", func(s *Summarizer, ctx context.Context, c *http.Client) (string, error) {
			return s.callOllama(ctx, c, "p")
		},
			"Authorization", "Bearer k", "http://localhost:11434/api/generate", `{"response":"hi"}`, "hi"},
		{"openai", func(s *Summarizer, ctx context.Context, c *http.Client) (string, error) {
			return s.callOpenAI(ctx, c, "p")
		},
			"Authorization", "Bearer k", "https://api.openai.com/v1/chat/completions", `{"choices":[{"message":{"content":"hi"}}]}`, "hi"},
		{"anthropic", func(s *Summarizer, ctx context.Context, c *http.Client) (string, error) {
			return s.callAnthropic(ctx, c, "p")
		},
			"x-api-key", "k", "https://api.anthropic.com/v1/messages", `{"content":[{"text":"hi"}]}`, "hi"},
	}
	for _, p := range providers {
		t.Run(p.name, func(t *testing.T) {
			ctx := context.Background()

			// Invalid base URL: the request cannot even be built.
			bad := NewSummarizer(SummarizerConfig{BaseURL: "http://[::1"})
			if _, err := p.call(bad, ctx, &http.Client{}); err == nil {
				t.Error("invalid base URL accepted")
			}

			// Transport failure.
			down := NewSummarizer(SummarizerConfig{BaseURL: "http://127.0.0.1:1"})
			if _, err := p.call(down, ctx, &http.Client{}); err == nil {
				t.Error("connection failure not reported")
			}

			// Garbage body.
			junk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("<html>not json"))
			}))
			defer junk.Close()
			if _, err := p.call(NewSummarizer(SummarizerConfig{BaseURL: junk.URL}), ctx, &http.Client{}); err == nil {
				t.Error("undecodable body accepted")
			}

			// Default endpoint and credentials, observed through the transport.
			var got *http.Request
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				got = r
				return stubResponse(p.goodBody), nil
			})}
			out, err := p.call(NewSummarizer(SummarizerConfig{APIKey: "k"}), ctx, client)
			if err != nil || out != p.want {
				t.Fatalf("call = %q, %v; want %q", out, err, p.want)
			}
			if got.URL.String() != p.defaultURL {
				t.Errorf("default endpoint = %s, want %s", got.URL, p.defaultURL)
			}
			if h := got.Header.Get(p.authHeader); h != p.authValue {
				t.Errorf("%s = %q, want %q", p.authHeader, h, p.authValue)
			}
		})
	}
}
