package gleann

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// streamServer answers every POST with the given content type and body, and
// records the decoded request bodies and headers.
type streamServer struct {
	mu       sync.Mutex
	requests []map[string]any
	headers  []http.Header
	srv      *httptest.Server
}

func newStreamServer(t *testing.T, status int, contentType, body string) *streamServer {
	t.Helper()
	s := &streamServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(raw, &req)
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.headers = append(s.headers, r.Header.Clone())
		s.mu.Unlock()
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *streamServer) last() (map[string]any, http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[len(s.requests)-1], s.headers[len(s.headers)-1]
}

func collectStream(t *testing.T, c *LeannChat) (string, error) {
	t.Helper()
	var sb strings.Builder
	err := c.chatStream(context.Background(), []ChatMessage{
		{Role: "system", Content: "be brief"},
		{Role: "user", Content: "hi"},
	}, func(tok string) { sb.WriteString(tok) })
	return sb.String(), err
}

// A proxy (or a server with streaming disabled) may answer a streaming
// request with one plain JSON object; every provider must still deliver the
// answer instead of reading it as an empty stream.
func TestChatStreamAcceptsNonStreamingJSON(t *testing.T) {
	cases := []struct {
		provider LLMProvider
		body     string
	}{
		{LLMOllama, `{"message":{"role":"assistant","content":"whole answer"},"done":true}`},
		{LLMOpenAI, `{"choices":[{"message":{"content":"whole "}},{"message":{"content":"answer"}}]}`},
		{LLMAnthropic, `{"content":[{"type":"text","text":"whole answer"},{"type":"tool_use","text":"ignored"}]}`},
	}
	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			s := newStreamServer(t, http.StatusOK, "application/json", tc.body)
			got, err := collectStream(t, newTestChat(tc.provider, s.srv.URL))
			if err != nil || got != "whole answer" {
				t.Fatalf("stream = %q, %v", got, err)
			}
		})
	}
}

