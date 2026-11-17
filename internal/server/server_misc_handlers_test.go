package server

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/pkg/conversations"
	"github.com/tevfik/gleann/pkg/gleann"
)

// writeTestFile creates path (and its parent directories) holding a small
// markdown document.
func writeTestFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("# Title\n\nsome text\n"), 0o644)
}

func TestHandleGetLogs(t *testing.T) {
	s := &Server{logBuffer: NewLogBuffer(5)}
	_, _ = s.logBuffer.Write([]byte("hello log"))
	w := doJSON(http.HandlerFunc(s.handleGetLogs), "GET", "/api/logs", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "hello log") {
		t.Errorf("got %d %s", w.Code, w.Body.String())
	}
}

func TestConversationUpdateAndClear(t *testing.T) {
	t.Setenv("GLEANN_CONVERSATIONS_DIR", t.TempDir())
	s := &Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/conversations/{id}", s.handleUpdateConversation)
	mux.HandleFunc("DELETE /api/conversations", s.handleClearConversations)

	store := conversations.DefaultStore()
	var ids []string
	for _, title := range []string{"first", "second"} {
		c := &conversations.Conversation{Title: title, Messages: []conversations.Message{{Role: "user", Content: title}}}
		if err := store.Save(c); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}

	if w := doJSON(mux, "PUT", "/api/conversations/"+ids[0], `{`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", w.Code)
	}
	if w := doJSON(mux, "PUT", "/api/conversations/nope", `{"title":"x"}`, nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown conversation: %d", w.Code)
	}
	w := doJSON(mux, "PUT", "/api/conversations/"+ids[0], `{"title":"renamed"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "renamed") {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if got, err := store.Load(ids[0]); err != nil || got.Title != "renamed" {
		t.Errorf("rename not persisted: %v %v", got, err)
	}

	w = doJSON(mux, "DELETE", "/api/conversations", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"count":2`) {
		t.Errorf("clear: %d %s", w.Code, w.Body.String())
	}
	if left, _ := store.List(); len(left) != 0 {
		t.Errorf("conversations left: %d", len(left))
	}
}

func TestClearConversations_StoreError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	if err := writeTestFile(file); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_CONVERSATIONS_DIR", file)
	w := doJSON(http.HandlerFunc((&Server{}).handleClearConversations), "DELETE", "/api/conversations", "", nil)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("got %d %s", w.Code, w.Body.String())
	}
}

func TestLastLines(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"a\nb\nc\n", 2, "b\nc"},
		{"a\nb", 5, "a\nb"},
		{"", 3, ""},
	}
	for _, tc := range tests {
		if got := lastLines(tc.in, tc.n); got != tc.want {
			t.Errorf("lastLines(%q,%d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestHandleIndexPath_Validation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", root)
	s := &Server{config: gleann.DefaultConfig(), searchers: map[string]*gleann.LeannSearcher{}}
	s.config.IndexDir = t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/indexes/{name}/index-path", s.handleIndexPath)

	tests := []struct {
		name, path, body string
		want             int
	}{
		{"invalid name", "/api/indexes/bad%20name/index-path", `{"path":"x"}`, 400},
		{"bad json", "/api/indexes/i/index-path", `{`, 400},
		{"missing path", "/api/indexes/i/index-path", `{}`, 400},
		{"outside allowed roots", "/api/indexes/i/index-path", `{"path":"` + filepath.ToSlash(t.TempDir()) + `"}`, 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if w := doJSON(mux, "POST", tc.path, tc.body, nil); w.Code != tc.want {
				t.Errorf("got %d %s, want %d", w.Code, w.Body.String(), tc.want)
			}
		})
	}
}

