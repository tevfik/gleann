package server

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// An upload leaves the embedding cache directory and the vault untouched: the
// document lives only in its index.
func TestUploadLeavesNoTraceOutsideItsIndex(t *testing.T) {
	_, h, cfg := newBuildServer(t)
	home := os.Getenv("HOME") // newBuildServer points HOME at a temp dir

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "private.md")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(fw, "# Private\n\nThis text must not reach the embedding cache or the vault.\n")
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/indexes/priv/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(cfg.IndexDir, "priv")); err != nil {
		t.Fatalf("index missing: %v", err)
	}
	for _, p := range []string{
		filepath.Join(home, ".gleann", "cache"),
		filepath.Join(home, ".gleann", "vault"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s exists after an upload (err=%v)", p, err)
		}
	}
}
