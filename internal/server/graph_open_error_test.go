package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteGraphOpenError(t *testing.T) {
	w := httptest.NewRecorder()
	writeGraphOpenError(w, "test", errors.New("no such file"))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
