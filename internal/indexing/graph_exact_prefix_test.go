package indexing

import "testing"

// TestPrefixOfMirrorsProductSeparators pins the precision-test mirror to the
// product's slash-only behaviour (indexer.buildPkgPrefixOf): the result must
// never contain a backslash, whatever filepath does on the host OS.
func TestPrefixOfMirrorsProductSeparators(t *testing.T) {
	cases := []struct{ module, rel, want string }{
		{"m", "internal/indexing/x.go", "m/internal/indexing"},
		{"m", `internal\indexing\x.go`, "m/internal/indexing"},
		{"m", "x.go", "m"},
		{"", "a/b/x.go", "a/b"},
	}
	for _, c := range cases {
		if got := prefixOf(c.module, c.rel); got != c.want {
			t.Errorf("prefixOf(%q,%q) = %q, want %q", c.module, c.rel, got, c.want)
		}
	}
}
