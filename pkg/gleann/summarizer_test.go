package gleann

import (
	"strings"
	"testing"
)

func TestExtractSummary(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSubstr []string
		wantEmpty  bool
	}{
		{
			name: "Case 1: Pure Technical Markdown",
			input: `
# Gleann Server
![Build Status](https://badge)

Inspired by the academic excellence of the Leann RAG backend, engineered for daily terminal use.
Gleann provides a robust semantic search interface over your code and documents.

## Features
- [x] TUI (Terminal User Interface)
- [x] Code Graph mapping via AST

Run the following command:
` + "```" + `bash
gleann start
` + "```" + `
This tool completely changes how you search.
`,
			// It should ignore the lists, headers, and code blocks, and extract the english sentences.
			wantSubstr: []string{
				"Inspired by the academic excellence of the Leann RAG backend, engineered for daily terminal use.",
				"Gleann provides a robust semantic search interface over your code and documents.",
				"This tool completely changes how you search.",
			},
			wantEmpty: false,
		},
		{
			name: "Case 2: Source Code File",
			input: `
package main

import (
	"fmt"
	"os"
)

// main is the entry point
func main() {
	// Let's print hello world
	fmt.Println("Hello world")
}
`,
			wantEmpty: true,
		},
		{
			name: "Case 3: Noise Mixed with High-Density Information",
			input: `
Company Confidential 2024. Company Confidential 2024. Company Confidential 2024.
Do not distribute. Do not distribute. Do not distribute.

Voice User Interface (VUI) module successfully integrated.
We achieved a completely functional Voice User Interface with 90% success rate on the test sets.
The module perfectly handles offline voice commands without internet access.

Company Confidential 2024.
`,
			wantSubstr: []string{
				"We achieved a completely functional Voice User Interface with 90% success rate on the test sets.",
				"The module perfectly handles offline voice commands without internet access.",
			},
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSummary(tt.input)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("ExtractSummary() = %q, want empty string", got)
				}
				return
			}
			for _, sub := range tt.wantSubstr {
				if !strings.Contains(got, sub) {
					t.Errorf("ExtractSummary() missing expected sentence. \nGot: %q\nWant substr: %q", got, sub)
				}
			}
		})
	}
}
