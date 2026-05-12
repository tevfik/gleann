package gleann

import "unicode/utf8"

// EstimateTokens returns a rough token estimate for text.
// Uses the common ~4 chars/token heuristic for English text.
// For code, the ratio is closer to ~3.5 chars/token, but 4 is a
// conservative default that avoids over-counting savings.
func EstimateTokens(text string) int {
	n := utf8.RuneCountInString(text)
	if n == 0 {
		return 0
	}
	// ~4 chars per token is a widely accepted heuristic
	return (n + 3) / 4
}

// TokenGain tracks cumulative token savings from compression.
type TokenGain struct {
	RawTokens        int     `json:"raw_tokens"`
	CompressedTokens int     `json:"compressed_tokens"`
	SavedTokens      int     `json:"saved_tokens"`
	Calls            int     `json:"calls"`
	Ratio            float64 `json:"ratio"` // 0.0 – 1.0, higher = more saved
}

// Add records a single compression event.
func (g *TokenGain) Add(rawTokens, compTokens int) {
	g.RawTokens += rawTokens
	g.CompressedTokens += compTokens
	g.SavedTokens += rawTokens - compTokens
	g.Calls++
	if g.RawTokens > 0 {
		g.Ratio = float64(g.SavedTokens) / float64(g.RawTokens)
	}
}
