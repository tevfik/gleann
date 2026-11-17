package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

func storageReportOf(t *testing.T, s *Server) storageReport {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleStorage(w, httptest.NewRequest("GET", "/api/storage", nil))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var rep storageReport
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestHandleStorage_ReportsDataFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GLEANN_CONFIG_DIR", home)
	if err := os.MkdirAll(filepath.Join(home, "models"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "models", "m.bin"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.json"), make([]byte, 7), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = filepath.Join(home, "indexes")
	s := NewServer(cfg, ":0", "test")

	rep := storageReportOf(t, s)
	if rep.DataDir != home || rep.DataBytes != 4103 {
		t.Fatalf("data_dir=%q data_bytes=%d", rep.DataDir, rep.DataBytes)
	}
	if rep.DataFolders["models"] != 4096 || rep.DataFolders["config.json"] != 7 {
		t.Fatalf("data_folders = %v", rep.DataFolders)
	}
	if rep.IndexDir != cfg.IndexDir {
		t.Fatalf("index_dir = %q", rep.IndexDir)
	}
}

func TestDataUsageCache_ReusesMeasurementWithinTTL(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &dataUsageCache{ttl: 40 * time.Millisecond}
	u, err := c.get(dir)
	if err != nil || u.Bytes != 10 {
		t.Fatalf("first: %+v %v", u, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), make([]byte, 5), 0o644); err != nil {
		t.Fatal(err)
	}
	if u, _ = c.get(dir); u.Bytes != 10 {
		t.Fatalf("within the TTL the cached value (10) must be served, got %d", u.Bytes)
	}
	time.Sleep(60 * time.Millisecond)
	if u, _ = c.get(dir); u.Bytes != 15 {
		t.Fatalf("after the TTL the folder is measured again (15), got %d", u.Bytes)
	}
	// A different directory never gets another directory's cached value.
	other := t.TempDir()
	if u, _ = c.get(other); u.Bytes != 0 {
		t.Fatalf("other dir: %d", u.Bytes)
	}
}
