package hnsw

import (
	"math"
	"math/rand"
	"slices"
	"sort"
	"testing"
)

// nodeStore answers like the map it replaced, whatever the IDs are and in
// whatever order they come, including removals and replacements.
func TestNodeStoreBehavesLikeAMap(t *testing.T) {
	idKinds := map[string]func(r *rand.Rand) int64{
		"slots":     func(r *rand.Rand) int64 { return int64(r.Intn(3000)) },
		"with gaps": func(r *rand.Rand) int64 { return int64(r.Intn(40000)) },
		"negative":  func(r *rand.Rand) int64 { return int64(r.Intn(600)) - 300 },
		"huge":      func(r *rand.Rand) int64 { return math.MaxInt64 - int64(r.Intn(500)) },
		"everything": func(r *rand.Rand) int64 {
			return []int64{int64(r.Intn(2000)), 1<<40 + int64(r.Intn(50)), -int64(r.Intn(50)), int64(r.Intn(1 << 20))}[r.Intn(4)]
		},
	}
	for name, nextID := range idKinds {
		t.Run(name, func(t *testing.T) {
			r := rand.New(rand.NewSource(1))
			var s nodeStore
			model := map[int64]*Node{}
			peak := 0
			check := func(id int64) {
				t.Helper()
				if got := s.get(id); got != model[id] {
					t.Fatalf("get(%d) = %p, the map has %p", id, got, model[id])
				}
			}
			for step := 0; step < 20000; step++ {
				id := nextID(r)
				switch r.Intn(10) {
				case 0, 1:
					s.remove(id)
					delete(model, id)
				case 2, 3, 4:
					check(id)
				default:
					n := &Node{ID: id}
					s.put(id, n)
					model[id] = n
				}
				check(id)
				if s.len() != len(model) {
					t.Fatalf("step %d: len = %d, the map has %d", step, s.len(), len(model))
				}
				// The slice never outgrows the nodes it has served by more
				// than a constant factor, whatever the IDs are.
				peak = max(peak, s.len())
				if s.denseLen() > 2*peak+denseSlack {
					t.Fatalf("step %d: %d dense slots for at most %d nodes", step, s.denseLen(), peak)
				}
				for id := range s.sparse {
					if uint64(id) < uint64(len(s.dense)) {
						t.Fatalf("step %d: node %d is in the map although the slice covers it", step, id)
					}
				}
			}
			seen := map[int64]*Node{}
			last, ascending := int64(-1), true
			for id, n := range s.all() {
				if _, dup := seen[id]; dup {
					t.Fatalf("all() yields %d twice", id)
				}
				seen[id] = n
				if uint64(id) < uint64(len(s.dense)) {
					ascending = ascending && id > last
					last = id
				}
			}
			if !ascending {
				t.Fatal("all() does not yield the dense IDs in ascending order")
			}
			if len(seen) != len(model) {
				t.Fatalf("all() yields %d nodes, the map has %d", len(seen), len(model))
			}
			for id, n := range model {
				if seen[id] != n {
					t.Fatalf("all() yields %p for %d, the map has %p", seen[id], id, n)
				}
			}
			// all() stops when asked to.
			count := 0
			for range s.all() {
				if count++; count == 3 {
					break
				}
			}
			if len(model) >= 3 && count != 3 {
				t.Fatalf("all() ran %d times after a break at 3", count)
			}
		})
	}
}

// One node with a huge ID (an index file is untrusted input) costs a map
// entry, not a slice that reaches up to it; and nodes that start out in the
// map move to the slice once it grows over their IDs.
func TestNodeStoreKeepsTheSliceSmall(t *testing.T) {
	var s nodeStore
	s.put(1<<50, &Node{ID: 1 << 50})
	s.put(denseSlack+5, &Node{ID: denseSlack + 5}) // beyond the slack of an empty store
	if s.denseLen() != 0 || len(s.sparse) != 2 {
		t.Fatalf("%d dense slots and %d map entries, want 0 and 2", s.denseLen(), len(s.sparse))
	}
	for id := int64(0); id < 10; id++ {
		s.put(id, &Node{ID: id})
	}
	s.put(denseSlack+6, &Node{ID: denseSlack + 6}) // now inside the slack: the slice grows over denseSlack+5
	if s.denseLen() != denseSlack+7 || len(s.sparse) != 1 {
		t.Fatalf("%d dense slots and %d map entries, want %d and 1", s.denseLen(), len(s.sparse), denseSlack+7)
	}
	if n := s.get(denseSlack + 5); n == nil || n.ID != denseSlack+5 {
		t.Fatalf("the node that moved from the map to the slice is lost: %v", n)
	}
	if s.get(1<<50) == nil || s.len() != 13 {
		t.Fatalf("len = %d, want 13 with the huge ID still there", s.len())
	}
}

// A visitSet forgets everything on reset, also after the epoch counter wraps,
// and holds IDs outside the slice range too.
func TestVisitSet(t *testing.T) {
	var v visitSet
	v.reset(100)
	ids := []int64{0, 99, 100, -1, 1 << 40}
	for _, id := range ids {
		if v.has(id) || v.visit(id) {
			t.Fatalf("%d is in a fresh set", id)
		}
		if !v.has(id) || !v.visit(id) {
			t.Fatalf("%d is not in the set after visit", id)
		}
	}
	v.reset(150) // grows: 100 is now in the slice range
	for _, id := range append(ids, 149) {
		if v.has(id) {
			t.Fatalf("%d survived the reset", id)
		}
	}
	v.visit(7)
	v.epoch = math.MaxUint32 // the next reset wraps the counter
	v.visit(8)
	v.reset(150)
	if v.epoch != 1 || v.has(7) || v.has(8) {
		t.Fatalf("after the wrap: epoch %d, has(7) %v, has(8) %v", v.epoch, v.has(7), v.has(8))
	}
	v.visit(120)
	v.reset(20) // shrinks; the slots beyond stay clean for the next growth
	v.visit(19)
	v.reset(150)
	if v.has(19) || v.has(120) {
		t.Fatal("a stamp from before the shrink is still current")
	}
}

// Sorting candidates with slices.SortFunc and byDistance leaves candidates at
// equal distances in the order sort.Slice left them: the sort is not stable,
// and the order of ties decides which neighbours a node keeps. If a Go release
// ever changes one sort and not the other, this fails and the build would no
// longer be the graph older builds made.
func TestSortKeepsTieOrder(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	for round := 0; round < 3000; round++ {
		n := r.Intn(300)
		distinct := 1 + r.Intn(1+n/2)
		a := make([]Candidate, n)
		for i := range a {
			a[i] = Candidate{ID: int64(i), Distance: float32(r.Intn(distinct))}
		}
		if round%7 == 0 { // nearly sorted input takes other paths through pdqsort
			sort.SliceStable(a, func(i, j int) bool { return a[i].Distance < a[j].Distance })
			if n > 2 {
				a[r.Intn(n)], a[r.Intn(n)] = a[r.Intn(n)], a[r.Intn(n)]
			}
		}
		b := slices.Clone(a)
		sort.Slice(a, func(i, j int) bool { return a[i].Distance < a[j].Distance })
		slices.SortFunc(b, byDistance)
		if !slices.Equal(a, b) {
			t.Fatalf("round %d (%d candidates, %d distances): the two sorts disagree", round, n, distinct)
		}
	}
}
