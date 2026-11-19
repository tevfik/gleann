package mcp

import (
	"strings"
	"testing"
)

// batch_ask with provider openai and no openai_base_url used to send every
// question (and the OpenAI key) to the Ollama host. Now each question fails
// with the missing-endpoint error and the LLM server gets nothing.
func TestBatchAskOpenAIWithoutEndpointSendsNothing(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	srv, llm := h2LLMServer(t, func(c *Config) {
		c.LLMProvider = "openai"
		c.OpenAIAPIKey = "sk-batch"
	})
	res := h2Call(t, srv.handleBatchAsk, map[string]any{"index": "idx", "questions": []any{"secret?"}})
	if out := extractText(res); !strings.Contains(out, "openai endpoint not set") {
		t.Errorf("batch output lacks the missing-endpoint error:\n%s", out)
	}
	if paths, _, _ := llm.calls(); len(paths) != 0 {
		t.Errorf("the Ollama host got %v", paths)
	}
}
