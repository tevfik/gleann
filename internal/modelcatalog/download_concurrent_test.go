package modelcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two downloads of one model (serve and a CLI command) run at the same time,
// both mid-stream together. They must end with exactly one valid file and no
// temp files, whichever finishes first.
func TestConcurrentDownloadsEndWithOneValidFile(t *testing.T) {
	isolate(t)
	payload := []byte(strings.Repeat("0123456789abcdef", 4096))
	h := sha256.Sum256(payload)

	var arrived sync.WaitGroup
	arrived.Add(2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		half := len(payload) / 2
		_, _ = w.Write(payload[:half])
		w.(http.Flusher).Flush()
		arrived.Done()
		arrived.Wait() // both downloads have started writing
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write(payload[half:])
	}))
	defer srv.Close()
	e := Entry{ID: "c", Name: "c", File: "c.gguf", URL: srv.URL, Bytes: int64(len(payload)), SHA256: hex.EncodeToString(h[:])}

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errs <- Download(context.Background(), e, nil) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Errorf("download %d: %v", i, err)
		}
	}
	got, err := os.ReadFile(e.Path())
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(got); sum != h {
		t.Fatal("installed model is corrupt (checksum differs)")
	}
	entries, _ := os.ReadDir(Dir())
	if len(entries) != 1 {
		var names []string
		for _, f := range entries {
			names = append(names, f.Name())
		}
		t.Errorf("models dir has %v, want only c.gguf", names)
	}
}

// A model that appears with the right checksum while this download runs is
// kept; this download's copy is discarded.
func TestDownloadKeepsModelInstalledMeanwhile(t *testing.T) {
	isolate(t)
	payload := []byte(strings.Repeat("ok", 2048))
	h := sha256.Sum256(payload)
	e := Entry{ID: "m", Name: "m", File: "m.gguf", Bytes: int64(len(payload)), SHA256: hex.EncodeToString(h[:])}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// the other process finishes first
		_ = os.WriteFile(e.Path(), payload, 0o644)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	e.URL = srv.URL
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Download(context.Background(), e, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(e.Path()); string(got) != string(payload) {
		t.Fatal("installed model changed")
	}
	if m, _ := filepath.Glob(filepath.Join(Dir(), "*.part")); len(m) != 0 {
		t.Errorf("temp files left: %v", m)
	}
}
