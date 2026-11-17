package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func postJSON(h http.Handler, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return w
}

func TestIndexPathOutsideAllowedRoots(t *testing.T) {
	s, h, _ := newBuildServer(t)
	allowed := t.TempDir()
	outside := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", allowed)
	if err := os.WriteFile(filepath.Join(outside, "x.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = s

	for _, p := range []string{outside, filepath.Join(allowed, "..", filepath.Base(outside)), "/etc"} {
		w := postJSON(h, "/api/indexes/leak/index-path", fmt.Sprintf(`{"path":%q}`, p))
		if w.Code != http.StatusForbidden {
			t.Errorf("path %q: want 403, got %d %s", p, w.Code, w.Body.String())
			continue
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if !strings.Contains(resp["error"], "allowed_roots") {
			t.Errorf("403 must name the setting: %q", resp["error"])
		}
	}

	w := postJSON(h, "/api/indexes/ok/index-path", fmt.Sprintf(`{"path":%q}`, allowed))
	if w.Code != http.StatusOK {
		t.Errorf("allowed root refused: %d %s", w.Code, w.Body.String())
	}
}

func TestIndexPathConfigRootsAndHomeDefault(t *testing.T) {
	s, h, _ := newBuildServer(t)
	t.Setenv("GLEANN_ALLOWED_ROOTS", "")
	home, _ := os.UserHomeDir()
	inHome := filepath.Join(home, "proj")
	if err := os.MkdirAll(inHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if w := postJSON(h, "/api/indexes/h/index-path", fmt.Sprintf(`{"path":%q}`, inHome)); w.Code != http.StatusOK {
		t.Errorf("home default: %d %s", w.Code, w.Body.String())
	}
	elsewhere := t.TempDir()
	if w := postJSON(h, "/api/indexes/h2/index-path", fmt.Sprintf(`{"path":%q}`, elsewhere)); w.Code != http.StatusForbidden {
		t.Errorf("outside home: want 403, got %d", w.Code)
	}
	s.config.AllowedRoots = []string{elsewhere}
	if w := postJSON(h, "/api/indexes/h2/index-path", fmt.Sprintf(`{"path":%q}`, elsewhere)); w.Code != http.StatusOK {
		t.Errorf("configured root: %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateDocsRootContained(t *testing.T) {
	_, h, _ := newBuildServer(t)
	root := t.TempDir()
	outside := t.TempDir()
	t.Setenv("GLEANN_ALLOWED_ROOTS", root)
	if err := os.WriteFile(filepath.Join(outside, "s.md"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := postJSON(h, "/api/indexes/u/update", fmt.Sprintf(`{"docs_root":%q,"sources":["s.md"]}`, outside))
	if w.Code != http.StatusForbidden {
		t.Errorf("docs_root outside: want 403, got %d %s", w.Code, w.Body.String())
	}

	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		w = postJSON(h, "/api/indexes/u/update", fmt.Sprintf(`{"docs_root":%q,"sources":["link/s.md"]}`, root))
		if w.Code != http.StatusForbidden {
			t.Errorf("symlinked source: want 403, got %d %s", w.Code, w.Body.String())
		}
	}
}