// A build that cannot succeed (the embedder is unreachable) is reported by the
// background task when a manager exists, and only logged when it does not.
func TestHandleIndexPath_BuildFailureSurfaces(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := writeTestFile(filepath.Join(docs, "a.md")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GLEANN_ALLOWED_ROOTS", root)
	isolateHome(t)

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.EmbeddingProvider = "ollama"
	cfg.EmbeddingModel = "m"
	cfg.OllamaHost = "http://127.0.0.1:1" // nothing listens here
	body := `{"path":"` + filepath.ToSlash(docs) + `","tags":["t"],"description":"d","mcp_exposed":true,"code_model":"c","doc_model":"d"}`

	t.Run("with task manager", func(t *testing.T) {
		bg := background.NewManager(1)
		defer bg.Stop()
		s := &Server{config: cfg, searchers: map[string]*gleann.LeannSearcher{}, bgManager: bg}
		t.Cleanup(s.invalidateAllSearchers)
		mux := http.NewServeMux()
		mux.HandleFunc("POST /api/indexes/{name}/index-path", s.handleIndexPath)
		w := doJSON(mux, "POST", "/api/indexes/withbg/index-path", body, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "indexing_started") {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		var failed *background.Task
		for _, task := range waitAllTerminal(t, bg) {
			if task.Type == background.TaskTypeAutoIndex {
				failed = task
			}
		}
		if failed == nil || failed.Status != background.StatusFailed || !strings.Contains(failed.Error, "indexing failed") {
			t.Errorf("task = %+v", failed)
		}
	})
	t.Run("without task manager", func(t *testing.T) {
		s := &Server{config: cfg, searchers: map[string]*gleann.LeannSearcher{}}
		t.Cleanup(s.invalidateAllSearchers)
		mux := http.NewServeMux()
		mux.HandleFunc("POST /api/indexes/{name}/index-path", s.handleIndexPath)
		w := doJSON(mux, "POST", "/api/indexes/nobg/index-path", body, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
		s.bgWG.Wait() // the build ran (and failed) in the background without crashing the server
	})
}

func waitAllTerminal(t *testing.T, bg *background.Manager) []*background.Task {
	t.Helper()
	var out []*background.Task
	for _, task := range bg.List("") {
		id := task.ID
		out = append(out, waitTask(t, bg, id, terminal))
	}
	return out
}

func multipartUpload(t *testing.T, field, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(fw, content)
	_ = mw.Close()
	return &body, mw.FormDataContentType()
}

func TestHandleUpload_Rejections(t *testing.T) {
	_, h, _ := newIndexedServer(t)

	post := func(name string, body io.Reader, ctype string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/indexes/"+name+"/upload", body)
		r.Header.Set("Content-Type", ctype)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	body, ct := multipartUpload(t, "file", "x.md", "hello")
	if w := post("bad%20name", body, ct); w.Code != http.StatusBadRequest {
		t.Errorf("invalid name: %d", w.Code)
	}

	// idx already holds passages: an upload would replace them, so it is refused.
	body, ct = multipartUpload(t, "file", "x.md", "hello")
	if w := post("idx", body, ct); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already has") {
		t.Errorf("existing index: %d %s", w.Code, w.Body.String())
	}

	// A fresh index name but no "file" part.
	body, ct = multipartUpload(t, "other", "x.md", "hello")
	if w := post("fresh", body, ct); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid file upload") {
		t.Errorf("wrong field: %d %s", w.Code, w.Body.String())
	}
	if w := post("fresh", strings.NewReader("not multipart"), "text/plain"); w.Code != http.StatusBadRequest {
		t.Errorf("not multipart: %d", w.Code)
	}

	// Filenames that reduce to nothing usable are refused.
	for _, fn := range []string{"..", "/", `\`} {
		body, ct = multipartUpload(t, "file", fn, "hello")
		if w := post("fresh", body, ct); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid file name") {
			t.Errorf("filename %q: %d %s", fn, w.Code, w.Body.String())
		}
	}
}

func TestHandleWatch_Validation(t *testing.T) {
	s, h, _ := newIndexedServer(t)
	root := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", root)
	s.bgManager = background.NewManager(1)
	defer s.bgManager.Stop()

	tests := []struct {
		name, path, body string
		want             int
		sub              string
	}{
		{"invalid name", "/api/indexes/bad%20name/watch", `{}`, 400, ""},
		{"bad json", "/api/indexes/idx/watch", `{`, 400, "invalid request body"},
		{"unknown index", "/api/indexes/ghost/watch", `{"enable":true,"dir":"x"}`, 404, "index not found"},
		{"no directory known", "/api/indexes/idx/watch", `{"enable":true}`, 400, "no directory"},
		{"directory outside allowed roots", "/api/indexes/idx/watch", `{"enable":true,"dir":"` + filepath.ToSlash(t.TempDir()) + `"}`, 403, "allowed_roots"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(h, "POST", tc.path, tc.body, nil)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.sub) {
				t.Errorf("got %d %s, want %d containing %q", w.Code, w.Body.String(), tc.want, tc.sub)
			}
		})
	}
}
