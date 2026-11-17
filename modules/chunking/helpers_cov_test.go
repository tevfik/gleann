package chunking

import (
	"go/parser"
	"strings"
	"testing"
)

func TestJoinLines_ClampsRange(t *testing.T) {
	lines := []string{"a", "b", "c"}
	tests := []struct {
		name       string
		start, end int
		want       string
	}{
		{"negative start is clamped", -5, 2, "a\nb"},
		{"end beyond slice is clamped", 1, 99, "b\nc"},
		{"empty range", 2, 2, ""},
		{"inverted range", 3, 1, ""},
	}
	for _, tc := range tests {
		if got := joinLines(lines, tc.start, tc.end); got != tc.want {
			t.Errorf("%s: joinLines = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestExprName(t *testing.T) {
	tests := []struct {
		src, want string
	}{
		{"T", "T"},
		{"*T", "T"},
		{"pkg.T", "pkg.T"},
		{"*pkg.T", "pkg.T"},
		{"[]int", ""}, // not a type name expression
	}
	for _, tc := range tests {
		expr, err := parser.ParseExpr(tc.src)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		if got := exprName(expr); got != tc.want {
			t.Errorf("exprName(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestChunkWithMetadata_FQNAndSignatureEdgeCases(t *testing.T) {
	c := NewASTChunker(DefaultASTChunkerConfig())

	// Unrecognised extension: the sliding-window chunk is named block_N and
	// the fully-qualified name is "<file>:<name>".
	items := c.ChunkWithMetadata("just some prose\n", map[string]any{"file": "docs/readme.xyz"})
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Metadata["fqn"] != "docs/readme.xyz:block_1" || items[0].Metadata["name"] != "block_1" {
		t.Errorf("fqn=%v name=%v", items[0].Metadata["fqn"], items[0].Metadata["name"])
	}

	// A first line longer than 120 characters is truncated in the signature.
	long := "def f(" + strings.Repeat("a, ", 80) + "z):\n    pass\n"
	items = c.ChunkWithMetadata(long, map[string]any{"file": "m.py"})
	sig, _ := items[0].Metadata["signature"].(string)
	if len(sig) != 120 {
		t.Errorf("signature length = %d, want 120", len(sig))
	}
}
