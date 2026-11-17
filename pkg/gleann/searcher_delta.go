package gleann

import (
	"context"
	"fmt"
	"math"
	"os"
)

// deltaState is what a searcher holds besides the main vector file (RFC 005
// section 3): the delta file's vectors and how many slots are live and dead.
// It is replaced as a whole (LeannSearcher.dstate), so a search sees the old
// state or the new one, never a mix.
type deltaState struct {
	delta *vectorDelta // nil: no delta file
	live  int64        // passages that have a vector slot
	dead  int64        // slots below the high-water mark that no passage holds
}

// loadDeltaState reads the delta file next to the index and the slot counts
// from the store. A delta whose dimensions differ from the index's is refused:
// it would be compared against vectors of another model.
func (s *LeannSearcher) loadDeltaState(ctx context.Context, basePath string) (*deltaState, error) {
	sp, ok := s.passages.(*storePassages)
	if !ok || sp.st == nil {
		return nil, nil
	}
	d, err := readVectorDelta(basePath + suffixDelta)
	if err != nil {
		return nil, fmt.Errorf("index %q: %w; repair the index: gleann index repair %s", s.name, err, s.name)
	}
	if d != nil && d.Len() > 0 && s.meta.Dimensions > 0 && d.dims != s.meta.Dimensions {
		return nil, fmt.Errorf("index %q: the delta file holds %d-dimensional vectors, the index %d; repair the index: gleann index repair %s",
			s.name, d.dims, s.meta.Dimensions, s.name)
	}
	count, _, slotless, err := sp.st.SlotSpan(ctx)
	if err != nil {
		return nil, err
	}
	slots, err := currentVectorSlots(ctx, sp.st)
	if err != nil {
		return nil, err
	}
	live := int64(count - slotless)
	return &deltaState{delta: d, live: live, dead: max(0, slots-live)}, nil
}

// loadVectorFiles loads the main vector file and the delta, then verifies that
// no publication landed meanwhile (load-then-verify, RFC 005 section 5): the
// freshness recorded before the files were read must still be current after.
// A publication that landed repeats the load; the main file is only loaded
// again when it was replaced.
func (s *LeannSearcher) loadVectorFiles(ctx context.Context, basePath string) error {
	indexPath := basePath + suffixMain
	for attempt := 0; ; attempt++ {
		if s.backend == nil {
			factory, err := GetBackend(s.meta.Backend)
			if err != nil {
				return fmt.Errorf("get backend: %w", err)
			}
			s.backend = factory.NewSearcher(s.config)
			// Attempt Zero-Copy Memory Mapping first, fallback to standard RAM loading
			if mmapSearcher, ok := s.backend.(MmapBackendSearcher); ok {
				if err := mmapSearcher.LoadFromFile(ctx, indexPath); err != nil {
					s.backend = nil
					return fmt.Errorf("load backend mmap: %w", err)
				}
			} else {
				indexData, err := os.ReadFile(indexPath)
				if err != nil {
					s.backend = nil
					return fmt.Errorf("read index: %w", err)
				}
				if err := s.backend.Load(ctx, indexData, s.meta); err != nil {
					s.backend = nil
					return fmt.Errorf("load backend: %w", err)
				}
			}
		}
		ds, err := s.loadDeltaState(ctx, basePath)
		if err != nil {
			return err
		}
		s.dstate.Store(ds)

		cur, known, err := s.currentFreshness(ctx)
		if err != nil {
			return fmt.Errorf("verifying index freshness: %w", err)
		}
		if !known {
			return nil
		}
		rec, _ := s.loadFresh()
		if cur.vector == rec.vector && cur.main == rec.main {
			return nil
		}
		if attempt >= 3 {
			return fmt.Errorf("%w: it kept changing while it was loaded", ErrIndexChanged)
		}
		// Something was published while the files were read: record the new
		// state (rolling a pending publication forward) and read again. A
		// replaced main file needs a new backend.
		mainReplaced := cur.main != rec.main
		if err := s.recordFreshness(ctx); err != nil {
			return err
		}
		if mainReplaced {
			_ = s.backend.Close()
			s.backend = nil
		}
	}
}

// overfetch is how many vector hits to request so that, after the hits of
// dead slots are dropped, about pool remain: pool plus its share of dead slots
// (RFC 005 section 3.2), capped at filterPoolCap. The score normaliser stays
// fixed at the first round, so over-fetching does not change scores.
func overfetch(pool int, st *deltaState) int {
	if st == nil || st.dead <= 0 || st.live <= 0 {
		return pool
	}
	extra := int(math.Ceil(float64(pool) * float64(st.dead) / float64(st.live)))
	return max(pool, min(pool+extra, filterPoolCap))
}

// mergeDeltaHits folds the delta's exact hits into the backend's, drops hits
// of dead slots and cuts the list to pool. Without a delta and without dead
// slots the lists come back untouched.
func (s *LeannSearcher) mergeDeltaHits(ctx context.Context, st *deltaState, query []float32, ids []int64, dists []float32, pool, fetched int) ([]int64, []float32, error) {
	if st == nil || (st.delta.Len() == 0 && st.dead == 0) {
		return ids, dists, nil
	}
	if st.delta.Len() > 0 {
		dIDs, dDists := st.delta.Search(query, fetched)
		ids, dists = mergeVectorHits(ids, dists, dIDs, dDists, 0)
	}
	if st.dead > 0 && len(ids) > 0 {
		if sp, ok := s.passages.(*storePassages); ok && sp.st != nil {
			live, err := sp.st.LiveSlots(ctx, ids)
			if err != nil {
				return nil, nil, err
			}
			keptIDs, keptDists := ids[:0:0], dists[:0:0]
			for i, id := range ids {
				if live[id] {
					keptIDs, keptDists = append(keptIDs, id), append(keptDists, dists[i])
				}
			}
			ids, dists = keptIDs, keptDists
		}
	}
	if len(ids) > pool {
		ids, dists = ids[:pool], dists[:pool]
	}
	return ids, dists, nil
}

// deltaScorer scores explicit slots (filtered search) across both legs: a slot
// in the delta is scored by the delta, any other by the main file's backend.
type deltaScorer struct {
	main  VectorIDScorer
	delta *vectorDelta
}

func (d deltaScorer) VectorDistances(ctx context.Context, query []float32, ids []int64) ([]float32, error) {
	out := make([]float32, len(ids))
	var mainIdx []int
	var mainIDs []int64
	for i, id := range ids {
		if dist, ok := d.delta.Distance(query, id); ok {
			out[i] = dist
			continue
		}
		mainIdx = append(mainIdx, i)
		mainIDs = append(mainIDs, id)
	}
	if len(mainIDs) > 0 {
		dists, err := d.main.VectorDistances(ctx, query, mainIDs)
		if err != nil {
			return nil, err
		}
		if len(dists) != len(mainIDs) {
			return nil, fmt.Errorf("vector backend scored %d of %d slots", len(dists), len(mainIDs))
		}
		for j, i := range mainIdx {
			out[i] = dists[j]
		}
	}
	return out, nil
}
