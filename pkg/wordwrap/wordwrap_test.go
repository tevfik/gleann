package wordwrap

import (
	"strings"
	"testing"
)

func TestWrap_Basic(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		width int
		want  string
	}{
		{
			name:  "short text no wrap",
			text:  "hello world",
			width: 80,
			want:  "hello world",
		},
		{
			name:  "wraps at word boundary",
			text:  "hello world foo bar",
			width: 11,
			want:  "hello world\nfoo bar",
		},
		{
			name:  "preserves existing newlines",
			text:  "line one\nline two",
			width: 80,
			want:  "line one\nline two",
		},
		{
			name:  "zero width passthrough",
			text:  "hello world",
			width: 0,
			want:  "hello world",
		},
		{
			name:  "negative width passthrough",
			text:  "hello world",
			width: -1,
			want:  "hello world",
		},
		{
			name:  "single long word",
			text:  "superlongword",
			width: 5,
			want:  "superlongword",
		},
		{
			name:  "empty string",
			text:  "",
			width: 80,
			want:  "",
		},
		{
			name:  "multiple spaces preserved short lines",
			text:  "a  b  c",
			width: 80,
			want:  "a  b  c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Wrap(tt.text, tt.width)
			if got != tt.want {
				t.Errorf("Wrap(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.want)
			}
		})
	}
}

func TestWrap_MultiLine(t *testing.T) {
	text := "first line is short\nsecond line is also short\nthird line is really really long and should wrap"
	got := Wrap(text, 30)

	lines := strings.Split(got, "\n")
	for i, line := range lines {
		if len(line) > 30 {
			t.Errorf("line %d exceeds width 30: %q (len=%d)", i, line, len(line))
		}
	}
}

func TestStreamWriter_Passthrough(t *testing.T) {
	var buf strings.Builder
	w := NewStreamWriter(0, func(s string) {
		buf.WriteString(s)
	})

	w.Write("hello ")
	w.Write("world")
	w.Flush()

	if got := buf.String(); got != "hello world" {
		t.Errorf("passthrough: got %q, want %q", got, "hello world")
	}
}

func TestStreamWriter_WrapsAtWidth(t *testing.T) {
	var buf strings.Builder
	w := NewStreamWriter(10, func(s string) {
		buf.WriteString(s)
	})

	// Write tokens that should wrap at width 10.
	w.Write("hello ")
	w.Write("world ")
	w.Write("foo")
	w.Flush()

	got := buf.String()
	// Should contain a newline when column exceeds width.
	if !strings.Contains(got, "\n") {
		t.Errorf("expected wrap newline in output: %q", got)
	}
}

func TestStreamWriter_PreservesNewlines(t *testing.T) {
	var buf strings.Builder
	w := NewStreamWriter(80, func(s string) {
		buf.WriteString(s)
	})

	w.Write("line1\nline2")
	w.Flush()

	got := buf.String()
	if !strings.Contains(got, "\n") {
		t.Errorf("expected newline preserved in output: %q", got)
	}
}

func TestTerminalWidth_Fallback(t *testing.T) {
	// In a test environment, terminal detection may fail.
	// TerminalWidth should return the fallback.
	w := TerminalWidth(120)
	if w <= 0 {
		t.Errorf("TerminalWidth should return positive value, got %d", w)
	}
}