// Malformed lines are skipped, the stream stops at its end marker, and the
// provider-specific request fields are sent.
func TestChatStreamParsesAndSkipsMalformedLines(t *testing.T) {
	cases := []struct {
		provider    LLMProvider
		contentType string
		body        string
		check       func(t *testing.T, req map[string]any, h http.Header)
	}{
		{LLMOllama, "application/x-ndjson",
			"\n{not json}\n" +
				`{"message":{"thinking":"hmm"}}` + "\n" +
				`{"message":{"content":"one "}}` + "\n" +
				`{"message":{"content":"two"},"done":true}` + "\n" +
				`{"message":{"content":" after done"}}` + "\n",
			func(t *testing.T, req map[string]any, h http.Header) {
				opts, _ := req["options"].(map[string]any)
				if opts["repeat_penalty"] != 1.3 || opts["num_predict"] != float64(100) {
					t.Errorf("ollama options = %v", opts)
				}
				if h.Get("Authorization") != "Bearer k" {
					t.Errorf("auth header = %q", h.Get("Authorization"))
				}
			}},
		{LLMOpenAI, "text/event-stream",
			": comment\n" +
				"data: {broken\n" +
				`data: {"choices":[{"delta":{"content":"one "}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{"content":"two"}}]}` + "\n\n" +
				"data: [DONE]\n" +
				`data: {"choices":[{"delta":{"content":" after done"}}]}` + "\n",
			func(t *testing.T, req map[string]any, h http.Header) {
				if req["repeat_penalty"] != 1.3 || req["max_tokens"] != float64(100) || req["stream"] != true {
					t.Errorf("openai request = %v", req)
				}
				if h.Get("Authorization") != "Bearer k" {
					t.Errorf("auth header = %q", h.Get("Authorization"))
				}
			}},
		{LLMAnthropic, "text/event-stream",
			"event: content_block_delta\n" +
				"data: nope\n" +
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"one "}}` + "\n" +
				`data: {"type":"ping"}` + "\n" +
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"two"}}` + "\n" +
				`data: {"type":"message_stop"}` + "\n" +
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" after stop"}}` + "\n",
			func(t *testing.T, req map[string]any, h http.Header) {
				if req["system"] != "be brief" || h.Get("x-api-key") != "k" || h.Get("anthropic-version") == "" {
					t.Errorf("anthropic request = %v, headers %v", req, h)
				}
				if msgs, _ := req["messages"].([]any); len(msgs) != 1 {
					t.Errorf("the system prompt must not be sent as a message: %v", req["messages"])
				}
			}},
	}
	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			s := newStreamServer(t, http.StatusOK, tc.contentType, tc.body)
			c := newTestChat(tc.provider, s.srv.URL)
			c.config.APIKey = "k"
			c.config.RepeatPenalty = 1.3
			var thinking string
			c.SetThinkingHandler(func(tok string) { thinking += tok })
			got, err := collectStream(t, c)
			if err != nil || got != "one two" {
				t.Fatalf("stream = %q, %v", got, err)
			}
			if tc.provider == LLMOllama && thinking != "hmm" {
				t.Errorf("thinking = %q", thinking)
			}
			req, h := s.last()
			tc.check(t, req, h)
		})
	}
}

func TestChatStreamReportsHTTPErrors(t *testing.T) {
	for _, p := range []LLMProvider{LLMOllama, LLMOpenAI, LLMAnthropic} {
		t.Run(string(p), func(t *testing.T) {
			s := newStreamServer(t, http.StatusServiceUnavailable, "text/plain", "overloaded")
			_, err := collectStream(t, newTestChat(p, s.srv.URL))
			if err == nil || !strings.Contains(err.Error(), "returned 503: overloaded") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	s := newStreamServer(t, http.StatusNotFound, "text/plain", "model 'test-model' not found")
	if _, err := collectStream(t, newTestChat(LLMOllama, s.srv.URL)); err == nil || !strings.Contains(err.Error(), "ollama pull test-model") {
		t.Fatalf("missing ollama model: %v", err)
	}
	if _, err := collectStream(t, newTestChat(LLMProvider("carrier-pigeon"), s.srv.URL)); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unknown provider: %v", err)
	}
	// Nothing listens: the error names the endpoint.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	for _, p := range []LLMProvider{LLMOpenAI, LLMAnthropic} {
		if _, err := collectStream(t, newTestChat(p, url)); err == nil || !strings.Contains(err.Error(), "POST "+url) {
			t.Fatalf("%s to a closed port: %v", p, err)
		}
	}
	if _, err := collectStream(t, newTestChat(LLMOllama, url)); err == nil || !strings.Contains(err.Error(), "ollama serve") {
		t.Fatalf("ollama to a closed port: %v", err)
	}
}

func TestAskStreamWithImages(t *testing.T) {
	s := newStreamServer(t, http.StatusOK, "application/x-ndjson",
		`{"message":{"content":"a red "}}`+"\n"+`{"message":{"content":"square"},"done":true}`+"\n")
	c := newTestChat(LLMOllama, s.srv.URL)
	var notified []SearchResult
	c.SetResultsHandler(func(r []SearchResult) { notified = r })
	var streamed strings.Builder
	results, err := c.AskStreamWithImages(context.Background(), "what is drawn?", []string{"aW1hZ2U="}, func(tok string) { streamed.WriteString(tok) })
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(notified) != 2 || streamed.String() != "a red square" {
		t.Fatalf("results %d, notified %d, streamed %q", len(results), len(notified), streamed.String())
	}
	req, _ := s.last()
	msgs, _ := req["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	if imgs, _ := last["images"].([]any); len(imgs) != 1 || imgs[0] != "aW1hZ2U=" {
		t.Fatalf("the image was not sent with the question: %v", last)
	}
	if content, _ := last["content"].(string); !strings.Contains(content, "doc1 content") || !strings.Contains(content, "what is drawn?") {
		t.Fatalf("prompt lacks the context or the question: %q", content)
	}
	h := c.History()
	if len(h) != 2 || h[0].Content != "what is drawn?" || h[1].Content != "a red square" {
		t.Fatalf("history = %+v", h)
	}

	// A failing search or LLM call leaves the history alone.
	c.SetSearcher(&chatMockSearcher{err: errors.New("index gone")})
	if _, err := c.AskStreamWithImages(context.Background(), "again?", nil, func(string) {}); err == nil || !strings.Contains(err.Error(), "index gone") {
		t.Fatalf("search error: %v", err)
	}
	bad := newStreamServer(t, http.StatusInternalServerError, "text/plain", "boom")
	c.SetSearcher(&chatMockSearcher{})
	c.config.BaseURL = bad.srv.URL
	if _, err := c.AskStreamWithImages(context.Background(), "again?", nil, func(string) {}); err == nil || !strings.Contains(err.Error(), "chat stream") {
		t.Fatalf("LLM error: %v", err)
	}
	if len(c.History()) != 2 {
		t.Fatalf("a failed turn changed the history: %+v", c.History())
	}
}

// Once the history exceeds its window, the dropped turns are condensed into
// the session summary, which is sent as a system message with later turns.
func TestLongConversationIsSummarised(t *testing.T) {
	s := newStreamServer(t, http.StatusOK, "application/x-ndjson", `{"message":{"content":"ok"},"done":true}`+"\n")
	c := newTestChat(LLMOllama, s.srv.URL)
	for i := 0; i < 12; i++ {
		c.AppendHistory(ChatMessage{Role: "user", Content: fmt.Sprintf("We decided to store embeddings in SQLite database number %d for durability reasons.", i)})
		c.AppendHistory(ChatMessage{Role: "assistant", Content: fmt.Sprintf("Noted: the SQLite database number %d keeps the embeddings durable.", i)})
	}
	if _, err := c.AskStream(context.Background(), "and now?", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if c.SessionSummary() == "" {
		t.Fatal("the dropped turns were not summarised")
	}
	if len(c.History()) > 22 {
		t.Fatalf("history kept %d messages", len(c.History()))
	}
	first := c.SessionSummary()

	// Overflow again: the new summary merges with the previous one.
	for i := 0; i < 6; i++ {
		c.AppendHistory(ChatMessage{Role: "user", Content: fmt.Sprintf("Later we moved the graph into the same SQLite file, step %d.", i)})
		c.AppendHistory(ChatMessage{Role: "assistant", Content: fmt.Sprintf("The graph now shares the SQLite file, step %d.", i)})
	}
	if _, err := c.AskStream(context.Background(), "and then?", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if c.SessionSummary() == "" || c.SessionSummary() == first && len(c.History()) > 22 {
		t.Fatalf("summary after the second overflow = %q", c.SessionSummary())
	}
	req, _ := s.last()
	found := false
	for _, m := range req["messages"].([]any) {
		mm := m.(map[string]any)
		if mm["role"] == "system" && strings.HasPrefix(mm["content"].(string), "Summary of earlier conversation") {
			found = true
		}
	}
	if !found {
		t.Fatal("the session summary was not sent to the model")
	}
	c.ClearHistory()
	if c.SessionSummary() != "" || len(c.History()) != 0 {
		t.Fatal("ClearHistory kept state")
	}
}
