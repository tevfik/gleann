package gleann

import (
	"fmt"
	"strings"
	"testing"
)

func budgetResults(n int, text string, src string) []SearchResult {
	out := make([]SearchResult, n)
	for i := range out {
		out[i] = SearchResult{
			ID:       int64(i),
			Text:     fmt.Sprintf("%s #%d", text, i),
			Score:    float32(n-i) / float32(n),
			Metadata: map[string]any{"source": fmt.Sprintf("%s/%d.go", src, i)},
		}
	}
	return out
}

// effectiveBudget mirrors the clamping documented on TokenBudgetOptions.
func effectiveBudget(maxTokens, minTokens int) int {
	m := minTokens
	if m < MinMaxTokens {
		m = MinMaxTokens
	}
	if maxTokens < m {
		return m
	}
	return maxTokens
}

func assertBudgetInvariant(t *testing.T, results []SearchResult, opts TokenBudgetOptions) (string, TokenBudgetReport) {
	t.Helper()
	out, rep := FormatSearchResultsWithBudget(results, opts)
	if opts.MaxTokens <= 0 {
		return out, rep
	}
	limit := effectiveBudget(opts.MaxTokens, opts.MinTokens)
	if got := EstimateTokens(out); got > limit {
		t.Fatalf("invariant violated: EstimateTokens(output)=%d > max_tokens=%d\n%s", got, limit, out)
	}
	if rep.UsedTokens != EstimateTokens(out) {
		t.Fatalf("report.UsedTokens=%d, actual=%d", rep.UsedTokens, EstimateTokens(out))
	}
	deduped, _ := DeduplicateSearchResults(results)
	if len(deduped) > 0 {
		if rep.MaxTokens != limit {
			t.Fatalf("report.MaxTokens=%d, want %d", rep.MaxTokens, limit)
		}
		if total := rep.FullCount + rep.LocatorCount + rep.DroppedCount; total != len(deduped) {
			t.Fatalf("full(%d)+locator(%d)+dropped(%d)=%d != %d deduped results",
				rep.FullCount, rep.LocatorCount, rep.DroppedCount, total, len(deduped))
		}
		if got := strings.Count(out, "[Locator only]"); got != rep.LocatorCount {
			t.Fatalf("output has %d locators, report says %d\n%s", got, rep.LocatorCount, out)
		}
		if got := strings.Count(out, "---\nResult [") - strings.Count(out, "[Locator only]"); got != rep.FullCount {
			t.Fatalf("output has %d full items, report says %d\n%s", got, rep.FullCount, out)
		}
	}
	return out, rep
}

func TestTokenBudget_InvariantTable(t *testing.T) {
	huge := strings.Repeat("x", 50_000)
	unicode := strings.Repeat("表格😀ж", 2_000)
	tests := []struct {
		name    string
		results []SearchResult
		opts    TokenBudgetOptions
	}{
		{"tiny budget clamps to minimum", budgetResults(5, "hello", "a"), TokenBudgetOptions{MaxTokens: 1}},
		{"exact minimum", budgetResults(5, "hello", "a"), TokenBudgetOptions{MaxTokens: MinMaxTokens}},
		{"min tokens above max", budgetResults(5, "hello", "a"), TokenBudgetOptions{MaxTokens: 40, MinTokens: 200}},
		{"huge single result", []SearchResult{{Text: huge, Metadata: map[string]any{"source": "big.go"}}}, TokenBudgetOptions{MaxTokens: 100}},
		{"huge first then small", append([]SearchResult{{Text: huge, Score: 1, Metadata: map[string]any{"source": "big.go"}}}, budgetResults(3, "small", "s")...), TokenBudgetOptions{MaxTokens: 150, IncludeTip: true}},
		{"unicode text", budgetResults(10, unicode, "u"), TokenBudgetOptions{MaxTokens: 300}},
		{"unicode source paths", budgetResults(10, "x", strings.Repeat("路径", 50)), TokenBudgetOptions{MaxTokens: 120}},
		{"many results", budgetResults(2000, "short text", "m"), TokenBudgetOptions{MaxTokens: 500, IncludeTip: true}},
		{"huge budget", budgetResults(20, "text", "h"), TokenBudgetOptions{MaxTokens: 1 << 30, IncludeTip: true}},
		{"nil metadata", []SearchResult{{Text: "a"}, {Text: "b"}}, TokenBudgetOptions{MaxTokens: 64}},
		{"long tokenizer name", budgetResults(3, "t", "l"), TokenBudgetOptions{MaxTokens: 40, TokenizerName: strings.Repeat("tok", 100)}},
		{"graph context", []SearchResult{{Text: "fn", Metadata: map[string]any{"source": "g.go"}, GraphContext: &GraphContextInfo{Symbols: []SymbolNeighbors{{FQN: "pkg.F", Kind: "function", Callers: []string{"a", "b"}, Callees: []string{"c"}}}}}}, TokenBudgetOptions{MaxTokens: 60}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertBudgetInvariant(t, tt.results, tt.opts)
		})
	}
}

