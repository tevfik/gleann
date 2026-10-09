package gleann

import (
	"strings"
	"testing"
)

func TestTokenBudget_Unconstrained(t *testing.T) {
	results := []SearchResult{
		{
			Text:  "Short passage 1",
			Score: 0.95,
			Metadata: map[string]any{
				"source": "pkg/file1.go",
			},
		},
		{
			Text:  "Short passage 2",
			Score: 0.85,
			Metadata: map[string]any{
				"source": "pkg/file2.go",
			},
		},
	}

	opts := TokenBudgetOptions{
		MaxTokens: 0, // Unconstrained
	}

	output, report := FormatSearchResultsWithBudget(results, opts)
	if report.MaxTokens != 0 {
		t.Errorf("expected MaxTokens=0, got %d", report.MaxTokens)
	}
	if report.FullCount != 2 {
		t.Errorf("expected FullCount=2, got %d", report.FullCount)
	}
	if report.LocatorCount != 0 {
		t.Errorf("expected LocatorCount=0, got %d", report.LocatorCount)
	}
	if report.DroppedCount != 0 {
		t.Errorf("expected DroppedCount=0, got %d", report.DroppedCount)
	}
	if !strings.Contains(output, "Short passage 1") || !strings.Contains(output, "Short passage 2") {
		t.Errorf("expected output to contain both passages, got: %s", output)
	}
}

func TestTokenBudget_StrictBudgetEnforcement(t *testing.T) {
	// Create several passages of moderate length (~60 tokens each)
	longText1 := strings.Repeat("Alpha beta gamma delta epsilon. ", 8) // ~256 chars, ~64 tokens
	longText2 := strings.Repeat("Zeta eta theta iota kappa lambda. ", 8)
	longText3 := strings.Repeat("Mu nu xi omicron pi rho sigma. ", 8)

	results := []SearchResult{
		{
			Text:  longText1,
			Score: 0.95,
			Metadata: map[string]any{
				"source": "cmd/alpha.go",
			},
		},
		{
			Text:  longText2,
			Score: 0.88,
			Metadata: map[string]any{
				"source": "cmd/beta.go",
			},
		},
		{
			Text:  longText3,
			Score: 0.75,
			Metadata: map[string]any{
				"source": "cmd/gamma.go",
			},
		},
	}

	// Set budget: 140 tokens (fits text1 full + text2 locator + summary)
	maxTokens := 140
	opts := TokenBudgetOptions{
		MaxTokens: maxTokens,
	}

	output, report := FormatSearchResultsWithBudget(results, opts)

	usedTokens := EstimateTokens(output)
	if usedTokens > maxTokens {
		t.Fatalf("Strict invariant violated! usedTokens=%d > maxTokens=%d\nOutput:\n%s", usedTokens, maxTokens, output)
	}

	if report.FullCount == 0 {
		t.Errorf("expected at least 1 full result if it fits, got %d", report.FullCount)
	}
	if report.LocatorCount+report.DroppedCount == 0 {
		t.Errorf("expected at least some locators or drops with tight budget, got locators=%d, dropped=%d",
			report.LocatorCount, report.DroppedCount)
	}

	// Must contain token budget summary header or footer
	if !strings.Contains(output, "Token Budget:") {
		t.Errorf("expected output to contain Token Budget accounting summary, got:\n%s", output)
	}
}

func TestTokenBudget_LocatorDegradation(t *testing.T) {
	// 1st passage fits fully, 2nd passage does not fit fully but fits as locator
	text1 := "Function calculateMetrics computes latency and throughput for all active worker nodes."
	text2 := strings.Repeat("This is a verbose passage with extensive details about configuration parameters. ", 20)

	results := []SearchResult{
		{
			Text:  text1,
			Score: 0.90,
			Metadata: map[string]any{
				"source": "pkg/metrics.go",
			},
		},
		{
			Text:  text2,
			Score: 0.80,
			Metadata: map[string]any{
				"source": "pkg/config.go",
			},
		},
	}

	// Budget that allows text1 full + text2 as locator
	maxTokens := 90
	opts := TokenBudgetOptions{
		MaxTokens: maxTokens,
	}

	output, report := FormatSearchResultsWithBudget(results, opts)

	usedTokens := EstimateTokens(output)
	if usedTokens > maxTokens {
		t.Fatalf("Invariant violated: usedTokens=%d > maxTokens=%d", usedTokens, maxTokens)
	}

	if report.FullCount != 1 {
		t.Errorf("expected FullCount=1, got %d", report.FullCount)
	}
	if report.LocatorCount < 1 {
		t.Errorf("expected LocatorCount>=1, got %d", report.LocatorCount)
	}

	if !strings.Contains(output, "[Locator only") {
		t.Errorf("expected output to contain '[Locator only', got:\n%s", output)
	}
}

