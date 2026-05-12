package gleann

import "testing"

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		text   string
		expect int
	}{
		{"", 0},
		{"hi", 1},       // 2 chars → ceil(2/4) = 1
		{"hello world", 3}, // 11 chars → ceil(11/4) = 3
		{"a", 1},
	}
	for _, tt := range tests {
		got := EstimateTokens(tt.text)
		if got != tt.expect {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.text, got, tt.expect)
		}
	}
}

func TestEstimateTokensUnicode(t *testing.T) {
	// Unicode characters count as 1 rune each
	text := "merhaba dünya" // Turkish, 13 Unicode code points
	tokens := EstimateTokens(text)
	if tokens <= 0 {
		t.Error("expected positive token count for Unicode text")
	}
}

func TestTokenGainAdd(t *testing.T) {
	g := &TokenGain{}
	g.Add(1000, 300)

	if g.RawTokens != 1000 {
		t.Errorf("RawTokens = %d, want 1000", g.RawTokens)
	}
	if g.CompressedTokens != 300 {
		t.Errorf("CompressedTokens = %d, want 300", g.CompressedTokens)
	}
	if g.SavedTokens != 700 {
		t.Errorf("SavedTokens = %d, want 700", g.SavedTokens)
	}
	if g.Calls != 1 {
		t.Errorf("Calls = %d, want 1", g.Calls)
	}
	if g.Ratio < 0.69 || g.Ratio > 0.71 {
		t.Errorf("Ratio = %f, want ~0.7", g.Ratio)
	}
}

func TestTokenGainMultipleAdds(t *testing.T) {
	g := &TokenGain{}
	g.Add(100, 50)
	g.Add(200, 80)
	g.Add(300, 120)

	if g.RawTokens != 600 {
		t.Errorf("RawTokens = %d, want 600", g.RawTokens)
	}
	if g.CompressedTokens != 250 {
		t.Errorf("CompressedTokens = %d, want 250", g.CompressedTokens)
	}
	if g.Calls != 3 {
		t.Errorf("Calls = %d, want 3", g.Calls)
	}
}

func TestTokenGainZero(t *testing.T) {
	g := &TokenGain{}
	g.Add(0, 0)

	if g.Ratio != 0 {
		t.Errorf("zero tokens should have ratio 0, got %f", g.Ratio)
	}
}
