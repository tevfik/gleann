package gleann

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chatCapture is an Ollama-shaped server that records the messages of the
// last request and answers both streaming (NDJSON) and plain requests.
type chatCapture struct {
	*httptest.Server
	last []ChatMessage
}

func newChatCapture(t *testing.T) *chatCapture {
	t.Helper()
	c := &chatCapture{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		c.last = req.Messages
		if req.Stream {
			w.Header().Set("Content-Type", "application/x-ndjson")
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"pong"},"done":true}` + "\n"))
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *chatCapture) systemTexts() []string {
	var out []string
	for _, m := range c.last {
		if m.Role == "system" {
			out = append(out, m.Content)
		}
	}
	return out
}

func fxChat(provider LLMProvider, baseURL string, s Searcher) *LeannChat {
	return NewChat(s, ChatConfig{Provider: provider, Model: "m", BaseURL: baseURL, SystemPrompt: "SYS"})
}

func fxSearcher() *chatMockSearcher {
	return &chatMockSearcher{results: []SearchResult{{Text: "doc one", Score: 1}}}
}

// askVariants runs every RAG entry point against the same chat.
func askVariants(c *LeannChat) map[string]func() error {
	ctx := context.Background()
	cb := func(string) {}
	return map[string]func() error{
		"AskWithResults": func() error { _, _, err := c.AskWithResults(ctx, "q"); return err },
		"AskStream":      func() error { _, err := c.AskStream(ctx, "q", cb); return err },
		"AskWithImages":  func() error { _, err := c.AskWithImages(ctx, "q", []string{"aW1n"}); return err },
		"AskStreamWithMedia": func() error {
			_, err := c.AskStreamWithMedia(ctx, "q", nil, cb)
			return err
		},
		"AskStreamWithImages": func() error {
			_, err := c.AskStreamWithImages(ctx, "q", []string{"aW1n"}, cb)
			return err
		},
	}
}

func TestAskVariantsReportSearchErrors(t *testing.T) {
	c := fxChat(LLMMock, "", &chatMockSearcher{err: os.ErrDeadlineExceeded})
	for name, run := range askVariants(c) {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.HasPrefix(err.Error(), "search:") {
				t.Fatalf("err = %v, want a search: error", err)
			}
		})
	}
	if len(c.History()) != 0 {
		t.Errorf("failed asks must not touch the history: %+v", c.History())
	}
}

func TestAskVariantsReportChatErrors(t *testing.T) {
	c := fxChat("no-such-provider", "", fxSearcher())
	want := map[string]string{
		"AskWithResults":      "chat:",
		"AskStream":           "chat stream:",
		"AskWithImages":       "chat:",
		"AskStreamWithMedia":  "chat stream:",
		"AskStreamWithImages": "chat stream:",
	}
	for name, run := range askVariants(c) {
		t.Run(name, func(t *testing.T) {
			err := run()
			if err == nil || !strings.HasPrefix(err.Error(), want[name]) || !strings.Contains(err.Error(), "unsupported LLM provider") {
				t.Fatalf("err = %v, want prefix %q", err, want[name])
			}
		})
	}
	if len(c.History()) != 0 {
		t.Errorf("failed asks must not touch the history: %+v", c.History())
	}
}

func TestAskInjectsMemoryAndSessionSummary(t *testing.T) {
	srv := newChatCapture(t)
	c := fxChat(LLMOllama, srv.URL, fxSearcher())
	c.SetMemoryContext("MEMORY-CTX")
	c.sessionSummary = "EARLIER-SUMMARY"

	cases := []struct {
		name       string
		run        func() error
		wantMemory bool // media prompts leave the memory block out on purpose
	}{
		{"AskWithResults", func() error { _, _, err := c.AskWithResults(context.Background(), "q"); return err }, true},
		{"AskStream", func() error { _, err := c.AskStream(context.Background(), "q", func(string) {}); return err }, true},
		{"AskWithImages", func() error { _, err := c.AskWithImages(context.Background(), "q", []string{"aW1n"}); return err }, false},
		{"AskStreamWithMedia", func() error {
			_, err := c.AskStreamWithMedia(context.Background(), "q", nil, func(string) {})
			return err
		}, false},
		{"AskStreamWithImages", func() error {
			_, err := c.AskStreamWithImages(context.Background(), "q", []string{"aW1n"}, func(string) {})
			return err
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(srv.systemTexts(), "\n")
			if !strings.Contains(joined, "Summary of earlier conversation: EARLIER-SUMMARY") {
				t.Errorf("session summary missing: %q", joined)
			}
			if got := strings.Contains(joined, "MEMORY-CTX"); got != tc.wantMemory {
				t.Errorf("memory context present = %v, want %v", got, tc.wantMemory)
			}
		})
	}
	// Every successful ask appended its user/assistant pair.
	if h := c.History(); len(h) != 2*len(cases) || h[len(h)-1].Content != "pong" {
		t.Errorf("history = %d messages", len(h))
	}
}

func TestAskStreamWithMediaAudioFallsBackToPlainChat(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "clip.MP3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newChatCapture(t)
	c := fxChat(LLMOllama, srv.URL, fxSearcher())

	var got []string
	results, err := c.AskStreamWithMedia(context.Background(), "q", []string{audio}, func(tok string) { got = append(got, tok) })
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Errorf("audio fallback returns no results, got %+v", results)
	}
	if len(got) != 1 || got[0] != "pong" {
		t.Errorf("callback got %q, want the whole answer once", got)
	}

	// An unreadable audio file fails before any callback.
	called := false
	_, err = c.AskStreamWithMedia(context.Background(), "q", []string{filepath.Join(dir, "missing.wav")}, func(string) { called = true })
	if err == nil || called {
		t.Errorf("err = %v called = %v", err, called)
	}
	// Same for a missing image on the streaming path.
	if _, err = c.AskStreamWithMedia(context.Background(), "q", []string{filepath.Join(dir, "missing.png")}, func(string) {}); err == nil {
		t.Error("missing image must fail")
	}
}

func TestTrimmedHistoryMergesSummaries(t *testing.T) {
	sentence := "The indexing pipeline reads every source file and stores passages in the database for later retrieval. "
	mk := func() []ChatMessage {
		var h []ChatMessage
		for i := 0; i < 15; i++ {
			h = append(h, ChatMessage{Role: "user", Content: sentence}, ChatMessage{Role: "assistant", Content: sentence})
		}
		return h
	}

	t.Run("first trim creates summary", func(t *testing.T) {
		c := fxChat(LLMMock, "", fxSearcher())
		c.history = mk()
		got := c.trimmedHistory()
		if len(got) != 20 || c.sessionSummary == "" {
			t.Errorf("history=%d summary=%q", len(got), c.sessionSummary)
		}
	})
	t.Run("later trim merges old and new summary", func(t *testing.T) {
		c := fxChat(LLMMock, "", fxSearcher())
		c.history = mk()
		c.sessionSummary = "The earlier conversation was about how the vector index is compacted after deletions."
		c.trimmedHistory()
		if !strings.Contains(c.sessionSummary, "indexing pipeline") {
			t.Errorf("merged summary lost the new content: %q", c.sessionSummary)
		}
	})
	t.Run("unsummarisable merge keeps the concatenation", func(t *testing.T) {
		c := fxChat(LLMMock, "", fxSearcher())
		// A single trimmed message yields one summary sentence; glued to an
		// endless "sentence" (> 400 chars, no terminator) the merged text has
		// no usable sentence, so the raw concatenation is kept.
		c.history = append([]ChatMessage{{Role: "user", Content: sentence}}, mk()[:20]...)
		c.sessionSummary = strings.Repeat("Alpha ", 100)
		c.trimmedHistory()
		if !strings.HasPrefix(c.sessionSummary, "Alpha Alpha") || !strings.Contains(c.sessionSummary, "indexing pipeline") {
			t.Errorf("summary = %q", c.sessionSummary)
		}
	})
}

func TestSaveSessionFailsWhenDirIsAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := fxChat(LLMMock, "", fxSearcher())
	c.AppendHistory(ChatMessage{Role: "user", Content: "hi"})
	if _, err := c.SaveSession(filepath.Join(blocker, "sessions"), "idx"); err == nil || !strings.Contains(err.Error(), "mkdir") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetRerankerReachesSearchers(t *testing.T) {
	rr := NewReranker(RerankerConfig{Provider: RerankerJina})

	single := &LeannSearcher{}
	fxChat(LLMMock, "", single).SetReranker(rr)
	if single.reranker != rr {
		t.Error("single searcher did not get the reranker")
	}

	a, b := &LeannSearcher{}, &LeannSearcher{}
	fxChat(LLMMock, "", &MultiSearcher{searchers: map[string]*LeannSearcher{"a": a, "b": b}}).SetReranker(rr)
	if a.reranker != rr || b.reranker != rr {
		t.Error("multi searcher members did not get the reranker")
	}

	// Other Searcher implementations are left alone.
	fxChat(LLMMock, "", fxSearcher()).SetReranker(rr)
}

func TestCompleteWithMockProvider(t *testing.T) {
	c := fxChat(LLMMock, "", fxSearcher())
	out, err := c.Complete(context.Background(), []ChatMessage{{Role: "user", Content: "ping"}})
	if err != nil || !strings.Contains(out, "ping") {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if out, err = c.chatMock(context.Background(), nil); err != nil || !strings.Contains(out, "no messages") {
		t.Errorf("empty chatMock = %q, %v", out, err)
	}
	if len(c.History()) != 0 {
		t.Error("Complete must not record history")
	}
}

// lLMCall invokes one provider entry point, streaming or not.
type lLMCall struct {
	name string
	run  func(c *LeannChat) error
}

func lLMCalls() []lLMCall {
	ctx := context.Background()
	msgs := []ChatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}}
	stream := func(f func(*LeannChat, context.Context, []ChatMessage, StreamCallback) error) func(*LeannChat) error {
		return func(c *LeannChat) error { return f(c, ctx, msgs, func(string) {}) }
	}
	plain := func(f func(*LeannChat, context.Context, []ChatMessage) (string, error)) func(*LeannChat) error {
		return func(c *LeannChat) error { _, err := f(c, ctx, msgs); return err }
	}
	return []lLMCall{
		{"ollama", plain((*LeannChat).chatOllama)},
		{"openai", plain((*LeannChat).chatOpenAI)},
		{"anthropic", plain((*LeannChat).chatAnthropic)},
		{"ollama-stream", stream((*LeannChat).chatOllamaStream)},
		{"openai-stream", stream((*LeannChat).chatOpenAIStream)},
		{"anthropic-stream", stream((*LeannChat).chatAnthropicStream)},
	}
}

func providerOf(name string) LLMProvider {
	switch {
	case strings.HasPrefix(name, "ollama"):
		return LLMOllama
	case strings.HasPrefix(name, "openai"):
		return LLMOpenAI
	default:
		return LLMAnthropic
	}
}

func TestProviderTransportFailures(t *testing.T) {
	// dropper accepts the connection and closes it without answering.
	dropper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	defer dropper.Close()
	// truncated promises a long error body and cuts it short.
	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("partial"))
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}))
	defer truncated.Close()
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{not json"))
	}))
	defer garbage.Close()

	for _, call := range lLMCalls() {
		t.Run(call.name, func(t *testing.T) {
			p := providerOf(call.name)
			check := func(t *testing.T, c *LeannChat, want string) {
				t.Helper()
				if err := call.run(c); err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("err = %v, want it to contain %q", err, want)
				}
			}
			t.Run("marshal failure", func(t *testing.T) {
				c := fxChat(p, "http://127.0.0.1:1", fxSearcher())
				c.SetTemperature(math.NaN()) // NaN is not representable in JSON
				check(t, c, "marshal")
			})
			t.Run("bad url", func(t *testing.T) {
				check(t, fxChat(p, "http://bad\x7fhost", fxSearcher()), "create request")
			})
			t.Run("connection dropped", func(t *testing.T) {
				check(t, fxChat(p, dropper.URL, fxSearcher()), "POST")
			})
			t.Run("unreadable error body", func(t *testing.T) {
				check(t, fxChat(p, truncated.URL, fxSearcher()), "(body unreadable)")
			})
			if !strings.HasSuffix(call.name, "-stream") && p != LLMOllama {
				t.Run("undecodable success body", func(t *testing.T) {
					check(t, fxChat(p, garbage.URL, fxSearcher()), "decode response")
				})
			}
		})
	}
}

func TestStreamsWithImagesUseMultimodalPayloads(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"ok\"}}\n\n" +
			"data: [DONE]\n\n"))
	}))
	defer srv.Close()

	msgs := []ChatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "look", Images: []string{"aW1n"}}}
	for _, p := range []LLMProvider{LLMOpenAI, LLMAnthropic} {
		t.Run(string(p), func(t *testing.T) {
			c := fxChat(p, srv.URL, fxSearcher())
			var got strings.Builder
			if err := c.chatStream(context.Background(), msgs, func(tok string) { got.WriteString(tok) }); err != nil {
				t.Fatal(err)
			}
			if got.String() != "ok" {
				t.Errorf("tokens = %q", got.String())
			}
			// The multimodal payload turns the content into a list of parts.
			raw, _ := json.Marshal(body["messages"])
			if !strings.Contains(string(raw), "aW1n") || !strings.Contains(string(raw), `"content":[`) {
				t.Errorf("image payload missing: %s", raw)
			}
		})
	}
}

func TestAnthropicStreamReportsOversizedEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + strings.Repeat("x", 2<<20) + "\n"))
	}))
	defer srv.Close()
	c := fxChat(LLMAnthropic, srv.URL, fxSearcher())
	err := c.chatAnthropicStream(context.Background(), []ChatMessage{{Role: "user", Content: "u"}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("err = %v, want the scanner's too-long error", err)
	}
}
