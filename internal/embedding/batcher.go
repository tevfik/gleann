package embedding

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Batcher coalesces multiple single-ID embedding requests into optimal GPU batches
// to overcome CGO/Inference latency and saturate the backend model.
type Batcher struct {
	server   *Server
	interval time.Duration
	maxBatch int

	reqCh  chan batchedRequest
	stopCh chan struct{}
	wg     sync.WaitGroup
}

type batchedRequest struct {
	ids    []int64
	respCh chan []float32
	errCh  chan error
}

// NewBatcher creates a new dynamic batcher over the embedding server.
func NewBatcher(server *Server, interval time.Duration, maxBatch int) *Batcher {
	if interval <= 0 {
		interval = 5 * time.Millisecond
	}
	if maxBatch <= 0 {
		maxBatch = 32
	}
	return &Batcher{
		server:   server,
		interval: interval,
		maxBatch: maxBatch,
		reqCh:    make(chan batchedRequest, maxBatch*10),
		stopCh:   make(chan struct{}),
	}
}

// Start begins the background batching loop.
func (b *Batcher) Start(ctx context.Context) {
	b.wg.Add(1)
	go b.loop(ctx)
}

// Stop gracefully shuts down the batcher.
func (b *Batcher) Stop() {
	close(b.stopCh)
	b.wg.Wait()
}

// Recompute is the interface function to be used by the Graph searchers.
// Searchers push one ID (or a few) at a time, but instead of blocking a whole CGO call,
// it waits in the batcher queue until flushed.
func (b *Batcher) Recompute(ctx context.Context, ids []int64) ([][]float32, error) {
	resps := make([][]float32, len(ids))

	// We handle them one by one for returning, but they are batched internally.
	// For actual Graph traversal, usually len(ids) == 1 anyway.
	var wg sync.WaitGroup
	var globalErr error
	var mu sync.Mutex

	for i, id := range ids {
		wg.Add(1)
		go func(idx int, targetID int64) {
			defer wg.Done()

			respCh := make(chan []float32, 1)
			errCh := make(chan error, 1)

			req := batchedRequest{
				ids:    []int64{targetID},
				respCh: respCh,
				errCh:  errCh,
			}

			select {
			case b.reqCh <- req:
			case <-ctx.Done():
				mu.Lock()
				globalErr = ctx.Err()
				mu.Unlock()
				return
			case <-b.stopCh:
				mu.Lock()
				globalErr = fmt.Errorf("batcher stopped")
				mu.Unlock()
				return
			}

			select {
			case r := <-respCh:
				mu.Lock()
				resps[idx] = r
				mu.Unlock()
			case err := <-errCh:
				mu.Lock()
				if globalErr == nil {
					globalErr = err
				}
				mu.Unlock()
			case <-ctx.Done():
				mu.Lock()
				globalErr = ctx.Err()
				mu.Unlock()
			case <-b.stopCh:
				mu.Lock()
				globalErr = fmt.Errorf("batcher stopped")
				mu.Unlock()
			}
		}(i, id)
	}

	wg.Wait()
	if globalErr != nil {
		return nil, globalErr
	}

	return resps, nil
}

func (b *Batcher) loop(ctx context.Context) {
	defer b.wg.Done()

	timer := time.NewTimer(b.interval)
	if !timer.Stop() {
		<-timer.C
	}

	var currentBatch []batchedRequest

	flush := func() {
		if len(currentBatch) == 0 {
			return
		}

		// Extract all IDs
		ids := make([]int64, 0, len(currentBatch))
		for _, req := range currentBatch {
			ids = append(ids, req.ids...)
		}

		// Send batch to underlying server (this does the Heavy CGO/GPU work in ONE call)
		embeddings, err := b.server.ComputeEmbeddings(ctx, ids)

		// Dispatch results back
		for i, req := range currentBatch {
			if err != nil {
				req.errCh <- err
			} else {
				// Each request in our setup is exactly 1 ID
				req.respCh <- embeddings[i]
			}
		}

		currentBatch = nil
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stopCh:
			return
		case req := <-b.reqCh:
			currentBatch = append(currentBatch, req)

			if len(currentBatch) == 1 {
				timer.Reset(b.interval)
			}

			if len(currentBatch) >= b.maxBatch {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				flush()
			}
		case <-timer.C:
			flush()
		}
	}
}

// RecomputerAdapter returns a gleann.EmbeddingRecomputer using the batcher.
func (b *Batcher) RecomputerAdapter() gleann.EmbeddingRecomputer {
	return func(ctx context.Context, ids []int64) ([][]float32, error) {
		return b.Recompute(ctx, ids)
	}
}
