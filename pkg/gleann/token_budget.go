package gleann

import (
	"fmt"
	"strings"
)

// MinMaxTokens is the minimum accepted token budget for search results.
// Below this, even minimal headers/footers cannot be safely guaranteed.
const MinMaxTokens = 32

// DefaultTokenizerName is the human-readable identifier for token estimation.
const DefaultTokenizerName = "gleann-standard (~4 chars/token)"

// TokenBudgetOptions configures token budgeting for search result rendering.
type TokenBudgetOptions struct {
	MaxTokens     int    // Strict upper bound on tokens. 0 = unconstrained.
	MinTokens     int    // Minimum clamped token budget (default: MinMaxTokens)
	TokenizerName string // Tokenizer identifier for accounting note
	IncludeTip    bool   // Whether to append tip about gleann_read if budget allows
}

// TokenBudgetReport records what was admitted, degraded, or dropped.
type TokenBudgetReport struct {
	MaxTokens      int
	UsedTokens     int
	FullCount      int
	LocatorCount   int
	DroppedCount   int
	DedupDropCount int
	Tokenizer      string
}

// DeduplicateSearchResults removes duplicate or overlapping results.
// If two results have the same source and identical text or matching line numbers,
// the lower-scored one is removed.
func DeduplicateSearchResults(results []SearchResult) ([]SearchResult, int) {
	if len(results) <= 1 {
		return results, 0
	}

	type dedupKey struct {
		source string
		text   string
	}

	seen := make(map[dedupKey]struct{}, len(results))
	var out []SearchResult
	var dropped int

	for _, r := range results {
		src, _ := r.Metadata["source"].(string)
		key := dedupKey{
			source: src,
			text:   strings.TrimSpace(r.Text),
		}

		if _, exists := seen[key]; exists {
			dropped++
			continue
		}

		seen[key] = struct{}{}
		out = append(out, r)
	}

	return out, dropped
}

