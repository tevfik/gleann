package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/embedding"
	"github.com/tevfik/gleann/pkg/conversations"
)

// newOtherModelEmbedder is an embedder for a model other than the one the test
// index was built with.
func newOtherModelEmbedder(host string) *embedding.Computer {
	return embedding.NewComputer(embedding.Options{Provider: embedding.ProviderOllama, Model: "other-model", BaseURL: host})
}

func TestUpdateIndex_SourceHandling(t *testing.T) {
	_, h, _ := newIndexedServer(t)
	docs := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", docs)
	if err := os.WriteFile(filepath.Join(docs, "new.md"), []byte("brand new passage about zebras"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.ToSlash(docs)

	tests := []struct {
		name string
		body string
		want int
		sub  string
	}{
		{"bad json", `{`, 400, "invalid request body"},
		{"nothing to update", `{}`, 400, "sources or items required"},
		{"absolute source", `{"docs_root":"` + root + `","sources":["/etc/passwd"]}`, 400, "invalid source path"},
		{"escaping source", `{"docs_root":"` + root + `","sources":["../x.md"]}`, 400, "invalid source path"},
		{"docs_root outside allowed roots", `{"docs_root":"` + filepath.ToSlash(t.TempDir()) + `","sources":["a.md"]}`, 403, "allowed_roots"},
		{"missing source file", `{"docs_root":"` + root + `","sources":["absent.md"]}`, 400, "read source file"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(h, "POST", "/api/indexes/idx/update", tc.body, nil)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.sub) {
				t.Errorf("got %d %s, want %d containing %q", w.Code, w.Body.String(), tc.want, tc.sub)
			}
		})
	}

	// A readable source is embedded into the index and reported back.
	w := doJSON(h, "POST", "/api/indexes/idx/update", `{"docs_root":"`+root+`","sources":["new.md"]}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" || resp["items_count"] != float64(1) || resp["mode"] == "" {
		t.Errorf("response = %v", resp)
	}
	if _, stale := resp["graph_stale"]; stale {
		t.Error("an index without a code graph must not report graph_stale")
	}
}

func TestUpdateIndex_ModelMismatchAndUnknownIndex(t *testing.T) {
	s, h, _ := newIndexedServer(t)

	// Updating an index that was never built is a 404.
	w := doJSON(h, "POST", "/api/indexes/ghost/update", `{"sources":["a.md"],"items":[{"text":"x"}]}`, nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "build it before updating") {
		t.Errorf("unknown index: %d %s", w.Code, w.Body.String())
	}

	s.cfgMu.Lock()
	s.embedder = newOtherModelEmbedder(s.config.OllamaHost)
	s.cfgMu.Unlock()
	w = doJSON(h, "POST", "/api/indexes/idx/update", `{"sources":["a.md"],"items":[{"text":"x"}]}`, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "model mismatch") {
		t.Errorf("mismatch: %d %s", w.Code, w.Body.String())
	}
}

func TestConversationEndpoints_RoundTrip(t *testing.T) {
	t.Setenv("GLEANN_CONVERSATIONS_DIR", t.TempDir())
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/conversations", s.handleListConversations)
	mux.HandleFunc("GET /api/conversations/{id}", s.handleGetConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)

	if w := doJSON(mux, "GET", "/api/conversations", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"count":0`) {
		t.Fatalf("empty list: %d %s", w.Code, w.Body.String())
	}

	conv := &conversations.Conversation{
		Title:    "design chat",
		Model:    "m1",
		Indexes:  []string{"docs", "code"},
		Messages: []conversations.Message{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}},
	}
	if err := conversations.DefaultStore().Save(conv); err != nil {
		t.Fatal(err)
	}

	w := doJSON(mux, "GET", "/api/conversations", "", nil)
	var list struct {
		Conversations []struct {
			ID, ShortID, Title, Model, Indexes string
			Messages                           int `json:"message_count"`
		} `json:"conversations"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 1 || list.Conversations[0].Indexes != "docs,code" || list.Conversations[0].Messages != 1 /* user turns */ ||
		list.Conversations[0].Title != "design chat" {
		t.Errorf("list = %+v", list)
	}

	// Fetch by id, by unique title, and delete.
	for _, key := range []string{conv.ID, "design chat"} {
		if w := doJSON(mux, "GET", "/api/conversations/"+strings.ReplaceAll(key, " ", "%20"), "", nil); w.Code != 200 ||
			!strings.Contains(w.Body.String(), conv.ID) {
			t.Errorf("get %q: %d %s", key, w.Code, w.Body.String())
		}
	}
	if w := doJSON(mux, "DELETE", "/api/conversations/"+conv.ID, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted"`) {
		t.Errorf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(mux, "GET", "/api/conversations/"+conv.ID, "", nil); w.Code != http.StatusNotFound {
		t.Errorf("get after delete: %d", w.Code)
	}
}

func TestListConversations_StoreError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_CONVERSATIONS_DIR", file) // a regular file where a directory is expected
	w := doJSON(http.HandlerFunc((&Server{}).handleListConversations), "GET", "/api/conversations", "", nil)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "failed to list conversations") {
		t.Errorf("got %d %s", w.Code, w.Body.String())
	}
}
