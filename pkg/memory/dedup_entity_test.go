package memory

import "testing"

// Fails on the previous rule (Jaccard 0.7 only): swapping one entity merged
// the two facts and the existing wording kept, so the "auth" fact was lost.
func TestAddNearDuplicateNeverMergesEntitySubstitution(t *testing.T) {
	billing := "The billing service connects to redis on host cache-1 for sessions and tokens"
	auth := "The auth service connects to redis on host cache-1 for sessions and tokens"
	for _, order := range [][2]string{{billing, auth}, {auth, billing}} {
		s := openDedupStore(t)
		a := addBlock(t, s, "p", TierLong, order[0])
		b := addBlock(t, s, "p", TierLong, order[1])
		if a.ID == b.ID || countBlocks(t, s) != 2 {
			t.Fatalf("%q merged into %q", order[1], order[0])
		}
		for _, want := range order {
			found := false
			all, _ := s.List("")
			for _, x := range all {
				if x.Content == want {
					found = true
				}
			}
			if !found {
				t.Errorf("fact lost: %q", want)
			}
		}
	}
}

// When the new text is the stored text plus extra words, the longer text wins
// (the added detail is kept, the ID stays); when the stored text is already
// the longer one it is kept.
func TestAddNearDuplicateKeepsTheLongerText(t *testing.T) {
	s := openDedupStore(t)
	short := "The billing service connects to redis on host cache-1 for sessions"
	long := "The billing service connects to redis on host cache-1 for sessions and tokens"
	first := addBlock(t, s, "p", TierLong, short)
	got := addBlock(t, s, "p", TierLong, long)
	if got.ID != first.ID || got.Content != long {
		t.Fatalf("superset: id %s content %q; want id %s and the longer text", got.ID, got.Content, first.ID)
	}
	stored, _ := s.Get(first.ID)
	if stored.Content != long || stored.Confirms != 1 {
		t.Fatalf("stored = %q confirms %d", stored.Content, stored.Confirms)
	}
	// A later, shorter restatement does not drop detail.
	again := addBlock(t, s, "p", TierLong, short+".")
	if again.ID != first.ID || again.Content != long {
		t.Fatalf("subset replaced the longer text: %q", again.Content)
	}
	if n := countBlocks(t, s); n != 1 {
		t.Fatalf("blocks = %d, want 1", n)
	}
	// The longer text is searchable after the replacement.
	m := NewManager(s)
	if got, _ := m.Search("redis tokens"); len(got) != 1 {
		t.Fatalf("replacement not searchable: %v", contents(got))
	}
}

// Fails on the old single-pass stem: "versions" became "version" while
// "version" became "vers", so the two never matched.
func TestStemIsConsistent(t *testing.T) {
	groups := [][]string{
		{"version", "versions"},
		{"connect", "connected", "connection", "connections"},
		{"listen", "listens", "listening"},
	}
	for _, g := range groups {
		want := stem(g[0])
		for _, w := range g {
			if got := stem(w); got != want {
				t.Errorf("stem(%q) = %q, want %q (same as %q)", w, got, want, g[0])
			}
			if stem(stem(w)) != stem(w) {
				t.Errorf("stem not a fixed point for %q", w)
			}
		}
	}
}

func TestSearchFloorMatchesInflections(t *testing.T) {
	m := searchFixture(t,
		Block{Content: "Pin the dependency version in go.mod and the build image"},
		Block{Content: "Every connection to the database goes through the pool"},
	)
	if got, _ := m.Search("which versions are pinned, dependency versions"); len(got) != 1 {
		t.Errorf("versions vs version: %v", contents(got))
	}
	if got, _ := m.Search("database connections pool"); len(got) != 1 {
		t.Errorf("connections vs connection: %v", contents(got))
	}
	if got, _ := m.Search("connect database"); len(got) != 1 {
		t.Errorf("connect vs connection: %v", contents(got))
	}
}