// FormatSearchResultsWithBudget formats search results while strictly enforcing
// the given token budget.
func FormatSearchResultsWithBudget(results []SearchResult, opts TokenBudgetOptions) (string, TokenBudgetReport) {
	tokenizer := opts.TokenizerName
	if tokenizer == "" {
		tokenizer = DefaultTokenizerName
	}

	// 1. Deduplication
	deduped, dedupDropped := DeduplicateSearchResults(results)

	report := TokenBudgetReport{
		MaxTokens:      opts.MaxTokens,
		DedupDropCount: dedupDropped,
		Tokenizer:      tokenizer,
	}

	if len(deduped) == 0 {
		msg := "No relevant memory fragments found."
		report.UsedTokens = EstimateTokens(msg)
		return msg, report
	}

	// 2. Unconstrained rendering when MaxTokens <= 0
	if opts.MaxTokens <= 0 {
		var sb strings.Builder
		for i, r := range deduped {
			sb.WriteString(renderFullItem(i+1, r))
		}
		if opts.IncludeTip {
			sb.WriteString("\n---\nTip: To read the full source code of any file above, use gleann_read with the Source path.\n")
		}
		res := sb.String()
		report.FullCount = len(deduped)
		report.UsedTokens = EstimateTokens(res)
		return res, report
	}

	// 3. Strict budget enforcement
	minAllowed := opts.MinTokens
	if minAllowed < MinMaxTokens {
		minAllowed = MinMaxTokens
	}
	maxTokens := opts.MaxTokens
	if maxTokens < minAllowed {
		maxTokens = minAllowed
	}
	report.MaxTokens = maxTokens

	droppedCount := 0

	// Helper to generate summary line
	buildSummary := func(full, loc, drop, used int) string {
		return fmt.Sprintf("\n---\n[Token Budget: %d/%d tokens used | %d full, %d locators, %d dropped | Tokenizer: %s]\n",
			used, maxTokens, full, loc, drop, tokenizer)
	}

	// Conservative estimate of summary note token overhead
	dummySummary := buildSummary(len(deduped), len(deduped), len(deduped), maxTokens)
	summaryReserve := EstimateTokens(dummySummary) + 2

	var currentItems []string
	currentTokens := 0

	for i, r := range deduped {
		fullStr := renderFullItem(i+1, r)
		fullTokens := EstimateTokens(fullStr)

		// Can we fit full result + summary reserve?
		if currentTokens+fullTokens+summaryReserve <= maxTokens {
			currentItems = append(currentItems, fullStr)
			currentTokens += fullTokens
			report.FullCount++
			continue
		}

		// Full did not fit, try locator only
		locStr := renderLocatorItem(i+1, r)
		locTokens := EstimateTokens(locStr)

		if currentTokens+locTokens+summaryReserve <= maxTokens {
			currentItems = append(currentItems, locStr)
			currentTokens += locTokens
			report.LocatorCount++
			continue
		}

		// Even locator does not fit
		droppedCount++
	}

	report.DroppedCount = droppedCount

	// Assemble final content
	var sb strings.Builder
	for _, item := range currentItems {
		sb.WriteString(item)
	}

	// Include tip if there's enough room and it's requested
	tip := "\n---\nTip: To read the full source code of any file above, use gleann_read with the Source path.\n"
	tipTokens := EstimateTokens(tip)
	if opts.IncludeTip && (currentTokens+tipTokens+summaryReserve <= maxTokens) {
		sb.WriteString(tip)
		currentTokens += tipTokens
	}

	// Calculate and append the real summary footer
	summary := buildSummary(report.FullCount, report.LocatorCount, report.DroppedCount, currentTokens+summaryReserve)
	sb.WriteString(summary)

	finalOutput := sb.String()
	finalTokens := EstimateTokens(finalOutput)

	// Final safety check: if rounding pushed it slightly over maxTokens,
	// trim last item or shorten summary to strictly enforce invariant
	for finalTokens > maxTokens && len(currentItems) > 0 {
		// Drop last item
		lastIdx := len(currentItems) - 1
		currentItems = currentItems[:lastIdx]
		if report.LocatorCount > 0 {
			report.LocatorCount--
		} else if report.FullCount > 0 {
			report.FullCount--
		}
		report.DroppedCount++

		sb.Reset()
		for _, item := range currentItems {
			sb.WriteString(item)
		}
		summary = buildSummary(report.FullCount, report.LocatorCount, report.DroppedCount, EstimateTokens(sb.String())+summaryReserve)
		sb.WriteString(summary)
		finalOutput = sb.String()
		finalTokens = EstimateTokens(finalOutput)
	}

	report.UsedTokens = finalTokens
	return finalOutput, report
}

func renderFullItem(idx int, r SearchResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("---\nResult [%d] (Score: %.4f)\n", idx, r.Score))
	if metaSource, ok := r.Metadata["source"]; ok {
		sb.WriteString(fmt.Sprintf("Source: %v\n", metaSource))
	}
	if idxName, ok := r.Metadata["_index"].(string); ok {
		sb.WriteString(fmt.Sprintf("Index: %s\n", idxName))
	}
	sb.WriteString(r.Text)
	sb.WriteString("\n")

	if r.GraphContext != nil && len(r.GraphContext.Symbols) > 0 {
		sb.WriteString("Graph Context:\n")
		for _, sym := range r.GraphContext.Symbols {
			sb.WriteString(fmt.Sprintf("  • %s (%s)\n", sym.FQN, sym.Kind))
			if len(sym.Callers) > 0 {
				sb.WriteString(fmt.Sprintf("    ← callers: %s\n", strings.Join(sym.Callers, ", ")))
			}
			if len(sym.Callees) > 0 {
				sb.WriteString(fmt.Sprintf("    → callees: %s\n", strings.Join(sym.Callees, ", ")))
			}
		}
	}
	return sb.String()
}

func renderLocatorItem(idx int, r SearchResult) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("---\nResult [%d] (Score: %.4f) [Locator only]\n", idx, r.Score))
	if metaSource, ok := r.Metadata["source"]; ok {
		sb.WriteString(fmt.Sprintf("Source: %v\n", metaSource))
	}
	if idxName, ok := r.Metadata["_index"].(string); ok {
		sb.WriteString(fmt.Sprintf("Index: %s\n", idxName))
	}
	return sb.String()
}
