package gleann

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestTokenBudgetUnconstrainedTipAndIndex(t *testing.T) {
	rs := []SearchResult{
		{Text: "first", Score: 0.9, Metadata: map[string]any{"source": "a.go", "_index": "code"}},
		{Text: "second", Score: 0.5, Metadata: map[string]any{"source": "b.go"}},
	}
	out, rep := FormatSearchResultsWithBudget(rs, TokenBudgetOptions{IncludeTip: true})
	if !strings.Contains(out, "Index: code") || !strings.Contains(out, "Tip: To read the full source") || rep.FullCount != 2 {
		t.Fatalf("unconstrained = %q (%+v)", out, rep)
	}
}

// Whatever the budget and the result sizes, the output never exceeds the
// budget and every result is accounted for exactly once (full, locator or
// dropped).
func TestTokenBudgetInvariantsRandomised(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(12)
		rs := make([]SearchResult, n)
		for i := range rs {
			rs[i] = SearchResult{
				Text:     strings.Repeat(fmt.Sprintf("word%d ", rng.Intn(1000)), 1+rng.Intn(120)),
				Score:    rng.Float32(),
				Metadata: map[string]any{"source": fmt.Sprintf("dir/file%d_%d.go", trial, i), "_index": "idx"},
			}
		}
		budget := MinMaxTokens + rng.Intn(1500)
		out, rep := FormatSearchResultsWithBudget(rs, TokenBudgetOptions{MaxTokens: budget, IncludeTip: rng.Intn(2) == 0})
		if got := EstimateTokens(out); got > budget || rep.UsedTokens > budget {
			t.Fatalf("trial %d: %d tokens used (report %d) over a budget of %d", trial, got, rep.UsedTokens, budget)
		}
		if rep.FullCount+rep.LocatorCount+rep.DroppedCount != n {
			t.Fatalf("trial %d: %d full + %d locator + %d dropped != %d results", trial, rep.FullCount, rep.LocatorCount, rep.DroppedCount, n)
		}
		if rep.LocatorCount > 0 && !strings.Contains(out, "[Locator only]") {
			t.Fatalf("trial %d: locators counted but not rendered", trial)
		}
	}
}
