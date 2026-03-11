package main

import (
	"testing"
	"time"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		max    int
		expect string
	}{
		{"short", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"long", "hello world this is a long string", 10, "hello w..."},
		{"newlines", "line1\nline2\nline3", 40, "line1 line2 line3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.input, tt.max)
			if got != tt.expect {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expect)
			}
		})
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		name   string
		dur    time.Duration
		expect string
	}{
		{"seconds", 45 * time.Second, "45s"},
		{"minutes", 15 * time.Minute, "15m"},
		{"hours", 5 * time.Hour, "5h"},
		{"days", 72 * time.Hour, "3d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAge(tt.dur)
			if got != tt.expect {
				t.Errorf("formatAge(%v) = %q, want %q", tt.dur, got, tt.expect)
			}
		})
	}
}

func TestParseFriendlyDuration(t *testing.T) {
	tests := []struct {
		input  string
		expect time.Duration
		errMsg string
	}{
		{"7d", 7 * 24 * time.Hour, ""},
		{"30d", 30 * 24 * time.Hour, ""},
		{"2w", 14 * 24 * time.Hour, ""},
		{"1w", 7 * 24 * time.Hour, ""},
		{"xyz", 0, "unsupported duration"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseFriendlyDuration(tt.input)
			if tt.errMsg != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expect {
				t.Errorf("parseFriendlyDuration(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestGetDeleteArgs(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		expect []string
	}{
		{"single", []string{"--delete", "abc123"}, []string{"abc123"}},
		{"multiple", []string{"--delete", "abc", "--delete", "def"}, []string{"abc", "def"}},
		{"none", []string{"--list"}, nil},
		{"missing_value", []string{"--delete"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getDeleteArgs(tt.args)
			if len(got) != len(tt.expect) {
				t.Errorf("getDeleteArgs(%v) = %v, want %v", tt.args, got, tt.expect)
				return
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("getDeleteArgs(%v)[%d] = %q, want %q", tt.args, i, got[i], tt.expect[i])
				}
			}
		})
	}
}
