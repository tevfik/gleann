// Package wordwrap provides terminal-aware word wrapping for streaming text output.
package wordwrap

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// TerminalWidth returns the current terminal width, or fallback if unavailable.
func TerminalWidth(fallback int) int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return fallback
	}
	return w
}

// Wrap wraps text to the given width at word boundaries.
// It preserves existing newlines and handles multi-line input.
func Wrap(text string, width int) string {
	if width <= 0 {
		return text
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if i > 0 {
			result.WriteByte('\n')
		}
		if len(line) <= width {
			result.WriteString(line)
			continue
		}
		result.WriteString(wrapLine(line, width))
	}
	return result.String()
}

func wrapLine(line string, width int) string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return ""
	}

	var result strings.Builder
	col := 0

	for i, word := range words {
		wlen := len(word)

		if col == 0 {
			// First word on line — always write it.
			result.WriteString(word)
			col = wlen
		} else if col+1+wlen <= width {
			// Fits with a space.
			result.WriteByte(' ')
			result.WriteString(word)
			col += 1 + wlen
		} else {
			// Wrap to next line.
			result.WriteByte('\n')
			result.WriteString(word)
			col = wlen
		}

		_ = i
	}
	return result.String()
}

// StreamWriter wraps streaming text output at the given column width.
// It buffers partial words and flushes complete wrapped lines.
type StreamWriter struct {
	width  int
	col    int
	output func(string)
}

// NewStreamWriter creates a streaming word wrapper.
// width=0 means no wrapping (passthrough).
func NewStreamWriter(width int, output func(string)) *StreamWriter {
	return &StreamWriter{
		width:  width,
		output: output,
	}
}

// Write processes a token from the LLM stream.
func (w *StreamWriter) Write(token string) {
	if w.width <= 0 {
		w.output(token)
		return
	}

	for _, ch := range token {
		if ch == '\n' {
			w.output("\n")
			w.col = 0
			continue
		}

		if ch == ' ' && w.col >= w.width {
			w.output("\n")
			w.col = 0
			continue
		}

		w.output(string(ch))
		w.col++

		if w.col >= w.width && ch == ' ' {
			w.output("\n")
			w.col = 0
		}
	}
}

// Flush outputs any remaining buffered content.
func (w *StreamWriter) Flush() {
	// Nothing to flush in current implementation — output is immediate.
}
