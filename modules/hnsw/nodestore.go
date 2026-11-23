package hnsw

import "iter"

// denseSlack is how far past twice the node count an ID may lie and still be
// stored in nodeStore.dense.
const denseSlack = 1024

// nodeStore maps node IDs to nodes, like the map[int64]*Node it replaces: any
// int64 is a valid ID, IDs may have gaps, and a lookup of an absent ID finds
// nothing. IDs are slots numbered from zero in every index gleann builds, so
// those are kept in a slice (dense) and found without hashing; an ID that is
// negative or far beyond the node count goes to a map (sparse) instead, which
// bounds the slice to 2*count+denseSlack entries whatever the IDs are (an
// index file is untrusted input: one huge ID must not allocate a huge slice).
//
// Invariant: a node whose ID is below len(dense) is in dense, never in sparse.
// get relies on it to answer from one of the two without consulting the other.
type nodeStore struct {
	dense  []*Node
	sparse map[int64]*Node
	count  int
}

// get returns the node with the given ID, or nil when there is none.
func (s *nodeStore) get(id int64) *Node {
	if uint64(id) < uint64(len(s.dense)) {
		return s.dense[id]
	}
	return s.getSparse(id)
}

// getSparse is the slow half of get, kept apart so that get inlines.
func (s *nodeStore) getSparse(id int64) *Node {
	if s.sparse == nil {
		return nil
	}
	return s.sparse[id]
}

// len returns the number of nodes.
func (s *nodeStore) len() int { return s.count }

// denseLen returns the number of IDs, counted from zero, that are looked up
// in the slice: a visitSet of that size marks them without hashing.
func (s *nodeStore) denseLen() int { return len(s.dense) }

// put stores n under id, replacing the node already there. n must not be nil.
func (s *nodeStore) put(id int64, n *Node) {
	if uint64(id) >= uint64(len(s.dense)) {
		if id < 0 || id >= 2*int64(s.count)+denseSlack {
			if s.sparse == nil {
				s.sparse = make(map[int64]*Node)
			}
			if _, ok := s.sparse[id]; !ok {
				s.count++
			}
			s.sparse[id] = n
			return
		}
		s.growDense(id)
	}
	if s.dense[id] == nil {
		s.count++
	}
	s.dense[id] = n
}

// remove drops the node with the given ID, if there is one. Edges that point
// at it stay behind: every traversal skips an ID without a node.
func (s *nodeStore) remove(id int64) {
	if uint64(id) < uint64(len(s.dense)) {
		if s.dense[id] != nil {
			s.dense[id] = nil
			s.count--
		}
		return
	}
	if _, ok := s.sparse[id]; ok {
		delete(s.sparse, id)
		s.count--
	}
}

// growDense extends dense to cover id and moves the sparse nodes whose IDs it
// now covers into it, which keeps the invariant. The move costs at most the
// number of slots added, so it is paid for by the growth itself.
func (s *nodeStore) growDense(id int64) {
	old := int64(len(s.dense))
	for int64(len(s.dense)) <= id {
		s.dense = append(s.dense, nil)
	}
	if len(s.sparse) == 0 {
		return
	}
	if id-old < int64(len(s.sparse)) {
		for i := old; i <= id; i++ {
			if n, ok := s.sparse[i]; ok {
				s.dense[i] = n
				delete(s.sparse, i)
			}
		}
		return
	}
	for i, n := range s.sparse {
		if i >= old && i <= id {
			s.dense[i] = n
			delete(s.sparse, i)
		}
	}
}

// all iterates over every node: the dense ones in ascending ID order, then
// the sparse ones in no particular order.
func (s *nodeStore) all() iter.Seq2[int64, *Node] {
	return func(yield func(int64, *Node) bool) {
		for id, n := range s.dense {
			if n != nil && !yield(int64(id), n) {
				return
			}
		}
		for id, n := range s.sparse {
			if !yield(id, n) {
				return
			}
		}
	}
}

// visitSet is the set of node IDs one level search has seen. IDs inside the
// dense range of the node store are marked in a slice, by writing the current
// epoch into their slot: starting the next search is then one increment
// instead of a new map. Any other ID is kept in a map.
type visitSet struct {
	stamp []uint32
	epoch uint32
	far   map[int64]struct{}
}

// reset empties the set and sizes it for a node store with n dense slots.
func (v *visitSet) reset(n int) {
	if n <= cap(v.stamp) {
		// Slots between the old and the new length were never written: they
		// are zero, and the epoch is never zero.
		v.stamp = v.stamp[:n]
	} else {
		v.stamp = make([]uint32, n, n+n/2+denseSlack)
	}
	v.epoch++
	if v.epoch == 0 { // wrapped: old stamps could now look current
		clear(v.stamp[:cap(v.stamp)])
		v.epoch = 1
	}
	if len(v.far) > 0 {
		clear(v.far)
	}
}

// visit adds id to the set and reports whether it was already in it.
func (v *visitSet) visit(id int64) bool {
	if uint64(id) < uint64(len(v.stamp)) {
		if v.stamp[id] == v.epoch {
			return true
		}
		v.stamp[id] = v.epoch
		return false
	}
	return v.visitFar(id)
}

// has reports whether id is in the set.
func (v *visitSet) has(id int64) bool {
	if uint64(id) < uint64(len(v.stamp)) {
		return v.stamp[id] == v.epoch
	}
	_, ok := v.far[id]
	return ok
}

func (v *visitSet) visitFar(id int64) bool {
	if _, ok := v.far[id]; ok {
		return true
	}
	if v.far == nil {
		v.far = make(map[int64]struct{})
	}
	v.far[id] = struct{}{}
	return false
}

// searchScratch holds the working memory of one level search (searchLevel)
// and of the greedy descent above it (descend), so that neither allocates once
// the buffers have grown. A scratch serves one search at a time: Insert uses
// the graph's own under the write lock, concurrent searches each take one from
// the graph's pool.
type searchScratch struct {
	visited    visitSet
	candidates candidateHeap
	results    candidateHeap
	freshIDs   []int64
	freshNodes []*Node
	freshDist  []float32
}

// dists returns the scratch distance buffer resized to n values.
func (sc *searchScratch) dists(n int) []float32 {
	if cap(sc.freshDist) < n {
		sc.freshDist = make([]float32, n, n+n/2)
	}
	sc.freshDist = sc.freshDist[:n]
	return sc.freshDist
}

// insertScratch is the working memory of Insert, guarded by the graph's write
// lock like the graph itself.
type insertScratch struct {
	search searchScratch

	// selectNeighborsHeuristic
	selected []Candidate
	selNodes []*Node
	inSel    visitSet

	// pruneConnections: the scored neighbours, their nodes and distances.
	scored     []Candidate
	pruneNodes []*Node
	pruneDist  []float32
}

// byDistance orders candidates by ascending distance. Sorting with it through
// slices.SortFunc makes the comparisons and moves sort.Slice made with
// "a.Distance < b.Distance" (both are the same pdqsort), so candidates at the
// same distance end up in the same order as before; TestSortKeepsTieOrder
// holds the two together.
func byDistance(a, b Candidate) int {
	switch {
	case a.Distance < b.Distance:
		return -1
	case a.Distance > b.Distance:
		return 1
	}
	return 0
}
