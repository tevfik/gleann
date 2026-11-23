package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/a2a"
	"github.com/tevfik/gleann/internal/auth"
)

// a2aScopeFixture mounts the real A2A skills of s behind the real security
// middleware with a temp token store, and returns the handler plus an admin
// and a query token. Everything lives in temp dirs (no real home directory).
func a2aScopeFixture(t *testing.T, s *Server) (http.Handler, string, string) {
	t.Helper()
	t.Setenv("GLEANN_API_TOKEN", "")
	t.Setenv("GLEANN_ALLOWED_HOSTS", "")
	t.Setenv("GLEANN_CORS_ORIGINS", "")
	t.Setenv("GLEANN_A2A_ENABLED", "true")
	st, err := auth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	admin, _, err := st.EnsureAdmin()
	if err != nil {
		t.Fatal(err)
	}
	q, err := st.Create("q", auth.ScopeQuery)
	if err != nil {
		t.Fatal(err)
	}
	s.addr = "127.0.0.1:8081"
	s.version = "test"
	mux := http.NewServeMux()
	s.mountA2A(mux)
	return securityMiddleware(newSecurityConfig(s.addr, st, nil), mux), admin, q
}

// sendA2A posts one message to /a2a/v1/message:send with the given token.
func sendA2A(t *testing.T, h http.Handler, tok string, parts []a2a.Part, md map[string]any) a2a.SendMessageResponse {
	t.Helper()
	body, err := json.Marshal(a2a.SendMessageRequest{
		Message:  a2a.Message{MessageID: "m1", Role: "ROLE_USER", Parts: parts},
		Metadata: md,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/a2a/v1/message:send", bytes.NewReader(body))
	r.Host = "127.0.0.1:8081"
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("message:send status %d: %s", w.Code, w.Body.String())
	}
	var resp a2a.SendMessageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Task == nil {
		t.Fatalf("no task in response: %s", w.Body.String())
	}
	return resp
}

func a2aTaskOutcome(resp a2a.SendMessageResponse) (a2a.TaskState, string) {
	msg := ""
	if resp.Task.Status.Message != nil {
		for _, p := range resp.Task.Status.Message.Parts {
			msg += p.Text
		}
	}
	return resp.Task.Status.State, msg
}

func a2aArtifactText(resp a2a.SendMessageResponse) string {
	var out string
	for _, art := range resp.Task.Artifacts {
		for _, p := range art.Parts {
			out += p.Text
		}
	}
	return out
}

// A query token must not be able to plant long-term memory through A2A:
// storing blocks is an admin operation on every other surface (docs/api.md).
func TestA2AScope_MemoryWriteRequiresAdmin(t *testing.T) {
	s := newA2AServerWithMem(t)
	h, admin, q := a2aScopeFixture(t, s)
	md := map[string]any{"skill": "memory-management"}

	writes := []struct {
		name  string
		query string
		md    map[string]any
	}{
		{"remember", "remember that planted fact", md},
		{"turkish", "hatırla planted fact", md},
		{"store", "store planted fact", md},
		{"keyword routing", "remember that planted via keyword", nil},
	}
	for _, c := range writes {
		resp := sendA2A(t, h, q, []a2a.Part{{Text: c.query}}, c.md)
		state, msg := a2aTaskOutcome(resp)
		if state != a2a.TaskStateFailed || !strings.Contains(msg, "admin") {
			t.Errorf("%s: query token write: state=%s msg=%q, want FAILED naming the admin scope", c.name, state, msg)
		}
	}
	// Nothing may have been persisted by the refused writes.
	blocks, err := s.blockMem.Search("planted")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Errorf("query token planted %d memory blocks: %+v", len(blocks), blocks)
	}

	// Admin keeps the write capability.
	resp := sendA2A(t, h, admin, []a2a.Part{{Text: "remember that planted fact"}}, md)
	if state, msg := a2aTaskOutcome(resp); state != a2a.TaskStateCompleted {
		t.Fatalf("admin write: state=%s msg=%q", state, msg)
	}
	if out := a2aArtifactText(resp); !strings.Contains(out, "Stored in memory") {
		t.Errorf("admin write artifact = %q", out)
	}

	// Memory reads stay available to query tokens.
	resp = sendA2A(t, h, q, []a2a.Part{{Text: "recall planted"}}, md)
	if state, msg := a2aTaskOutcome(resp); state != a2a.TaskStateCompleted {
		t.Errorf("query recall: state=%s msg=%q", state, msg)
	}
}

