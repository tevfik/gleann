package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tevfik/gleann/pkg/conversations"
)

// chatRecorder is a fake Ollama /api/chat endpoint. It records every request
// body and answers with a fixed reply (non-stream JSON or NDJSON chunks).
type chatRecorder struct {
	mu     sync.Mutex
	bodies []map[string]any
	status int // non-zero: reply with this status and an error body
}

func (c *chatRecorder) last(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.bodies) == 0 {
		t.Fatal("LLM was never called")
	}
	return c.bodies[len(c.bodies)-1]
}

func (c *chatRecorder) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

func newChatFake(t *testing.T, rec *chatRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		status := rec.status
		rec.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("llm exploded"))
			return
		}
		if stream, _ := body["stream"].(bool); stream {
			w.Header().Set("Content-Type", "application/x-ndjson")
			for _, tok := range []string{"Hel", "lo ", "world"} {
				b, _ := json.Marshal(map[string]any{"message": map[string]string{"role": "assistant", "content": tok}, "done": false})
				_, _ = w.Write(append(b, '\n'))
			}
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}` + "\n"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"role": "assistant", "content": "the answer"}, "done": true})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAskServer returns a server with a real index "idx" whose LLM settings
// point at a recording fake Ollama; conversations live in a temp dir.
func newAskServer(t *testing.T) (*Server, http.Handler, *chatRecorder) {
	t.Helper()
	s, h, _ := newIndexedServer(t)
	t.Setenv("GLEANN_CONVERSATIONS_DIR", t.TempDir())
	rec := &chatRecorder{}
	llm := newChatFake(t, rec)
	s.cfgMu.Lock()
	s.config.LLMProvider = "ollama"
	s.config.LLMModel = "chat-model"
	s.config.OllamaHost = llm.URL
	s.cfgMu.Unlock()
	return s, h, rec
}

func postObj(h http.Handler, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b)))
	return w
}

func TestHandleAsk_AnswersWithSourcesAndPassesModel(t *testing.T) {
	_, h, rec := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "what is alpha?", "top_k": 2})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp askResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Answer != "the answer" {
		t.Errorf("answer = %q", resp.Answer)
	}
	if len(resp.Sources) == 0 || len(resp.Sources) > 2 {
		t.Errorf("want 1..2 sources (top_k=2), got %d", len(resp.Sources))
	}
	if resp.Citations != nil {
		t.Error("citations must be absent unless requested")
	}
	if got := rec.last(t)["model"]; got != "chat-model" {
		t.Errorf("LLM model = %v, want chat-model", got)
	}
}

func TestHandleAsk_RequestOverridesAndSystemPrompt(t *testing.T) {
	_, h, rec := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{
		"question": "q", "llm_model": "other-model", "system_prompt": "You are a pirate.",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	body := rec.last(t)
	if body["model"] != "other-model" {
		t.Errorf("model override ignored: %v", body["model"])
	}
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatal("no messages sent to LLM")
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || !strings.Contains(first["content"].(string), "pirate") {
		t.Errorf("system prompt not first message: %v", first)
	}
}

func TestHandleAsk_Citations(t *testing.T) {
	_, h, _ := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "alpha", "cite": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp askResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Citations == nil {
		t.Fatal("cite=true must attach citations")
	}
}

func TestHandleAsk_ImagesAreForwarded(t *testing.T) {
	_, h, rec := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q", "images": []string{"QUJD"}})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	msgs := rec.last(t)["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	imgs, _ := last["images"].([]any)
	if len(imgs) != 1 || imgs[0] != "QUJD" {
		t.Errorf("images not attached to the user message: %v", last)
	}
}

func TestHandleAsk_RestoresConversationHistory(t *testing.T) {
	_, h, rec := newAskServer(t)
	store := conversations.DefaultStore()
	conv := &conversations.Conversation{
		ID: "conv-history", Title: "t",
		Messages: []conversations.Message{{Role: "user", Content: "earlier question"}, {Role: "assistant", Content: "earlier answer"}},
	}
	if err := store.Save(conv); err != nil {
		t.Fatal(err)
	}
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "follow up", "conversation_id": "conv-history"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	raw, _ := json.Marshal(rec.last(t)["messages"])
	if !strings.Contains(string(raw), "earlier question") || !strings.Contains(string(raw), "earlier answer") {
		t.Errorf("history not replayed to the LLM: %s", raw)
	}
}

func TestHandleAsk_LLMErrorIs500(t *testing.T) {
	_, h, rec := newAskServer(t)
	rec.status = http.StatusInternalServerError
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q"})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ask failed") {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestHandleAsk_VisionRAGWithoutSourceDirFallsBackToText(t *testing.T) {
	_, h, rec := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q", "vision_rag": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	msgs := rec.last(t)["messages"].([]any)
	if imgs, _ := msgs[len(msgs)-1].(map[string]any)["images"].([]any); len(imgs) != 0 {
		t.Errorf("no images expected, got %v", imgs)
	}
}

func TestHandleAsk_ExpandOptionStillAnswers(t *testing.T) {
	s, h, _ := newAskServer(t)
	// The index holds prose, so HyDE expansion is a no-op; the request must
	// still be answered and the shared expander for this LLM config cached.
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "alpha", "expand": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	cfg := s.cfg()
	first, second := s.queryExpander(cfg), s.queryExpander(cfg)
	if first != second {
		t.Error("expander must be shared per LLM config")
	}
}

func TestHandleAsk_InvalidIndexNameAndTagGate(t *testing.T) {
	_, h, rec := newAskServer(t)
	if w := postObj(h, "/api/indexes/bad%20name/ask", map[string]any{"question": "q"}); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid index name: status %d", w.Code)
	}
	// An index without the required tag must be refused before the LLM runs.
	t.Setenv("GLEANN_TAGS", "secret-tag")
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("tag gate: status %d: %s", w.Code, w.Body.String())
	}
	if rec.calls() != 0 {
		t.Error("LLM must not be called for a forbidden index")
	}
}

// sseEvents parses "data: ..." lines of an SSE body.
func sseEvents(body string) []string {
	var out []string
	for _, ln := range strings.Split(body, "\n") {
		if rest, ok := strings.CutPrefix(ln, "data: "); ok {
			out = append(out, rest)
		}
	}
	return out
}

func TestHandleAskStream_TokensSourcesConversationDone(t *testing.T) {
	_, h, _ := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "stream me", "stream": true})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q", ct)
	}
	evs := sseEvents(w.Body.String())
	if len(evs) < 4 || evs[len(evs)-1] != "[DONE]" {
		t.Fatalf("events = %v", evs)
	}
	var text string
	var sawSources bool
	var convID string
	for _, e := range evs {
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(e), &m) != nil {
			continue
		}
		if tok, ok := m["token"]; ok {
			var s string
			_ = json.Unmarshal(tok, &s)
			text += s
		}
		if _, ok := m["sources"]; ok {
			sawSources = true
		}
		if c, ok := m["conversation_id"]; ok {
			_ = json.Unmarshal(c, &convID)
		}
	}
	if text != "Hello world" {
		t.Errorf("streamed text = %q", text)
	}
	if !sawSources {
		t.Error("no sources event")
	}
	if convID == "" {
		t.Fatal("no conversation_id event")
	}
	conv, err := conversations.DefaultStore().Load(convID)
	if err != nil {
		t.Fatalf("conversation not persisted: %v", err)
	}
	if conv.Title != "stream me" || len(conv.Messages) < 2 {
		t.Errorf("saved conversation = %+v", conv)
	}
}

func TestHandleAskStream_QueryParamAndLongTitleTruncated(t *testing.T) {
	_, h, _ := newAskServer(t)
	q := strings.Repeat("x", 80)
	b, _ := json.Marshal(map[string]any{"question": q})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/indexes/idx/ask?stream=true", bytes.NewReader(b)))
	if !strings.HasSuffix(strings.TrimSpace(w.Body.String()), "[DONE]") {
		t.Fatalf("not an SSE stream: %s", w.Body.String())
	}
	convs, err := conversations.DefaultStore().List()
	if err != nil || len(convs) != 1 {
		t.Fatalf("List = %v, %v", convs, err)
	}
	if want := strings.Repeat("x", 47) + "..."; convs[0].Title != want {
		t.Errorf("title = %q, want %q", convs[0].Title, want)
	}
}

func TestHandleAskStream_ContinuesExistingConversationKeepingTitle(t *testing.T) {
	_, h, _ := newAskServer(t)
	store := conversations.DefaultStore()
	if err := store.Save(&conversations.Conversation{ID: "keep-title", Title: "Original title"}); err != nil {
		t.Fatal(err)
	}
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "more", "stream": true, "conversation_id": "keep-title"})
	if !strings.Contains(w.Body.String(), `"conversation_id":"keep-title"`) {
		t.Fatalf("conversation id not echoed: %s", w.Body.String())
	}
	conv, err := store.Load("keep-title")
	if err != nil {
		t.Fatal(err)
	}
	if conv.Title != "Original title" {
		t.Errorf("title overwritten: %q", conv.Title)
	}
}

func TestHandleAskStream_LLMErrorIsAnEvent(t *testing.T) {
	_, h, rec := newAskServer(t)
	rec.status = http.StatusInternalServerError
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q", "stream": true})
	if w.Code != http.StatusOK { // headers are already sent when the LLM fails
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"error"`) || !strings.Contains(body, "llm exploded") {
		t.Errorf("error event missing: %s", body)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "[DONE]") {
		t.Errorf("stream must still terminate with [DONE]: %s", body)
	}
}

func TestHandleAskStream_VisionRAGStatusEvents(t *testing.T) {
	_, h, _ := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q", "stream": true, "vision_rag": true})
	body := w.Body.String()
	for _, want := range []string{"Analysing documents for visual context", "No relevant images found"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing status %q in %s", want, body)
		}
	}
}

func TestHandleAskStream_WithImagesReportsCount(t *testing.T) {
	_, h, rec := newAskServer(t)
	w := postObj(h, "/api/indexes/idx/ask", map[string]any{"question": "q", "stream": true, "vision_rag": true, "images": []string{"QUJD"}})
	if !strings.Contains(w.Body.String(), "Sending 1 image(s) to multimodal model") {
		t.Errorf("body = %s", w.Body.String())
	}
	msgs := rec.last(t)["messages"].([]any)
	if imgs, _ := msgs[len(msgs)-1].(map[string]any)["images"].([]any); len(imgs) != 1 {
		t.Errorf("image not forwarded on stream path: %v", imgs)
	}
}
