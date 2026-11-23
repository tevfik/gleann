package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScopeContextRoundTrip(t *testing.T) {
	if got := ScopeFromContext(context.Background()); got != "" {
		t.Errorf("scope of bare context = %q, want empty", got)
	}
	for _, scope := range []string{"query", "admin"} {
		if got := ScopeFromContext(WithScope(context.Background(), scope)); got != scope {
			t.Errorf("round trip: got %q, want %q", got, scope)
		}
	}
}

// The scope attached to the request context by the HTTP front end must reach
// the skill handler unchanged (including the empty/unknown case).
func TestSendMessage_PropagatesScope(t *testing.T) {
	s := NewServer(DefaultAgentCard("test", "http://localhost:8080"))
	var got string
	s.RegisterSkill("semantic-search", func(ctx SkillContext) (string, error) {
		got = ctx.Scope
		return "ok", nil
	})
	mux := http.NewServeMux()
	s.Mount(mux)
	body, err := json.Marshal(SendMessageRequest{
		Message: Message{MessageID: "m1", Role: "ROLE_USER", Parts: []Part{{Text: "search anything"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"", "query", "admin"} {
		r := httptest.NewRequest(http.MethodPost, "/a2a/v1/message:send", bytes.NewReader(body))
		if scope != "" {
			r = r.WithContext(WithScope(r.Context(), scope))
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("scope %q: status %d: %s", scope, w.Code, w.Body.String())
		}
		if got != scope {
			t.Errorf("handler saw scope %q, want %q", got, scope)
		}
	}
}
