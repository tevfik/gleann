//go:build kuzu

package kuzu_test

import (
	"context"
	"testing"

	"github.com/tevfik/gleann/bench/graphdb"
	_ "github.com/tevfik/gleann/bench/graphdb/adapters/kuzu"
	_ "github.com/tevfik/gleann/bench/graphdb/adapters/sqlite"
)

// TestParityWithReference runs the Kùzu adapter against the reference on a
// small graph. Path lookups are excluded: Kùzu's 1000-node exploration cap is
// a documented difference (see Caveats).
func TestParityWithReference(t *testing.T) {
	rep, err := graphdb.Run(context.Background(), graphdb.Config{
		Sizes: []int{5000}, Queries: 40, Paths: 5, Backends: []string{"kuzu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Sizes[0].Results {
		if r.Error != "" {
			t.Fatalf("%s: %s", r.Backend, r.Error)
		}
		for check, n := range r.Mismatches {
			if check != "path" && n > 0 {
				t.Errorf("%s: %d %s mismatch(es): %v", r.Backend, n, check, r.Examples)
			}
		}
	}
}
