package chunking

import (
	"strings"
	"testing"
)

func TestSplitOversize_NonPositiveLimitsAreClamped(t *testing.T) {
	// Limits below 1 are raised to 1 word / 1 rune, so every piece is tiny
	// but no input is lost and nothing loops forever.
	parts := splitOversize("ab cd", 0, 0)
	if len(parts) < 2 {
		t.Fatalf("parts = %q, want the text cut into several pieces", parts)
	}
	if got := strings.ReplaceAll(strings.Join(parts, ""), " ", ""); got != "abcd" {
		t.Errorf("rejoined text = %q, want %q (no characters lost)", got, "abcd")
	}
}

func TestGetOverlapText_ZeroAndCJKBudget(t *testing.T) {
	if got := getOverlapText("some words here", 0); got != "" {
		t.Errorf("zero overlap = %q, want empty", got)
	}
	// CJK text with fewer tokens than the overlap budget is returned whole.
	short := "你好世界"
	if got := getOverlapText(short, 50); got != short {
		t.Errorf("short CJK overlap = %q, want the whole text", got)
	}
	// With more tokens than the budget only a suffix is kept.
	long := strings.Repeat("你好世界", 20)
	got := getOverlapText(long, 3)
	if got == "" || len(got) >= len(long) || !strings.HasSuffix(long, got) {
		t.Errorf("long CJK overlap = %q, want a proper non-empty suffix", got)
	}
}
