package gleann

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tevfik/gleann/internal/store"
)

// ErrIndexChanged is returned by Search when another process published a new
// vector generation while the search ran: the passages the query resolved may
// belong to a different vector file than the one this searcher holds, so the
// result is discarded instead of returning a wrong passage. The caller reloads
// the searcher (the server and the MCP server do this on acquire) and retries.
var ErrIndexChanged = errors.New("index changed while searching; reload the index and retry")

// indexFreshness is what a format-2 searcher recorded about the database when
// it loaded, so a later small read can tell whether another process (a CLI
// sync, a build, watch in another binary) wrote to the index since.
//
//   - vector is the published vector generation plus the pending-publication
//     keys. A publication commits the renumbered vec_slots together with the
//     pending keys (state "pending"), renames the vector file, then finishes
//     (state "generation+1"); every transition differs from the loaded state,
//     and the generation never decreases, so equality means the slots this
//     searcher resolves still address the vector file it holds. This is the
//     part Search re-checks after resolving passages.
//   - graph is the graph row counters and the document-graph presence. The
//     graph handle reads the live tables, so it never serves stale rows; what
//     can go stale is the decision made at Load whether the index has a graph
//     at all, and that decision follows these values.
type indexFreshness struct {
	vector string
	graph  string
}

var freshnessKeys = []string{
	"vector_generation", "pending_vector_generation", "pending_vector_file",
	store.MetaGraphSymbols,
	store.MetaGraphEdgesPrefix + "CALLS",
	store.MetaGraphEdgesPrefix + "IMPLEMENTS",
	store.MetaGraphEdgesPrefix + "REFERENCES",
}

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
	for _, k := range freshnessKeys[3:] {
		fmt.Fprintf(&g, "%s=%s;", k, m[k])
	}
	fmt.Fprintf(&g, "docs=%t", docs)
	return indexFreshness{
		vector: fmt.Sprintf("gen=%s;pending=%s@%s", m["vector_generation"], m["pending_vector_generation"], m["pending_vector_file"]),
		graph:  g.String(),
	}, nil
}

// recordFreshness stores the state the searcher is about to load. It runs
// right after the publication recovery and before the vector file is read: a
// publication that lands later makes the recorded state differ, so it is
// detected, never missed.
func (s *LeannSearcher) recordFreshness(ctx context.Context) error {
	s.fresh, s.freshKnown = indexFreshness{}, false
	sp, ok := s.passages.(*storePassages)
	if !ok || sp.st == nil {
		return nil
	}
	f, err := sp.freshness(ctx)
	if err != nil {
		return fmt.Errorf("recording index freshness: %w", err)
	}
	s.fresh, s.freshKnown = f, true
	return nil
}

// Stale reports whether the index on disk changed since this searcher loaded
// it: another process published a vector generation (slots renumbered, vector
// file replaced) or changed whether the index has a code or document graph. It
// costs one small database read and no file walk. A searcher that did not
// record a state (format 1, a custom or unloaded searcher) is never stale. A
// read failure (the database is gone or unreadable) is reported as stale
// together with the error, so the caller's reload surfaces the real problem.
//
// Callers that cache searchers across requests (the server, the MCP server)
// check Stale on acquire and replace a stale searcher; a long-lived single
// holder (the chat) uses Refresh.
func (s *LeannSearcher) Stale(ctx context.Context) (bool, error) {
	cur, ok, err := s.currentFreshness(ctx)
	if !ok {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return cur != s.fresh, nil
}

// vectorStale is the Search-time check: only a change of the published
// vector state invalidates a result in flight.
func (s *LeannSearcher) vectorStale(ctx context.Context) (bool, error) {
	cur, ok, err := s.currentFreshness(ctx)
	if !ok {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return cur.vector != s.fresh.vector, nil
}

// currentFreshness reads the database state when this searcher recorded one.
func (s *LeannSearcher) currentFreshness(ctx context.Context) (indexFreshness, bool, error) {
	if s == nil || !s.freshKnown {
		return indexFreshness{}, false, nil
	}
	sp, ok := s.passages.(*storePassages)
	if !ok || sp.st == nil {
		return indexFreshness{}, false, nil
	}
	cur, err := sp.freshness(ctx)
	return cur, true, err
}

// Refresh reloads the searcher in place when the index changed on disk (see
// Stale) and reports whether it did. It is for a single holder that issues one
// request at a time, such as the chat session: it is NOT safe to call while
// another goroutine is searching with the same searcher. Servers replace a
// stale searcher instead, so in-flight requests finish on the old one.
func (s *LeannSearcher) Refresh(ctx context.Context) (bool, error) {
	stale, serr := s.Stale(ctx)
	if !stale {
		return false, nil
	}
	name := s.name
	if name == "" {
		return false, serr
	}
	_ = s.Close()
	s.loaded, s.vectorAvailable = false, false
	s.backend, s.passages = nil, nil
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
