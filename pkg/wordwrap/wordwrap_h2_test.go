package wordwrap

import (
	"strings"
	"testing"
)

func TestWrap_WhitespaceOnlyLongLine(t *testing.T) {
	// A line longer than the width but with no words collapses to empty.
	if got := Wrap("ok\n"+strings.Repeat(" ", 20)+"\nend", 5); got != "ok\n\nend" {
		t.Fatalf("Wrap = %q", got)
	}
}

func TestStreamWriter_BreaksAfterSpaceReachingWidth(t *testing.T) {
	var out strings.Builder
	w := NewStreamWriter(3, func(s string) { out.WriteString(s) })
	// "ab" fills two columns; the space reaches the width and ends the line.
	w.Write("ab cd")
	w.Flush()
	if got := out.String(); got != "ab \ncd" {
		t.Fatalf("stream output = %q", got)
	}
}
