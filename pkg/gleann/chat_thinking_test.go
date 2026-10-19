package gleann

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaStream_ThinkingTokensGoToHandlerOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"message":{"role":"assistant","thinking":"let me "},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","thinking":"think"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant","content":"42"},"done":false}`)
		fmt.Fprintln(w, `{"message":{"role":"assistant"},"done":true}`)
	}))
	defer srv.Close()

	c := NewChat(nil, ChatConfig{Provider: LLMOllama, Model: "m", BaseURL: srv.URL})
	var think, answer strings.Builder
	c.SetThinkingHandler(func(s string) { think.WriteString(s) })
	if err := c.chatOllamaStream(context.Background(), []ChatMessage{{Role: "user", Content: "q"}}, func(s string) { answer.WriteString(s) }); err != nil {
		t.Fatal(err)
	}
	if think.String() != "let me think" || answer.String() != "42" {
		t.Errorf("think=%q answer=%q", think.String(), answer.String())
	}

	// Without a handler the reasoning is dropped, as before.
	c.SetThinkingHandler(nil)
	answer.Reset()
	if err := c.chatOllamaStream(context.Background(), nil, func(s string) { answer.WriteString(s) }); err != nil || answer.String() != "42" {
		t.Errorf("answer=%q err=%v", answer.String(), err)
	}
}

func TestResultsHandler_FiresBeforeGeneration(t *testing.T) {
	want := []SearchResult{{Text: "x", Score: 1}}
	c := NewChat(stubSearcher{want}, ChatConfig{Provider: LLMMock, Model: "m"})
	var order []string
	c.SetResultsHandler(func(r []SearchResult) { order = append(order, fmt.Sprintf("results:%d", len(r))) })
	if _, err := c.AskStream(context.Background(), "q", func(string) {
		if len(order) == 0 || order[len(order)-1] != "token" {
			order = append(order, "token")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "results:1,token" {
		t.Errorf("order = %v", order)
	}
}

type stubSearcher struct{ r []SearchResult }

func (s stubSearcher) Search(ctx context.Context, q string, opts ...SearchOption) ([]SearchResult, error) {
	return s.r, nil
}
func (s stubSearcher) Close() error { return nil }
