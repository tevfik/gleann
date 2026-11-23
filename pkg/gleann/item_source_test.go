package gleann

import (
	"testing"
)

// TestItemSourceMatchesSplitItemMeta verifies that itemSource extracts the same
// source string as splitItemMeta without marshalling the remaining metadata to
// JSON. S1 relies on this for grouping items by source and language guessing.
func TestItemSourceMatchesSplitItemMeta(t *testing.T) {
	cases := []struct {
		name string
		md   map[string]any
	}{
		{name: "nil", md: nil},
		{name: "empty", md: map[string]any{}},
		{name: "simple source", md: map[string]any{"source": "main.go"}},
		{name: "nested path", md: map[string]any{"source": "pkg/gleann/builder.go"}},
		{name: "with chunk_index and lang", md: map[string]any{"source": "internal/store/store.go", "chunk_index": 4, "lang": "go"}},
		{name: "non-string source", md: map[string]any{"source": 12345}},
		{name: "missing source key", md: map[string]any{"author": "alice", "lines": 42}},
		{name: "unserialisable other field", md: map[string]any{"source": "chan.go", "ch": make(chan int)}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, _ := splitItemMeta(tc.md)
			got := itemSource(tc.md)
			if got != want {
				t.Fatalf("itemSource(%v) = %q, want %q (from splitItemMeta)", tc.md, got, want)
			}
		})
	}
}
