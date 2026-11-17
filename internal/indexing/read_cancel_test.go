package indexing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/multimodal"
)

// A code file of 2 KiB or more is not sniffed by the collector in code mode,
// so a binary payload behind a .go extension reaches the reader, which skips
// it by its NUL bytes.
func TestReadDocumentsSkipsLargeBinaryInCodeMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "blob.go"), "package x\x00"+strings.Repeat("A", 4096))
	writeFile(t, filepath.Join(dir, "ok.go"), "package x\n\nfunc Ok() {}\n")
	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{ChunkSize: 256, NoPlugins: true, Mode: ModeCode})
	if err != nil {
		t.Fatal(err)
	}
	got := bySource(items)
	if _, has := got["blob.go"]; has {
		t.Errorf("binary payload produced passages: %v", got)
	}
	if got["ok.go"] == "" {
		t.Errorf("text file lost: %v", got)
	}
}

// visionChat serves /api/show and /api/chat for a vision model; chat decides
// the answer.
func visionChat(t *testing.T, chat func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	t.Setenv("GLEANN_MULTIMODAL_CACHE_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/show":
			_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]any{"families": []string{"clip"}}})
		case "/api/chat":
			chat(w)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func chatReply(w http.ResponseWriter, content string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"content": content}})
}

// A vision model that answers with only whitespace describes nothing: the
// image is recorded but produces no passage.
func TestReadDocumentsBlankImageDescription(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := visionChat(t, func(w http.ResponseWriter) { chatReply(w, "  \n") })
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "blank.png"), "\x89PNG\r\n\x1a\nblank image")
	mm := &multimodal.Processor{OllamaHost: srv.URL, Model: "vlm-blank", Lang: "en"}
	items, _, err := ReadDocuments(context.Background(), dir, ReadOptions{Multimodal: mm, NoPlugins: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("blank description produced passages: %v", bySource(items))
	}
}

// Cancelling while the read is in flight drains the queued files without
// doing their work and returns the context error.
func TestReadDocumentsCancelledMidRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	srv := visionChat(t, func(w http.ResponseWriter) {
		once.Do(cancel)
		time.Sleep(20 * time.Millisecond)
		chatReply(w, "an image")
	})
	dir := t.TempDir()
	// More images than workers, so some are still queued when the first
	// description request cancels the context.
	for i := 0; i < workerCount()+24; i++ {
		writeFile(t, filepath.Join(dir, fmt.Sprintf("img%02d.png", i)), fmt.Sprintf("\x89PNG\r\n\x1a\nimage number %d", i))
	}
	mm := &multimodal.Processor{OllamaHost: srv.URL, Model: "vlm-cancel", Lang: "en"}
	items, docs, err := ReadDocuments(ctx, dir, ReadOptions{Multimodal: mm, NoPlugins: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if items != nil || docs != nil {
		t.Errorf("cancelled read returned results: %d items, %d docs", len(items), len(docs))
	}
}
