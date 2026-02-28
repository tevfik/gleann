package embedding

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Server provides an in-process embedding computation service.
// This replaces Python LEANN's ZMQ-based subprocess server with
// goroutines and channels for zero-overhead communication.
type Server struct {
	computer *Computer
	passages *gleann.PassageManager

	// Request/response channels.
	reqCh  chan embeddingRequest
	stopCh chan struct{}
	wg     sync.WaitGroup

	running atomic.Bool
	workers int
}

type embeddingRequest struct {
	ids    []int64
	respCh chan embeddingResponse
}

type embeddingResponse struct {
	embeddings [][]float32
	err        error
}

// ServerOptions configures the embedding server.
type ServerOptions struct {
	Computer *Computer
	Passages *gleann.PassageManager
	Workers  int // Number of concurrent workers (default: 4).
}

// NewServer creates a new in-process embedding server.
func NewServer(opts ServerOptions) *Server {
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	return &Server{
		computer: opts.Computer,
		passages: opts.Passages,
		reqCh:    make(chan embeddingRequest, opts.Workers*2),
		stopCh:   make(chan struct{}),
		workers:  opts.Workers,
	}
}

// Start starts the embedding server workers.
func (s *Server) Start(ctx context.Context) error {
	if s.running.Load() {
		return nil
	}

	s.running.Store(true)

	// Launch worker goroutines.
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker(ctx)
	}

	return nil
}

// Stop stops the embedding server.
func (s *Server) Stop() error {
	if !s.running.Load() {
		return nil
	}

	s.running.Store(false)
	close(s.stopCh)
	s.wg.Wait()
	return nil
}

// ComputeEmbeddings requests embedding computation for the given passage IDs.
func (s *Server) ComputeEmbeddings(ctx context.Context, ids []int64) ([][]float32, error) {
	if !s.running.Load() {
		return nil, fmt.Errorf("server not running")
	}

	respCh := make(chan embeddingResponse, 1)
	req := embeddingRequest{
		ids:    ids,
		respCh: respCh,
	}

	select {
	case s.reqCh <- req:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.stopCh:
		return nil, fmt.Errorf("server stopped")
	}

	select {
	case resp := <-respCh:
		return resp.embeddings, resp.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.stopCh:
		return nil, fmt.Errorf("server stopped")
	}
}

// IsRunning returns whether the server is running.
func (s *Server) IsRunning() bool {
	return s.running.Load()
}

// worker processes embedding requests.
func (s *Server) worker(ctx context.Context) {
	defer s.wg.Done()

	for {
		select {
		case req, ok := <-s.reqCh:
			if !ok {
				return
			}
			embeddings, err := s.processRequest(ctx, req.ids)
			req.respCh <- embeddingResponse{
				embeddings: embeddings,
				err:        err,
			}
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// processRequest fetches passages and computes their embeddings.
func (s *Server) processRequest(ctx context.Context, ids []int64) ([][]float32, error) {
	// Get passage texts.
	texts, err := s.passages.GetTexts(ids)
	if err != nil {
		return nil, fmt.Errorf("get passage texts: %w", err)
	}

	// Compute embeddings.
	embeddings, err := s.computer.Compute(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("compute embeddings: %w", err)
	}

	return embeddings, nil
}

// Recomputer returns an EmbeddingRecomputer function that uses this server.
// This is the bridge between the HNSW search and the embedding server.
func (s *Server) Recomputer() gleann.EmbeddingRecomputer {
	return func(ctx context.Context, ids []int64) ([][]float32, error) {
		return s.ComputeEmbeddings(ctx, ids)
	}
}
