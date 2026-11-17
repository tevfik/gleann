package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/conversations"
)

// CLI tests of conversation management (`gleann chat --list/--show/...`),
// `gleann tasks`, `gleann token` and `gleann tokens`.

func TestCLIBConversations(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("chat", "--list")
	wantExit(t, r, 0)
	mustContain(t, "empty list", r.Out, "No saved conversations.")
	r = e.run("chat", "--show-last")
	wantExit(t, r, 1)
	mustContain(t, "show-last empty", r.Err, "no saved conversations")
	r = e.run("chat", "--pick")
	wantExit(t, r, 0)
	mustContain(t, "pick empty", r.Out, "No saved conversations.")

	old := bSaveConv(t, "Old thread", "What was decided last year?", "Nothing much.")
	bAgeConversation(t, e, old.ID, 45*24*time.Hour)
	recent := bSaveConv(t, "Rate limiting design", "How does the limiter work?", "A token bucket.")
	sys := &conversations.Conversation{Title: "With system", Messages: []conversations.Message{
		{Role: "system", Content: "You are terse."}, {Role: "user", Content: "hi"}}}
	if err := conversations.DefaultStore().Save(sys); err != nil {
		t.Fatal(err)
	}

	r = e.run("chat", "--list")
	wantExit(t, r, 0)
	mustContain(t, "list", r.Out, "Conversations (3)", conversations.ShortID(recent.ID), "Rate limiting design", "1 msgs", "index=demo", "index=-")

	r = e.run("chat", "--show", recent.ID[:8])
	wantExit(t, r, 0)
	mustContain(t, "show", r.Out, "Title:   Rate limiting design", "You: How does the limiter work?", "Assistant: A token bucket.", "Messages: 1")
	r = e.run("chat", "--show", "With system")
	wantExit(t, r, 0)
	mustContain(t, "show by title", r.Out, "[system] You are terse.")
	r = e.run("chat", "--show", "no-such-conversation")
	wantExit(t, r, 1)
	mustContain(t, "show missing", r.Err, "conversation not found")

	r = e.run("chat", "--show-last")
	wantExit(t, r, 0)
	mustContain(t, "show-last", r.Out, "Conversation: ")

	// Invalid duration, then delete the 45-day-old one.
	r = e.run("chat", "--delete-older-than", "eventually")
	wantExit(t, r, 1)
	mustContain(t, "bad duration", r.Err, `invalid duration "eventually"`)
	r = e.run("chat", "--delete-older-than", "30d")
	wantExit(t, r, 0)
	mustContain(t, "delete-older-than", r.Out, "Deleted 1 conversation(s) older than 30d")
	r = e.run("chat", "--delete-older-than", "720h")
	wantExit(t, r, 0)
	mustContain(t, "delete-older-than go duration", r.Out, "Deleted 0 conversation(s) older than 720h")

	r = e.run("chat", "--delete", recent.ID, "--delete", "ghost-id")
	wantExit(t, r, 0)
	mustContain(t, "delete", r.Out, "Deleted conversation "+recent.ID)
	mustContain(t, "delete missing", r.Err, `error deleting "ghost-id"`)

	convs, err := conversations.DefaultStore().List()
	if err != nil || len(convs) != 1 || convs[0].Title != "With system" {
		t.Errorf("remaining conversations = %+v (%v)", convs, err)
	}

	// The conversation handler lists by default.
	r = captureRun(t, "", func() { cmdConversations(nil) })
	wantExit(t, r, 0)
	mustContain(t, "default list", r.Out, "Conversations (1)")
}

func TestCLIBConversationsUnreadableStore(t *testing.T) {
	e := newCLIEnv(t)
	// A file where the conversation directory should be makes List fail.
	p := filepath.Join(e.Home, "convfile")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_CONVERSATIONS_DIR", p)
	for _, args := range [][]string{{"chat", "--list"}, {"chat", "--pick"}, {"chat", "--delete-older-than", "1d"}} {
		r := e.run(args...)
		wantExit(t, r, 1)
		mustContain(t, strings.Join(args, " "), r.Err, "error:")
	}
}