func TestTokenBudget_ReportCountsWhenFullFollowsLocator(t *testing.T) {
	// A large result degrades to a locator, a later small result still fits in
	// full. The reported full/locator counts must match what was rendered,
	// including after the final safety trim.
	big := SearchResult{Text: strings.Repeat("y", 2000), Score: 0.9, Metadata: map[string]any{"source": "big.go"}}
	small := SearchResult{Text: "tiny", Score: 0.5, Metadata: map[string]any{"source": "s.go"}}
	for budget := MinMaxTokens; budget < 200; budget++ {
		assertBudgetInvariant(t, []SearchResult{big, small, big, small}, TokenBudgetOptions{MaxTokens: budget, IncludeTip: true})
	}
}

func TestTokenBudget_EmptyAndDedup(t *testing.T) {
	out, rep := FormatSearchResultsWithBudget(nil, TokenBudgetOptions{MaxTokens: 50})
	if out != "No relevant memory fragments found." || rep.UsedTokens != EstimateTokens(out) {
		t.Fatalf("unexpected empty output %q %+v", out, rep)
	}
	r := SearchResult{Text: "  same  ", Metadata: map[string]any{"source": "a.go"}}
	r2 := SearchResult{Text: "same", Metadata: map[string]any{"source": "a.go"}}
	r3 := SearchResult{Text: "same", Metadata: map[string]any{"source": "b.go"}}
	deduped, dropped := DeduplicateSearchResults([]SearchResult{r, r2, r3})
	if len(deduped) != 2 || dropped != 1 {
		t.Fatalf("dedup = %d kept / %d dropped, want 2/1", len(deduped), dropped)
	}
	if deduped[0].Text != r.Text {
		t.Fatalf("dedup must keep the first (highest-ranked) occurrence")
	}
}

func FuzzFormatSearchResultsWithBudget(f *testing.F) {
	f.Add(10, 0, 3, "hello world", "src", false, uint8(5))
	f.Add(32, 0, 1, strings.Repeat("a", 5000), "x", true, uint8(1))
	f.Add(1, 500, 4, "表格😀", "路径", true, uint8(9))
	f.Add(400, 0, 50, "func main() {}", "cmd/main.go", true, uint8(0))
	f.Add(0, 0, 3, "x", "y", false, uint8(2))
	f.Fuzz(func(t *testing.T, maxTokens, minTokens, n int, text, src string, tip bool, seed uint8) {
		if n < 0 {
			n = -n
		}
		n %= 64
		if maxTokens > 1<<20 || maxTokens < -1<<20 || minTokens > 1<<16 || minTokens < -1<<16 {
			return
		}
		results := make([]SearchResult, 0, n)
		for i := 0; i < n; i++ {
			// Vary sizes deterministically so full/locator/drop paths interleave.
			rep := 1 + (int(seed)*(i+1))%7
			body := strings.Repeat(text, rep)
			if (int(seed)+i)%5 == 0 {
				body = text // duplicates exercise dedup
			}
			results = append(results, SearchResult{
				Text:     body,
				Score:    float32(i),
				Metadata: map[string]any{"source": fmt.Sprintf("%s/%d", src, i%3)},
			})
		}
		assertBudgetInvariant(t, results, TokenBudgetOptions{MaxTokens: maxTokens, MinTokens: minTokens, IncludeTip: tip})
	})
}
