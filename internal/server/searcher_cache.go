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
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
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
			s.touchSearcherLRULocked(name)
			release := s.retainLocked(sr)
			s.mu.Unlock()
			// Another process (a CLI sync or build) may have written to the
			// index since this searcher loaded: one small read decides. A
			// delta-only change (an incremental sync) is absorbed in place, the
			// main vector file stays loaded and concurrent searches are not
			// disturbed. Anything else retires the searcher, which is not
			// closed under its borrowers, and the loop loads a fresh one.
			change, serr := sr.Change(ctx)
			if change == gleann.IndexDeltaChanged && serr == nil {
				if rerr := sr.RefreshDelta(ctx); rerr == nil {
					return sr, release, nil
				} else {
					serr = rerr
				}
			}
			if change != gleann.IndexUnchanged || serr != nil {
				release()
				if serr != nil {
					log.Printf("ℹ️  Index %q freshness check failed (%v); reloading", name, serr)
				}
				s.retireIfCurrent(name, sr)
				continue
			}
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
	return nil, nil, fmt.Errorf("index %q was invalidated repeatedly while loading: %w", name, errSearcherBusy)
}

// errSearcherBusy: every load attempt of a searcher was invalidated by a
// concurrent change (config updates, index writes). The index exists; the
// request can be retried.
var errSearcherBusy = errors.New("the index changed during every load attempt; retry")

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
	s.touchSearcherLRULocked(name)
	toClose := s.enforceSearcherBudgetLocked()
	s.mu.Unlock()
	for _, sr := range toClose {
		sr.Close()
	}
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
	s.removeSearcherLRULocked(name)
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

func (s *Server) searcherCacheBudget() int64 {
	if env := os.Getenv("GLEANN_SEARCHER_CACHE_BUDGET"); env != "" {
		if b, err := gleann.ParseByteSize(env); err == nil {
			return b
		}
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.config.SearcherCacheBudget
}

func (s *Server) touchSearcherLRULocked(name string) {
	for i, n := range s.searcherLRU {
		if n == name {
			s.searcherLRU = append(s.searcherLRU[:i], s.searcherLRU[i+1:]...)
			break
		}
	}
	s.searcherLRU = append(s.searcherLRU, name)
}

func (s *Server) removeSearcherLRULocked(name string) {
	for i, n := range s.searcherLRU {
		if n == name {
			s.searcherLRU = append(s.searcherLRU[:i], s.searcherLRU[i+1:]...)
			return
		}
	}
}

// enforceSearcherBudgetLocked evicts least-recently-used searchers when total
// resident size exceeds the configured searcherCacheBudget. Caller holds s.mu.
// The returned searchers must be closed after unlocking s.mu.
func (s *Server) enforceSearcherBudgetLocked() []*gleann.LeannSearcher {
	budget := s.searcherCacheBudget()
	if budget <= 0 {
		return nil
	}
	totalSize := int64(0)
	for _, sr := range s.searchers {
		if sr != nil {
			totalSize += sr.EstimatedSize()
		}
	}
	if totalSize <= budget {
		return nil
	}

	var toClose []*gleann.LeannSearcher
	for len(s.searcherLRU) > 1 && totalSize > budget {
		oldest := s.searcherLRU[0]
		sr := s.searchers[oldest]
		size := int64(0)
		if sr != nil {
			size = sr.EstimatedSize()
		}
		closed := s.retireLocked(oldest)
		if closed != nil {
			toClose = append(toClose, closed)
		}
		totalSize -= size
	}
	return toClose
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

// retireIfCurrent retires the cached searcher for name only if it is still
// sr: a concurrent acquire may already have replaced it with a fresh one,
// which must stay. Unlike invalidateSearcher it does not bump the cache
// generation: a load that started before the change records the older state
// and is found stale by its first acquire, so it needs no discarding.
func (s *Server) retireIfCurrent(name string, sr *gleann.LeannSearcher) {
	s.mu.Lock()
	var toClose *gleann.LeannSearcher
	if cur, ok := s.searchers[name]; ok && cur == sr {
		toClose = s.retireLocked(name)
	}
	s.mu.Unlock()
	if toClose != nil {
		toClose.Close()
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

// writeSearcherError answers a failed acquireSearcher by class: an index that
// does not exist (not-exist, or a plain file where its directory should be) is
// 404; one in the old store format is a conflict the caller can resolve with
// `gleann index migrate` (409 with that hint); a load that kept being
// invalidated, or ran into the server's deadline, is 503 with Retry-After; a
// client that went away gets nothing worth sending (499, logged only);
// anything else (corrupt database, unreadable file, model mismatch) is a
// server-side 500 with a generic body, never "not found". The detail of a 500
// is logged, not sent: it carries file paths.
func writeSearcherError(w http.ResponseWriter, r *http.Request, name string, err error) {
	switch {
	case errors.Is(err, gleann.ErrOldStoreFormat):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, context.Canceled) && r.Context().Err() != nil:
		// The request's own context is done: the client went away.
		log.Printf("load index %q: client went away: %v", name, err)
		w.WriteHeader(statusClientClosedRequest)
	case errors.Is(err, errSearcherBusy), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		// A canceled load with a live client is the server shutting down (the
		// load runs on the server's lifetime and every waiter gets its error).
		// Transient, not "not found": the client should retry.
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("index %q is busy; retry", name))
	case indexMissing(err):
		writeError(w, http.StatusNotFound, fmt.Sprintf("index %q not found", name))
	default:
		log.Printf("load index %q: %v", name, err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("index %q could not be loaded", name))
	}
}

// statusClientClosedRequest is the nginx convention for a request the client
// abandoned; nobody reads it, it only keeps the log and metrics honest.
const statusClientClosedRequest = 499
