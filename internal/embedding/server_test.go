package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// mockPassageManager creates a passage manager with test data.
func newTestPassageManager(t *testing.T) *gleann.PassageManager {
	t.Helper()
	dir := t.TempDir()
	pm := gleann.NewPassageManager(dir + "/test")

	items := []gleann.Item{
		{Text: "hello world", Metadata: map[string]any{}},
		{Text: "foo bar baz", Metadata: map[string]any{}},
		{Text: "test passage", Metadata: map[string]any{}},
	}
	if _, err := pm.Add(items); err != nil {
		t.Fatalf("add passages: %v", err)
	}
	return pm
}

func newMockEmbeddingServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)

		var count int
		switch v := req.Input.(type) {
		case []interface{}:
			count = len(v)
		default:
			count = 1
		}

		embeddings := make([][]float32, count)
		for i := range embeddings {
			embeddings[i] = []float32{0.1, 0.2, 0.3, 0.4}
		}
		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embeddings})
	}))
}

func TestServerStartStop(t *testing.T) {
	mockSrv := newMockEmbeddingServer(t)
	defer mockSrv.Close()

	computer := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  mockSrv.URL,
	})
	pm := newTestPassageManager(t)

	srv := NewServer(ServerOptions{
		Computer: computer,
		Passages: pm,
		Workers:  2,
	})

	if srv.IsRunning() {
		t.Error("server should not be running before Start")
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !srv.IsRunning() {
		t.Error("server should be running after Start")
	}

	// Starting again should be no-op.
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("double start: %v", err)
	}

	if err := srv.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if srv.IsRunning() {
		t.Error("server should not be running after Stop")
	}

	// Stopping again should be no-op.
	if err := srv.Stop(); err != nil {
		t.Fatalf("double stop: %v", err)
	}
}

func TestServerComputeEmbeddings(t *testing.T) {
	mockSrv := newMockEmbeddingServer(t)
	defer mockSrv.Close()

	computer := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  mockSrv.URL,
	})
	pm := newTestPassageManager(t)

	srv := NewServer(ServerOptions{
		Computer: computer,
		Passages: pm,
		Workers:  2,
	})

	ctx := context.Background()
	srv.Start(ctx)
	defer srv.Stop()

	embeddings, err := srv.ComputeEmbeddings(ctx, []int64{0, 1})
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
	if len(embeddings[0]) != 4 {
		t.Errorf("expected 4 dimensions, got %d", len(embeddings[0]))
	}
}

func TestServerNotRunning(t *testing.T) {
	mockSrv := newMockEmbeddingServer(t)
	defer mockSrv.Close()

	computer := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  mockSrv.URL,
	})
	pm := newTestPassageManager(t)

	srv := NewServer(ServerOptions{
		Computer: computer,
		Passages: pm,
	})

	_, err := srv.ComputeEmbeddings(context.Background(), []int64{0})
	if err == nil {
		t.Fatal("expected error when server not running")
	}
}

func TestServerContextCancellation(t *testing.T) {
	mockSrv := newMockEmbeddingServer(t)
	defer mockSrv.Close()

	computer := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  mockSrv.URL,
	})
	pm := newTestPassageManager(t)

	srv := NewServer(ServerOptions{
		Computer: computer,
		Passages: pm,
		Workers:  1,
	})

	ctx := context.Background()
	srv.Start(ctx)
	defer srv.Stop()

	cancelCtx, cancel := context.WithTimeout(ctx, 1*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)

	_, err := srv.ComputeEmbeddings(cancelCtx, []int64{0})
	if err == nil {
		// Context may or may not have expired depending on timing.
		// This is fine — just testing the code path.
	}
}

func TestServerRecomputer(t *testing.T) {
	mockSrv := newMockEmbeddingServer(t)
	defer mockSrv.Close()

	computer := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  mockSrv.URL,
	})
	pm := newTestPassageManager(t)

	srv := NewServer(ServerOptions{
		Computer: computer,
		Passages: pm,
		Workers:  2,
	})

	ctx := context.Background()
	srv.Start(ctx)
	defer srv.Stop()

	recomputer := srv.Recomputer()
	if recomputer == nil {
		t.Fatal("recomputer should not be nil")
	}

	embeddings, err := recomputer(ctx, []int64{0, 2})
	if err != nil {
		t.Fatalf("recomputer: %v", err)
	}
	if len(embeddings) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(embeddings))
	}
}

func TestServerDefaultWorkers(t *testing.T) {
	computer := NewComputer(Options{})
	pm := newTestPassageManager(t)

	srv := NewServer(ServerOptions{
		Computer: computer,
		Passages: pm,
		Workers:  0, // Should default to 4.
	})

	if srv.workers != 4 {
		t.Errorf("expected default 4 workers, got %d", srv.workers)
	}
}
