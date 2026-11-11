package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleStorage(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"name":"docs","num_passages":5}`)
	if err := os.WriteFile(filepath.Join(dir, "docs", "docs.meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "docs.index"), make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	s := NewServer(cfg, ":0", "test")

	w := httptest.NewRecorder()
	s.handleStorage(w, httptest.NewRequest("GET", "/api/storage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got gleann.IndexUsage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Indexes != 1 || got.Passages != 5 || got.Bytes != int64(2048+len(meta)) || got.IndexDir != dir {
		t.Fatalf("got %+v", got)
	}
	if got.DiskTotalBytes == 0 || got.DiskFreeBytes == 0 || got.DiskFreeBytes > got.DiskTotalBytes {
		t.Fatalf("disk %d free of %d", got.DiskFreeBytes, got.DiskTotalBytes)
	}
}
