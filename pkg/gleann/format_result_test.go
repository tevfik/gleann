package gleann

import (
	"strings"
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
		{
			name: "result with hierarchical breadcrumb",
			result: SearchResult{
				Text: "Breadcrumb context snippet.",
				GraphContext: &GraphContextInfo{
					DocumentContext: &DocumentContextData{
						Name:       "architecture.md",
						FolderName: "docs",
						Breadcrumb: "docs > architecture.md > Storage > KuzuDB",
						Summary:    "Detailed overview of KuzuDB storage.",
					},
				},
			},
			idx: 7,
			expected: "[7]\n" +
				"Location: docs > architecture.md > Storage > KuzuDB\n" +
				"Summary: Detailed overview of KuzuDB storage.\n" +
				"Content:\n" +
				"Breadcrumb context snippet.",
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

func TestFormatResult_ASTContextCompression(t *testing.T) {
	codeSnippet := `package sample

type Engine struct {
	ID string
}

func NewEngine(id string) *Engine {
	// A long function implementation that would consume many tokens
	e := &Engine{ID: id}
	for i := 0; i < 10; i++ {
		e.ID += fmt.Sprintf("-%d", i)
	}
	return e
}

func (e *Engine) Run() error {
	// Another long function implementation with multiple lines of logic
	if e.ID == "" {
		return errors.New("empty id")
	}
	return nil
}`

	res := SearchResult{
		Text: codeSnippet,
		Metadata: map[string]any{
			"source": "pkg/engine/engine.go",
		},
		GraphContext: &GraphContextInfo{
			Symbols: []SymbolNeighbors{
				{
					FQN:     "sample.Engine",
					Kind:    "struct",
					Callers: []string{"main.Init"},
				},
			},
		},
	}

	// 1. Primary result (idx=1) with compression enabled: should remain FULL body
	primary := formatResultWithOptions(res, 1, true)
	if !strings.Contains(primary, "func NewEngine(id string) *Engine {") || !strings.Contains(primary, "for i := 0; i < 10; i++ {") {
		t.Errorf("expected primary result (idx=1) to keep full body, got:\n%s", primary)
	}
	if strings.Contains(primary, "[AST Context: Signatures Only]") {
		t.Errorf("primary result should not be marked as signatures only")
	}

	// 2. Secondary result (idx=2) with compression enabled: should compress to signatures
	secondary := formatResultWithOptions(res, 2, true)
	if !strings.Contains(secondary, "[AST Context: Signatures Only]") {
		t.Errorf("expected secondary result to be marked as signatures only, got:\n%s", secondary)
	}
	if !strings.Contains(secondary, "Signatures & Interface:") {
		t.Errorf("expected Signatures & Interface header, got:\n%s", secondary)
	}
	if !strings.Contains(secondary, "func NewEngine(id string) *Engine") {
		t.Errorf("expected signature of NewEngine in compressed output")
	}
	if strings.Contains(secondary, "for i := 0; i < 10; i++ {") {
		t.Errorf("expected function body to be stripped in compressed output, but found it:\n%s", secondary)
	}

	// 3. Secondary result (idx=2) with compression disabled: should remain FULL body
	uncompressed := formatResultWithOptions(res, 2, false)
	if strings.Contains(uncompressed, "[AST Context: Signatures Only]") {
		t.Errorf("expected uncompressed result when compress=false")
	}
	if !strings.Contains(uncompressed, "for i := 0; i < 10; i++ {") {
		t.Errorf("expected full body when compress=false")
	}
}
