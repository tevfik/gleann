package gleann

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchToFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("payload"))
		case "/release":
			_, _ = w.Write([]byte(`{"tag_name":"v1.2.3"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		dest := filepath.Join(dir, "ok.bin")
		if err := fetchToFile(ctx, srv.URL+"/ok", dest); err != nil {
			t.Fatalf("fetchToFile: %v", err)
		}
		data, err := os.ReadFile(dest)
		if err != nil || string(data) != "payload" {
			t.Fatalf("got %q, %v", data, err)
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 {
			t.Fatalf("expected only the destination file, found %d entries", len(entries))
		}
	})

	t.Run("status error leaves no file", func(t *testing.T) {
		dest := filepath.Join(dir, "missing.bin")
		err := fetchToFile(ctx, srv.URL+"/missing", dest)
		var se *httpStatusError
		if !errors.As(err, &se) || se.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404 httpStatusError, got %v", err)
		}
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Fatalf("destination should not exist after failed download")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if err := fetchToFile(cctx, srv.URL+"/ok", filepath.Join(dir, "c.bin")); err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})

	t.Run("fetchJSON", func(t *testing.T) {
		var rel struct {
			TagName string `json:"tag_name"`
		}
		if err := fetchJSON(ctx, srv.URL+"/release", &rel); err != nil || rel.TagName != "v1.2.3" {
			t.Fatalf("fetchJSON: %v %+v", err, rel)
		}
	})
}