func TestTokenBudget_Deduplication(t *testing.T) {
	results := []SearchResult{
		{
			Text:  "Exact duplicate text block across overlapping chunk",
			Score: 0.92,
			Metadata: map[string]any{
				"source": "pkg/duplicate.go",
				"line":   10,
			},
		},
		{
			Text:  "Exact duplicate text block across overlapping chunk",
			Score: 0.82,
			Metadata: map[string]any{
				"source": "pkg/duplicate.go",
				"line":   10,
			},
		},
		{
			Text:  "Unique text block",
			Score: 0.75,
			Metadata: map[string]any{
				"source": "pkg/unique.go",
				"line":   50,
			},
		},
	}

	deduped, dropped := DeduplicateSearchResults(results)
	if dropped != 1 {
		t.Errorf("expected 1 dropped duplicate, got %d", dropped)
	}
	if len(deduped) != 2 {
		t.Fatalf("expected 2 deduped results, got %d", len(deduped))
	}
	if deduped[0].Score != 0.92 {
		t.Errorf("expected highest score to be retained, got score=%.2f", deduped[0].Score)
	}
}

func TestTokenBudget_MinTokensEnforcement(t *testing.T) {
	results := []SearchResult{
		{
			Text:  "Short test",
			Score: 0.9,
			Metadata: map[string]any{
				"source": "test.go",
			},
		},
	}

	// Below MinMaxTokens (32), should be clamped to MinMaxTokens
	opts := TokenBudgetOptions{
		MaxTokens: 10,
	}

	output, report := FormatSearchResultsWithBudget(results, opts)
	if report.MaxTokens != MinMaxTokens {
		t.Errorf("expected MaxTokens to clamp to %d, got %d", MinMaxTokens, report.MaxTokens)
	}
	usedTokens := EstimateTokens(output)
	if usedTokens > MinMaxTokens {
		t.Errorf("usedTokens=%d exceeds clamped min=%d", usedTokens, MinMaxTokens)
	}
}

func TestTokenBudget_EmptyResults(t *testing.T) {
	output, report := FormatSearchResultsWithBudget(nil, TokenBudgetOptions{MaxTokens: 100})
	if !strings.Contains(output, "No relevant memory fragments found") {
		t.Errorf("unexpected output for empty results: %s", output)
	}
	if report.FullCount != 0 || report.LocatorCount != 0 || report.DroppedCount != 0 {
		t.Errorf("counts should all be 0 for empty results, got: %+v", report)
	}
}

func TestTokenBudget_SingleItemLargerThanBudget(t *testing.T) {
	hugeText := strings.Repeat("A very large string that definitely exceeds fifty tokens by a wide margin. ", 10)
	results := []SearchResult{
		{
			Text:  hugeText,
			Score: 0.85,
			Metadata: map[string]any{
				"source": "huge.go",
			},
		},
	}

	maxTokens := 45
	opts := TokenBudgetOptions{
		MaxTokens: maxTokens,
	}

	output, report := FormatSearchResultsWithBudget(results, opts)
	usedTokens := EstimateTokens(output)
	if usedTokens > maxTokens {
		t.Fatalf("Invariant violated! usedTokens=%d > maxTokens=%d", usedTokens, maxTokens)
	}
	// It should either be a locator or dropped, but never full
	if report.FullCount != 0 {
		t.Errorf("huge item should not be admitted as full, got FullCount=%d", report.FullCount)
	}
}