// A query token must not be able to make the server read and summarise a
// local file by path: the file content would leak through the vision model.
func TestA2AScope_MultimodalPathRequiresAdmin(t *testing.T) {
	s, _, rec := newAskServer(t)
	dir := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", dir)
	img := filepath.Join(dir, "secret.png")
	if err := os.WriteFile(img, []byte("\x89PNG fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.cfgMu.Lock()
	s.config.MultimodalModel = "vision-x"
	s.cfgMu.Unlock()
	h, admin, q := a2aScopeFixture(t, s)
	md := map[string]any{"skill": "multimodal-analyze"}

	resp := sendA2A(t, h, q, []a2a.Part{{Text: img}}, md)
	state, msg := a2aTaskOutcome(resp)
	if state != a2a.TaskStateFailed || !strings.Contains(msg, "admin") {
		t.Errorf("query token path read: state=%s msg=%q, want FAILED naming the admin scope", state, msg)
	}
	if n := rec.calls(); n != 0 {
		t.Errorf("query token caused %d model calls: the local file was read and sent", n)
	}

	// Admin keeps the path form.
	resp = sendA2A(t, h, admin, []a2a.Part{{Text: img}}, md)
	if state, msg := a2aTaskOutcome(resp); state != a2a.TaskStateCompleted {
		t.Fatalf("admin path read: state=%s msg=%q", state, msg)
	}
	if out := a2aArtifactText(resp); !strings.Contains(out, "Analysis of") {
		t.Errorf("admin path artifact = %q", out)
	}
	if n := rec.calls(); n != 1 {
		t.Errorf("admin path read: model calls = %d, want 1", n)
	}

	// Attachments carry their own bytes and stay allowed for query tokens.
	resp = sendA2A(t, h, q, []a2a.Part{{Text: "describe", File: &a2a.FilePart{Name: "a.png", Bytes: "QUJD"}}}, md)
	if state, msg := a2aTaskOutcome(resp); state != a2a.TaskStateCompleted {
		t.Errorf("query attachment: state=%s msg=%q", state, msg)
	}
}

// Handler-level table test for the memory write gate (complements the HTTP
// test above; an unknown scope must fail closed just like "query").
func TestA2AScope_MemoryHandlerWriteScopes(t *testing.T) {
	s := newA2AServerWithMem(t)
	for _, c := range []struct{ name, scope, query string }{
		{"query remember", "query", "remember that scope fact"},
		{"unknown remember", "", "remember that scope fact"},
		{"query hatırla", "query", "hatırla scope fact"},
		{"query store", "query", "store scope fact"},
	} {
		_, err := s.a2aMemoryHandler(a2a.SkillContext{Query: c.query, Scope: c.scope})
		if err == nil || !strings.Contains(err.Error(), "admin") {
			t.Errorf("%s: err=%v, want admin-scope refusal", c.name, err)
		}
	}
	if blocks, err := s.blockMem.Search("scope fact"); err != nil || len(blocks) != 0 {
		t.Errorf("refused writes persisted blocks: %+v (err=%v)", blocks, err)
	}
	if out, err := s.a2aMemoryHandler(a2a.SkillContext{Query: "remember that scope fact", Scope: "admin"}); err != nil || !strings.Contains(out, "Stored in memory") {
		t.Errorf("admin write: out=%q err=%v", out, err)
	}
	if _, err := s.a2aMemoryHandler(a2a.SkillContext{Query: "recall scope fact", Scope: "query"}); err != nil {
		t.Errorf("query recall must stay allowed: %v", err)
	}
}

// Handler-level table test for the multimodal path gate.
func TestA2AScope_MultimodalHandlerScopes(t *testing.T) {
	s, _, rec := newAskServer(t)
	dir := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", dir)
	img := filepath.Join(dir, "pic.png")
	if err := os.WriteFile(img, []byte("\x89PNG fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"", "query"} {
		_, err := s.a2aMultimodalHandler(a2a.SkillContext{Query: img, Scope: scope})
		if err == nil || !strings.Contains(err.Error(), "admin") {
			t.Errorf("scope %q: err=%v, want admin-scope refusal", scope, err)
		}
	}
	if n := rec.calls(); n != 0 {
		t.Errorf("refused path reads caused %d model calls", n)
	}
	// Attachments stay query-allowed (they carry their own bytes).
	out, err := s.a2aMultimodalHandler(a2a.SkillContext{Files: []a2a.FilePart{{Name: "a.png"}}, Scope: "query"})
	if err != nil || !strings.Contains(out, "[a.png]") {
		t.Errorf("query attachment: out=%q err=%v", out, err)
	}
}
