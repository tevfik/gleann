package conversations

import (
	"context"
	"errors"
	"testing"
)

func TestOpenAIBaseURLNeverDefaultsToTheCloud(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	if _, err := OpenAIBaseURL(""); !errors.Is(err, ErrOpenAIEndpointUnset) {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("OPENAI_BASE_URL", "http://env:1/v1/")
	if u, err := OpenAIBaseURL(""); err != nil || u != "http://env:1" {
		t.Errorf("env = %q, %v", u, err)
	}
	if u, _ := OpenAIBaseURL("http://cfg:2/v1"); u != "http://cfg:2" {
		t.Errorf("configured = %q", u)
	}
}

// Conversation titles are summarised from the conversation itself: without
// an endpoint the summary fails instead of going to api.openai.com.
func TestSummarizerOpenAIWithoutEndpointFails(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	s := &Summarizer{Provider: "openai", Model: "m", APIKey: "sk"}
	if _, err := s.callOpenAI(context.Background(), nil, "private conversation"); !errors.Is(err, ErrOpenAIEndpointUnset) {
		t.Fatalf("err = %v", err)
	}
}
