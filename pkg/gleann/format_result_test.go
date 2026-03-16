package gleann

import (
	"testing"
)

func TestFormatResult(t *testing.T) {
	tests := []struct {
		name     string
		result   SearchResult
		idx      int
		expected string
	}{
		{
			name: "minimal result",
			result: SearchResult{
				Text: "This is a test context.",
			},
			idx: 1,
			expected: "[1]\n" +
				"Content:\n" +
				"This is a test context.",
		},
		{
			name: "result with source metadata",
			result: SearchResult{
				Text: "This is a test context.",
				Metadata: map[string]any{
					"source": "test_file.txt",
				},
			},
			idx: 2,
			expected: "[2] (source: test_file.txt)\n" +
				"Content:\n" +
				"This is a test context.",
		},
		{
			name: "result with document context",
			result: SearchResult{
				Text: "This is a test context.",
				GraphContext: &GraphContextInfo{
					DocumentContext: &DocumentContextData{
						Name:       "test_doc",
						FolderName: "test_folder",
						Summary:    "This is a test document summary.",
					},
				},
			},
			idx: 3,
			expected: "[3]\n" +
				"Document: test_doc | Folder: test_folder\n" +
				"Summary: This is a test document summary.\n" +
				"Content:\n" +
				"This is a test context.",
		},
		{
			name: "result with symbols context",
			result: SearchResult{
				Text: "This is a test context.",
				GraphContext: &GraphContextInfo{
					Symbols: []SymbolNeighbors{
						{
							FQN:     "pkg.FuncA",
							Kind:    "function",
							Callers: []string{"pkg.FuncB"},
							Callees: []string{"pkg.FuncC"},
						},
					},
				},
			},
			idx: 4,
			expected: "[4]\n" +
				"Code Context:\n" +
				"- Symbol: pkg.FuncA (function)\n" +
				"  Callers: pkg.FuncB\n" +
				"  Callees: pkg.FuncC\n" +
				"Content:\n" +
				"This is a test context.",
		},
		{
			name: "result with multiple symbols and missing callers/callees",
			result: SearchResult{
				Text: "This is a test context.",
				GraphContext: &GraphContextInfo{
					Symbols: []SymbolNeighbors{
						{
							FQN:  "pkg.TypeA",
							Kind: "struct",
						},
						{
							FQN:     "pkg.FuncD",
							Kind:    "method",
							Callers: []string{"pkg.FuncE", "pkg.FuncF"},
						},
					},
				},
			},
			idx: 5,
			expected: "[5]\n" +
				"Code Context:\n" +
				"- Symbol: pkg.TypeA (struct)\n" +
				"- Symbol: pkg.FuncD (method)\n" +
				"  Callers: pkg.FuncE, pkg.FuncF\n" +
				"Content:\n" +
				"This is a test context.",
		},
		{
			name: "combined full result",
			result: SearchResult{
				Text: "This is a test context.",
				Metadata: map[string]any{
					"source": "full_test.go",
				},
				GraphContext: &GraphContextInfo{
					DocumentContext: &DocumentContextData{
						Name:       "full_test.go",
						FolderName: "pkg/gleann",
						Summary:    "Full test file summary.",
					},
					Symbols: []SymbolNeighbors{
						{
							FQN:     "gleann.TestFunc",
							Kind:    "function",
							Callees: []string{"gleann.HelperFunc"},
						},
					},
				},
			},
			idx: 6,
			expected: "[6] (source: full_test.go)\n" +
				"Document: full_test.go | Folder: pkg/gleann\n" +
				"Summary: Full test file summary.\n" +
				"Code Context:\n" +
				"- Symbol: gleann.TestFunc (function)\n" +
				"  Callees: gleann.HelperFunc\n" +
				"Content:\n" +
				"This is a test context.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := formatResult(tt.result, tt.idx)
			if actual != tt.expected {
				t.Errorf("formatResult() = %q, want %q", actual, tt.expected)
			}
		})
	}
}
