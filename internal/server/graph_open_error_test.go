package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteGraphOpenError(t *testing.T) {
	t.Run("unsupported build is 503 not 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeGraphOpenError(w, "test", errGraphUnsupported)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		body := w.Body.String()
		if strings.Contains(body, "not found") {
			t.Errorf("body must not claim the index is missing: %s", body)
		}
		if !strings.Contains(body, "treesitter") {
			t.Errorf("body should name the build tag: %s", body)
		}
	})
	t.Run("other errors stay 404", func(t *testing.T) {
		w := httptest.NewRecorder()
		writeGraphOpenError(w, "test", errors.New("no such file"))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})
}
