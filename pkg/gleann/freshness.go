package gleann

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/store"
)

// ErrIndexChanged is returned by Search when another process published a new
// main vector file while the search ran: the passages the query resolved may
// belong to a different vector file than the one this searcher holds, so the
// result is discarded instead of returning a wrong passage. The caller reloads
// the searcher (the server and the MCP server do this on acquire) and retries.
var ErrIndexChanged = errors.New("index changed while searching; reload the index and retry")

// indexFreshness is what a format-2 searcher recorded about the database when
// it loaded, so a later small read can tell whether another process (a CLI
// sync, a build, watch in another binary) wrote to the index since.
//
//   - vector is the published vector generation plus the pending-publication
//     keys. Every publication moves it (pending, then generation+1), and the
//     generation never decreases. A change of only this part means the delta
//     file and the live/dead slot counts changed: the main vector file is
//     untouched, so a searcher refreshes its delta in place (RFC 005
//     section 5).
//   - main is vector_main_generation, which changes only when the main vector
//     file is replaced (a merge, a compaction, a full build, an emptied
//     index). Slots may then have been renumbered, so the searcher must load
//     the new file; a result computed against the old one is discarded. This
//     is the part Search re-checks after resolving passages.
//   - graph is the graph row counters and the document-graph presence. The
//     graph handle reads the live tables, so it never serves stale rows; what
//     can go stale is the decision made at Load whether the index has a graph
//     at all, and that decision follows these values.
type indexFreshness struct {
	vector string
	main   string
	graph  string
}

var graphFreshnessKeys = []string{
	store.MetaGraphSymbols,
	store.MetaGraphEdgesPrefix + "CALLS",
	store.MetaGraphEdgesPrefix + "IMPLEMENTS",
	store.MetaGraphEdgesPrefix + "REFERENCES",
}

var freshnessKeys = append([]string{
	"vector_generation", "pending_vector_generation", "pending_vector_file", metaVectorMainGeneration,
}, graphFreshnessKeys...)

// freshness reads the current state: one meta read plus one indexed probe.
func (sp *storePassages) freshness(ctx context.Context) (indexFreshness, error) {
	m, err := sp.st.MetaSnapshot(ctx, freshnessKeys...)
	if err != nil {
		return indexFreshness{}, err
	}
	docs, err := sp.st.HasDocNodes(ctx)
	if err != nil {
		return indexFreshness{}, err
	}
	var g strings.Builder
	for _, k := range graphFreshnessKeys {
		fmt.Fprintf(&g, "%s=%s;", k, m[k])
	}
	fmt.Fprintf(&g, "docs=%t", docs)
	return indexFreshness{
		vector: fmt.Sprintf("gen=%s;pending=%s@%s", m["vector_generation"], m["pending_vector_generation"], m["pending_vector_file"]),
		main:   m[metaVectorMainGeneration],
		graph:  g.String(),
	}, nil
}

func (s *LeannSearcher) loadFresh() (indexFreshness, bool) {
	s.freshMu.Lock()
	defer s.freshMu.Unlock()
	return s.fresh, s.freshKnown
}

func (s *LeannSearcher) storeFresh(f indexFreshness, known bool) {
	s.freshMu.Lock()
	s.fresh, s.freshKnown = f, known
	s.freshMu.Unlock()
}

// recordFreshness stores the state the searcher is about to load. It runs
// right after the publication recovery and before the vector files are read:
// a publication that lands later makes the recorded state differ, so it is
// detected, never missed. A publication that is pending at this moment (a
// writer committed it between the open's recovery and this read) is rolled
// forward first, so the recorded state always describes files that are in
// place.
func (s *LeannSearcher) recordFreshness(ctx context.Context) error {
	s.storeFresh(indexFreshness{}, false)
	sp, ok := s.passages.(*storePassages)
	if !ok || sp.st == nil {
		return nil
	}
	for attempt := 0; ; attempt++ {
		f, err := sp.freshness(ctx)
		if err != nil {
			return fmt.Errorf("recording index freshness: %w", err)
		}
		pending, err := sp.st.Meta(ctx, "pending_vector_file")
		if err != nil {
			return fmt.Errorf("recording index freshness: %w", err)
		}
		if pending == "" || attempt >= 3 {
			s.storeFresh(f, true)
			return nil
		}
		if err := recoverVectorPublication(ctx, sp.st, sp.basePath); err != nil {
			return fmt.Errorf("recovering vector publication: %w", err)
		}
	}
}

// IndexChange says what, if anything, changed in the index on disk since a
// searcher loaded it.
type IndexChange int

const (
	// IndexUnchanged: nothing a search depends on changed.
	IndexUnchanged IndexChange = iota
	// IndexDeltaChanged: only the delta file and the slot counts changed (an
	// incremental sync). RefreshDelta brings the searcher up to date without
	// touching the main vector file.
	IndexDeltaChanged
	// IndexFullReload: the main vector file was replaced (slots may have been
	// renumbered) or the graph decision changed; the searcher must be loaded
	// again.
	IndexFullReload
)

// Change reports what changed in the index on disk since this searcher loaded
// it. It costs one small database read and no file walk. A searcher that did
// not record a state (format 1, a custom or unloaded searcher) never changes.
// A read failure (the database is gone or unreadable) is reported as a full
// reload together with the error, so the caller's reload surfaces the real
// problem.
func (s *LeannSearcher) Change(ctx context.Context) (IndexChange, error) {
	cur, ok, err := s.currentFreshness(ctx)
	if !ok {
		return IndexUnchanged, nil
	}
	if err != nil {
		return IndexFullReload, err
	}
	rec, _ := s.loadFresh()
	switch {
	case cur.main != rec.main || cur.graph != rec.graph:
		return IndexFullReload, nil
	case cur.vector != rec.vector:
		return IndexDeltaChanged, nil
	}
	return IndexUnchanged, nil
}

