package server

// Searcher cache with reference counting.
//
// Handlers borrow a cached *gleann.LeannSearcher with acquireSearcher and
// must call the returned release func when done. Invalidation (index
// delete/update, config change, shutdown) removes the searcher from the
// cache immediately, but Close() is deferred until the last borrower
// releases it, so in-flight searches never run against a closed index.
//
// Loads run outside s.mu, deduplicated per index name with singleflight, on
// a context detached from the request: one slow load does not block other
// indexes, and a disconnecting client does not abort a load other requests
// are waiting on.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tevfik/gleann/pkg/gleann"
)

// searcherLoadTimeout bounds a single index load.
const searcherLoadTimeout = 10 * time.Minute

// searcherRef tracks borrowers of a cached searcher. Guarded by Server.mu.
type searcherRef struct {
	refs    int
	retired bool // removed from the cache; close when refs drops to 0
}

// acquireSearcher returns the cached searcher for name, loading it if
// needed, together with a release func that must be called exactly once
// when the caller is done with the searcher (extra calls are no-ops).
func (s *Server) acquireSearcher(ctx context.Context, name string) (*gleann.LeannSearcher, func(), error) {
	if err := gleann.ValidateName(name); err != nil {
		return nil, nil, err
	}

	// A load may complete just after a concurrent invalidation, in which
	// case it is discarded and we retry; bound the retries.
	for attempt := 0; attempt < 3; attempt++ {
		s.mu.Lock()
		if sr, ok := s.searchers[name]; ok && sr != nil {
			release := s.retainLocked(sr)
			s.mu.Unlock()
			return sr, release, nil
		}
		s.mu.Unlock()

		ch := s.loadGroup.DoChan(name, func() (any, error) {
			return nil, s.loadSearcher(name)
		})
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case res := <-ch:
			if res.Err != nil {
				return nil, nil, res.Err
			}
		}
	}
	return nil, nil, fmt.Errorf("index %q was invalidated repeatedly while loading; retry", name)
}

// loadSearcher loads the index and, unless the cache was invalidated while
// loading, inserts it into s.searchers. Called via singleflight.
func (s *Server) loadSearcher(name string) error {
	s.mu.RLock()
	gen := s.searcherGen
	s.mu.RUnlock()

	cfg, emb := s.cfgAndEmbedder()

	// Check model compatibility if dynamic resolution is not configured.
	if meta, err := gleann.GetIndexMeta(cfg.IndexDir, name); err == nil {
		if cfg.EmbedderResolver == nil && meta.EmbeddingModel != "" && emb != nil && meta.EmbeddingModel != emb.ModelName() {
			return fmt.Errorf("model mismatch: index uses '%s', current is '%s'. please delete and recreate index", meta.EmbeddingModel, emb.ModelName())
		}
	}

	ctx, cancel := context.WithTimeout(s.lifetime(), searcherLoadTimeout)
	defer cancel()

	searcher := gleann.NewSearcher(cfg, emb)
	if err := searcher.Load(ctx, name); err != nil {
		return err
	}

	s.mu.Lock()
	if s.searcherGen != gen {
		// Invalidated (config change, delete, update...) while loading:
		// the result may be stale, drop it.
		s.mu.Unlock()
		searcher.Close()
		return nil
	}
	if s.searchers == nil {
		s.searchers = make(map[string]*gleann.LeannSearcher)
	}
	if existing, ok := s.searchers[name]; ok && existing != nil {
		s.mu.Unlock()
		searcher.Close()
		return nil
	}
	s.searchers[name] = searcher
	s.mu.Unlock()
	return nil
}

// retainLocked increments the borrow count of sr. Caller holds s.mu.
func (s *Server) retainLocked(sr *gleann.LeannSearcher) func() {
	if s.searcherRefs == nil {
		s.searcherRefs = make(map[*gleann.LeannSearcher]*searcherRef)
	}
	ref := s.searcherRefs[sr]
	if ref == nil {
		ref = &searcherRef{}
		s.searcherRefs[sr] = ref
	}
	ref.refs++

	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			ref.refs--
			closeNow := ref.retired && ref.refs <= 0
			if ref.refs <= 0 {
				delete(s.searcherRefs, sr)
			}
			s.mu.Unlock()
			if closeNow {
				sr.Close()
			}
		})
	}
}

// retireLocked removes name from the cache. It returns the searcher if it
// can be closed right away (no borrowers); otherwise it is marked retired
// and closed by the last release. Caller holds s.mu and must Close the
// returned searcher after unlocking.
func (s *Server) retireLocked(name string) *gleann.LeannSearcher {
	sr, ok := s.searchers[name]
	delete(s.searchers, name)
	if !ok || sr == nil {
		return nil
	}
	if ref := s.searcherRefs[sr]; ref != nil && ref.refs > 0 {
		ref.retired = true
		return nil
	}
	delete(s.searcherRefs, sr)
	return sr
}

// invalidateSearcher drops the cached searcher for name (closing it once
// no request uses it) and discards any load of it currently in flight.
func (s *Server) invalidateSearcher(name string) {
	s.mu.Lock()
	s.searcherGen++
	sr := s.retireLocked(name)
	s.mu.Unlock()
	if sr != nil {
		sr.Close()
	}
}

// invalidateAllSearchers drops every cached searcher (see invalidateSearcher).
func (s *Server) invalidateAllSearchers() {
	s.mu.Lock()
	s.searcherGen++
	var toClose []*gleann.LeannSearcher
	for name := range s.searchers {
		if sr := s.retireLocked(name); sr != nil {
			toClose = append(toClose, sr)
		}
	}
	s.mu.Unlock()
	for _, sr := range toClose {
		sr.Close()
	}
}

// cachedSearcherCount returns the number of cached searchers.
func (s *Server) cachedSearcherCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.searchers)
}