func TestCLIBTasks(t *testing.T) {
	e := newCLIEnv(t)
	now := time.Now()
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		if r.URL.Query().Get("status") == "failed" {
			_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []map[string]any{
			{"id": "t-done", "type": "ReIndex", "status": "completed", "progress": 1.0, "message": "ok", "created_at": now.Add(-3 * time.Hour)},
			{"id": "t-run", "type": "AutoIndex", "status": "running", "progress": 0.5, "message": "indexing", "created_at": now},
			{"id": "t-wait", "type": "HealthCheck", "status": "queued", "progress": 0, "message": "", "created_at": now.Add(-10 * time.Minute)},
		}})
	}))
	defer srv.Close()

	r := e.run("tasks", "--addr", srv.URL+"/")
	wantExit(t, r, 0)
	mustContain(t, "tasks", r.Out, "ID", "STATUS", "🔄 running", "⏳ queued", "✅ completed", "50%", "3h ago", "1 running, 1 queued, 3 total")
	if strings.Index(r.Out, "t-run") > strings.Index(r.Out, "t-wait") || strings.Index(r.Out, "t-wait") > strings.Index(r.Out, "t-done") {
		t.Errorf("tasks not sorted running, queued, completed:\n%s", r.Out)
	}

	r = e.run("tasks", "--addr", srv.URL, "--json")
	wantExit(t, r, 0)
	var res struct {
		Tasks []taskInfo `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(r.Out), &res); err != nil || len(res.Tasks) != 3 {
		t.Errorf("tasks --json = %q (%v)", r.Out, err)
	}

	r = e.run("tasks", "--addr", srv.URL, "--status", "failed")
	wantExit(t, r, 0)
	mustContain(t, "tasks empty", r.Out, "No background tasks.")
	if gotQuery != "status=failed" {
		t.Errorf("status filter query = %q", gotQuery)
	}

	r = e.run("tasks", "help")
	wantExit(t, r, 0)
	mustContain(t, "tasks help", r.Out, "gleann tasks — view background tasks")
}

func TestCLIBTasksServerErrors(t *testing.T) {
	e := newCLIEnv(t)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broken/api/tasks" {
			_, _ = w.Write([]byte("not json"))
			return
		}
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	r := e.run("tasks", "--addr", bad.URL)
	wantExit(t, r, 1)
	mustContain(t, "status error", r.Err, "server returned 503")
	r = e.run("tasks", "--addr", bad.URL+"/broken")
	wantExit(t, r, 1)
	mustContain(t, "bad json", r.Err, "invalid response")

	url := bad.URL
	bad.Close() // nothing listens there any more
	r = e.run("tasks", "--addr", url)
	wantExit(t, r, 1)
	mustContain(t, "unreachable", r.Err, "cannot reach gleann server at "+url, "is 'gleann serve' running?")

	r = e.run("tasks", "--bogus")
	wantExit(t, r, 2)
}

func TestCLIBToken(t *testing.T) {
	e := newCLIEnv(t)

	r := e.run("token")
	wantExit(t, r, 0)
	mustContain(t, "token usage", r.Out, "Usage: gleann token <command>")

	r = e.run("token", "create", "ci-bot")
	wantExit(t, r, 0)
	raw := strings.TrimSpace(r.Out)
	if raw == "" || strings.Contains(raw, "\n") {
		t.Fatalf("token create must print exactly the token, got %q", r.Out)
	}
	mustContain(t, "token create", r.Err, `token "ci-bot" (query) created`)

	r = e.run("token", "create", "ops", "--scope", "admin")
	wantExit(t, r, 0)
	mustContain(t, "token create admin", r.Err, `token "ops" (admin) created`)

	r = e.run("token", "list")
	wantExit(t, r, 0)
	mustContain(t, "token list", r.Out, "NAME", "SCOPE", "ci-bot", "query", "ops", "admin")
	mustNotContain(t, "token list", r.Out, raw)

	r = e.run("token", "show")
	wantExit(t, r, 0)
	admin := strings.TrimSpace(r.Out)
	data, err := os.ReadFile(authTokenPath())
	if err != nil || strings.TrimSpace(string(data)) != admin {
		t.Fatalf("token show = %q, file %s = %q (%v)", admin, authTokenPath(), data, err)
	}
	if !strings.HasPrefix(authTokenPath(), filepath.Join(e.Home, ".gleann", "auth")) {
		t.Errorf("authTokenPath %s is outside the test home", authTokenPath())
	}

	r = e.run("token", "rotate")
	wantExit(t, r, 0)
	mustContain(t, "token rotate", r.Out, "admin token rotated; new value in "+authTokenPath())
	data, _ = os.ReadFile(authTokenPath())
	if strings.TrimSpace(string(data)) == admin {
		t.Error("rotate did not change the admin token")
	}

	r = e.run("token", "revoke", "ci-bot")
	wantExit(t, r, 0)
	mustContain(t, "token revoke", r.Out, "revoked ci-bot")
	r = e.run("token", "list")
	mustNotContain(t, "token list after revoke", r.Out, "ci-bot")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"token", "create"}, "usage: gleann token create"},
		{[]string{"token", "create", "--scope", "admin"}, "usage: gleann token create"},
		{[]string{"token", "create", "x", "--scope", "root"}, "error:"},
		{[]string{"token", "revoke"}, "usage: gleann token revoke"},
		{[]string{"token", "revoke", "never-existed"}, "error:"},
		{[]string{"token", "frobnicate"}, `unknown token command "frobnicate"`},
	} {
		r := e.run(tc.args...)
		if r.Code != 1 || !strings.Contains(r.Err, tc.want) {
			t.Errorf("%v: exit %d stderr %q, want 1 with %q", tc.args, r.Code, r.Err, tc.want)
		}
	}
}

func TestCLIBTokenStoreUnusable(t *testing.T) {
	e := newCLIEnv(t)
	// A file in place of ~/.gleann/auth makes the store fail to open.
	if err := os.MkdirAll(filepath.Join(e.Home, ".gleann"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Home, ".gleann", "auth"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("token", "list")
	wantExit(t, r, 1)
	mustContain(t, "unusable store", r.Err, "error:")
}

func TestCLIBTokens(t *testing.T) {
	e := newCLIEnv(t)
	e.sampleDocs()
	e.write("logo.png", "\x89PNG binary")
	e.write("blob.txt", "\xff\xfe\x00invalid utf8")

	r := e.run("tokens", e.Docs)
	wantExit(t, r, 0)
	mustContain(t, "tokens dir", r.Out, "Token Estimation for: "+e.Docs, "Scanned 4 file(s)", "Read Mode", "full")

	r = e.run("tokens", filepath.Join(e.Docs, "README.md"))
	wantExit(t, r, 0)
	mustContain(t, "tokens file", r.Out, "Scanned 1 file(s)")

	empty := filepath.Join(e.Home, "only-binaries")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "a.zip"), []byte("PK"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = e.run("tokens", empty)
	wantExit(t, r, 0)
	mustContain(t, "tokens no text", r.Out, "No readable text files found.")

	r = e.run("tokens")
	wantExit(t, r, 1)
	mustContain(t, "tokens usage", r.Out, "gleann tokens — Token consumption comparison")
	r = e.run("tokens", filepath.Join(e.Home, "does-not-exist"))
	wantExit(t, r, 1)
	mustContain(t, "tokens missing", r.Err, "Error:")
}