// Stale reports whether the index on disk changed since this searcher loaded
// it in any way a search could notice (see Change). Callers that cache
// searchers across requests (the server, the MCP server) use Change to tell a
// delta-only change, which RefreshDelta absorbs in place, from one that needs
// a new searcher; a long-lived single holder (the chat) uses Refresh.
func (s *LeannSearcher) Stale(ctx context.Context) (bool, error) {
	c, err := s.Change(ctx)
	return c != IndexUnchanged || err != nil, err
}

// mainChanged is the Search-time check: only a replacement of the main vector
// file invalidates a result in flight. A delta-only publication never reuses
// a slot, so a hit of the older state still names the right passage.
func (s *LeannSearcher) mainChanged(ctx context.Context) (bool, error) {
	cur, ok, err := s.currentFreshness(ctx)
	if !ok {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	rec, _ := s.loadFresh()
	return cur.main != rec.main, nil
}

// currentFreshness reads the database state when this searcher recorded one.
func (s *LeannSearcher) currentFreshness(ctx context.Context) (indexFreshness, bool, error) {
	if s == nil {
		return indexFreshness{}, false, nil
	}
	if _, known := s.loadFresh(); !known {
		return indexFreshness{}, false, nil
	}
	sp, ok := s.passages.(*storePassages)
	if !ok || sp.st == nil {
		return indexFreshness{}, false, nil
	}
	cur, err := sp.freshness(ctx)
	return cur, true, err
}

// errNeedsFullReload is returned by RefreshDelta when the main vector file
// changed too: the caller loads the index again.
var errNeedsFullReload = errors.New("the main vector file changed; reload the index")

// RefreshDelta reloads the delta file and the live/dead slot counts of a
// searcher whose main vector file is still current (Change reported
// IndexDeltaChanged). It is safe while other goroutines search with the same
// searcher: they see the old delta or the new one, never a mix. It follows
// load-then-verify: the state is recorded, the delta read, and the state read
// again; a publication that lands in between repeats the load. It returns
// errNeedsFullReload when the main file changed meanwhile.
func (s *LeannSearcher) RefreshDelta(ctx context.Context) error {
	sp, ok := s.passages.(*storePassages)
	if !ok || sp.st == nil {
		return errNeedsFullReload
	}
	basePath := filepath.Join(s.config.IndexDir, s.name, s.name)
	loaded, _ := s.loadFresh()
	for attempt := 0; attempt < 4; attempt++ {
		// A publication pending now is rolled forward, so the files read below
		// are the ones the recorded state describes.
		if err := recoverVectorPublication(ctx, sp.st, basePath); err != nil {
			return fmt.Errorf("recovering vector publication: %w", err)
		}
		rec, err := sp.freshness(ctx)
		if err != nil {
			return fmt.Errorf("recording index freshness: %w", err)
		}
		if rec.main != loaded.main || rec.graph != loaded.graph {
			return errNeedsFullReload
		}
		ds, err := s.loadDeltaState(ctx, basePath)
		if err != nil {
			return err
		}
		cur, err := sp.freshness(ctx)
		if err != nil {
			return fmt.Errorf("verifying index freshness: %w", err)
		}
		if cur.vector == rec.vector && cur.main == rec.main {
			s.dstate.Store(ds)
			s.storeFresh(rec, true)
			return nil
		}
	}
	return ErrIndexChanged
}

// Refresh brings the searcher up to date when the index changed on disk (see
// Change) and reports whether it did: a delta-only change reloads the delta
// in place, anything else reloads the searcher. It is for a single holder that
// issues one request at a time, such as the chat session: a full reload is NOT
// safe while another goroutine is searching with the same searcher. Servers
// replace a stale searcher instead, so in-flight requests finish on the old
// one.
func (s *LeannSearcher) Refresh(ctx context.Context) (bool, error) {
	change, serr := s.Change(ctx)
	if change == IndexUnchanged && serr == nil {
		return false, nil
	}
	if change == IndexDeltaChanged && serr == nil {
		if err := s.RefreshDelta(ctx); err == nil {
			return true, nil
		}
		// The main file changed meanwhile (or the delta could not be read):
		// load everything again below.
	}
	name := s.name
	if name == "" {
		return false, serr
	}
	_ = s.Close()
	s.loaded, s.vectorAvailable = false, false
	s.backend, s.passages = nil, nil
	s.dstate.Store(nil)
	// A reload must not stack the new passages on the previous in-memory
	// BM25 documents.
	if _, ok := s.scorer.(*BM25Adapter); ok {
		s.scorer = NewBM25Adapter()
	}
	if err := s.Load(ctx, name); err != nil {
		return false, fmt.Errorf("reloading index %q after it changed: %w", name, err)
	}
	return true, nil
}

// searchFresh is the chat's retrieval: it first reloads the searcher when the
// index changed on disk since the session loaded it (a sync in another
// terminal), and retries once when a publication lands during the query.
func (c *LeannChat) searchFresh(ctx context.Context, question string, opts ...SearchOption) ([]SearchResult, error) {
	ls, refreshable := c.searcher.(*LeannSearcher)
	if refreshable {
		if _, err := ls.Refresh(ctx); err != nil {
			return nil, err
		}
	}
	results, err := c.searcher.Search(ctx, question, opts...)
	if refreshable && errors.Is(err, ErrIndexChanged) {
		if _, rerr := ls.Refresh(ctx); rerr != nil {
			return nil, rerr
		}
		return c.searcher.Search(ctx, question, opts...)
	}
	return results, err
}
