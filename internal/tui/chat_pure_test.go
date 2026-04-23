package tui

import (
	"testing"
)

// ── truncStr ──────────────────────────────────────────────────

func TestTruncStr_Short(t *testing.T) {
	if got := truncStr("hi", 10); got != "hi" {
		t.Errorf("expected 'hi', got %q", got)
	}
}

func TestTruncStr_Exact(t *testing.T) {
	if got := truncStr("abcde", 5); got != "abcde" {
		t.Errorf("expected 'abcde', got %q", got)
	}
}

func TestTruncStr_Long(t *testing.T) {
	got := truncStr("hello world", 8)
	if got != "hello..." {
		t.Errorf("expected 'hello...', got %q", got)
	}
}

func TestTruncStr_VerySmallMaxLen(t *testing.T) {
	got := truncStr("hello world", 3)
	if got != "..." {
		t.Errorf("expected '...', got %q", got)
	}
}

func TestTruncStr_ZeroMaxLen(t *testing.T) {
	got := truncStr("hello", 0)
	if got != "..." {
		t.Errorf("expected '...', got %q", got)
	}
}

// ── estimateContextLimit ──────────────────────────────────────

func TestEstimateContextLimit_GPT4o(t *testing.T) {
	if got := estimateContextLimit("gpt-4o-mini"); got != 128000 {
		t.Errorf("expected 128000, got %d", got)
	}
}

func TestEstimateContextLimit_GPT4Turbo(t *testing.T) {
	if got := estimateContextLimit("gpt-4-turbo"); got != 128000 {
		t.Errorf("expected 128000, got %d", got)
	}
}

func TestEstimateContextLimit_GPT4(t *testing.T) {
	if got := estimateContextLimit("gpt-4"); got != 8192 {
		t.Errorf("expected 8192, got %d", got)
	}
}

func TestEstimateContextLimit_GPT35(t *testing.T) {
	if got := estimateContextLimit("gpt-3.5-turbo"); got != 16384 {
		t.Errorf("expected 16384, got %d", got)
	}
}

func TestEstimateContextLimit_Claude(t *testing.T) {
	if got := estimateContextLimit("claude-3-opus"); got != 200000 {
		t.Errorf("expected 200000, got %d", got)
	}
}

func TestEstimateContextLimit_Llama3(t *testing.T) {
	if got := estimateContextLimit("llama3:8b"); got != 8192 {
		t.Errorf("expected 8192, got %d", got)
	}
}

func TestEstimateContextLimit_Mistral(t *testing.T) {
	if got := estimateContextLimit("mistral:7b"); got != 32768 {
		t.Errorf("expected 32768, got %d", got)
	}
}

func TestEstimateContextLimit_Deepseek(t *testing.T) {
	if got := estimateContextLimit("deepseek-coder"); got != 128000 {
		t.Errorf("expected 128000, got %d", got)
	}
}

func TestEstimateContextLimit_Unknown(t *testing.T) {
	if got := estimateContextLimit("some-random-model"); got != 0 {
		t.Errorf("expected 0 for unknown, got %d", got)
	}
}

// ── findClosestFloat ──────────────────────────────────────────

func TestFindClosestFloat_Exact(t *testing.T) {
	presets := []float64{0.1, 0.5, 0.7, 1.0}
	if got := findClosestFloat(presets, 0.5); got != 1 {
		t.Errorf("expected index 1, got %d", got)
	}
}

func TestFindClosestFloat_Between(t *testing.T) {
	presets := []float64{0.1, 0.5, 0.9}
	if got := findClosestFloat(presets, 0.6); got != 1 {
		t.Errorf("expected closest to 0.5 (idx 1), got %d", got)
	}
}

func TestFindClosestFloat_First(t *testing.T) {
	presets := []float64{0.1, 0.5, 0.9}
	if got := findClosestFloat(presets, 0.0); got != 0 {
		t.Errorf("expected index 0, got %d", got)
	}
}

func TestFindClosestFloat_Last(t *testing.T) {
	presets := []float64{0.1, 0.5, 0.9}
	if got := findClosestFloat(presets, 1.0); got != 2 {
		t.Errorf("expected index 2, got %d", got)
	}
}

// ── findClosestInt ────────────────────────────────────────────

func TestFindClosestInt_Exact(t *testing.T) {
	presets := []int{256, 512, 1024, 2048}
	if got := findClosestInt(presets, 512); got != 1 {
		t.Errorf("expected index 1, got %d", got)
	}
}

func TestFindClosestInt_Between(t *testing.T) {
	presets := []int{256, 512, 1024, 2048}
	if got := findClosestInt(presets, 700); got != 1 {
		t.Errorf("expected closest to 512 (idx 1), got %d", got)
	}
}

func TestFindClosestInt_CloserToHigher(t *testing.T) {
	presets := []int{256, 512, 1024, 2048}
	if got := findClosestInt(presets, 900); got != 2 {
		t.Errorf("expected closest to 1024 (idx 2), got %d", got)
	}
}

// ── intAbs ────────────────────────────────────────────────────

func TestIntAbs_Positive(t *testing.T) {
	if got := intAbs(5); got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

func TestIntAbs_Negative(t *testing.T) {
	if got := intAbs(-5); got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

func TestIntAbs_Zero(t *testing.T) {
	if got := intAbs(0); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

// ── extractScript ─────────────────────────────────────────────

func TestExtractScript_GenericFenceWithShebang(t *testing.T) {
	text := "```\n#!/bin/bash\necho test\n```"
	got := extractScript(text)
	if got != "#!/bin/bash\necho test" {
		t.Errorf("expected shebang script, got %q", got)
	}
}

func TestExtractScript_ShebangNaked(t *testing.T) {
	text := "#!/bin/bash\necho hello"
	got := extractScript(text)
	if got != "#!/bin/bash\necho hello" {
		t.Errorf("expected naked shebang script, got %q", got)
	}
}
