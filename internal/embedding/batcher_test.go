package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

type mockTrackingServer struct {
	mu           sync.Mutex
	computeCalls int
	batchSizes   []int
	server       *httptest.Server
}

func newMockTrackingServer(t *testing.T) *mockTrackingServer {
	t.Helper()
	ms := &mockTrackingServer{}
	ms.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaEmbedRequest
		json.NewDecoder(r.Body).Decode(&req)

		var count int
		switch v := req.Input.(type) {
		case []interface{}:
			count = len(v)
		default:
			count = 1
		}

		ms.mu.Lock()
		ms.computeCalls++
		ms.batchSizes = append(ms.batchSizes, count)
		ms.mu.Unlock()

		embeddings := make([][]float32, count)
		for i := range embeddings {
			// Return dummy embedding of size 1 for tests.
			embeddings[i] = []float32{float32(count)}
		}

		// Simulate latency
		time.Sleep(10 * time.Millisecond)

		json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: embeddings})
	}))
	return ms
}

func createMockServerEnv(t *testing.T) (*Server, *mockTrackingServer) {
	tracker := newMockTrackingServer(t)

	comp := NewComputer(Options{
		Provider: ProviderOllama,
		BaseURL:  tracker.server.URL,
	})

	dir := t.TempDir()
	pm := gleann.NewPassageManager(dir + "/test")
	t.Cleanup(func() { pm.Close() })

	items := make([]gleann.Item, 100)
	for i := 0; i < 100; i++ {
		items[i] = gleann.Item{Text: "mock", Metadata: map[string]any{}}
	}
	pm.Add(items)

	opts := ServerOptions{
		Computer: comp,
		Passages: pm,
	}
	s := NewServer(opts)

	return s, tracker
}

func TestBatcher(t *testing.T) {
	ctx := context.Background()

	server, tracker := createMockServerEnv(t)
	defer tracker.server.Close()
	server.Start(ctx)
	defer server.Stop()

	// 10ms flush interval, max 5 items
	batcher := NewBatcher(server, 10*time.Millisecond, 5)
	batcher.Start(ctx)
	defer batcher.Stop()

	var wg sync.WaitGroup
	results := make([]float32, 12)

	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			vecs, err := batcher.Recompute(ctx, []int64{id})
			if err != nil {
				t.Errorf("request failed: %v", err)
				return
			}
			if len(vecs) != 1 {
				t.Errorf("expected 1 vector, got %d", len(vecs))
				return
			}
			results[id] = vecs[0][0]
		}(int64(i))
	}

	wg.Wait()

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	// 12 requests with maxBatch=5 means we should have approx 3 batches
	if tracker.computeCalls > 3 {
		t.Errorf("expected max 3 compute calls due to batching, got %d. Batches: %v", tracker.computeCalls, tracker.batchSizes)
	}

	totalItems := 0
	for _, s := range tracker.batchSizes {
		if s > 5 {
			t.Errorf("batch size %d exceeded maxBatch 5", s)
		}
		totalItems += s
	}

	if totalItems != 12 {
		t.Errorf("expected 12 total items computed, got %d", totalItems)
	}
}

func TestBatcherTimeoutFlush(t *testing.T) {
	ctx := context.Background()
	server, tracker := createMockServerEnv(t)
	defer tracker.server.Close()
	server.Start(ctx)
	defer server.Stop()

	// 20ms flush interval, max 50 items
	batcher := NewBatcher(server, 20*time.Millisecond, 50)
	batcher.Start(ctx)
	defer batcher.Stop()

	start := time.Now()
	_, err := batcher.Recompute(ctx, []int64{1})
	dur := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}

	if dur < 20*time.Millisecond {
		t.Errorf("expected at least 20ms duration for timer flush, got %v", dur)
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	if tracker.computeCalls != 1 {
		t.Errorf("expected 1 compute call, got %d", tracker.computeCalls)
	}
}

func TestBatcherCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server, tracker := createMockServerEnv(t)
	defer tracker.server.Close()
	server.Start(ctx)
	defer server.Stop()

	batcher := NewBatcher(server, 100*time.Millisecond, 5)
	batcher.Start(ctx)
	defer batcher.Stop()

	cancel() // Cancel immediately

	_, err := batcher.Recompute(ctx, []int64{1})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
