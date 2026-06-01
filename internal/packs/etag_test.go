package packs

import (
	"strings"
	"testing"
)

func TestETagHasher(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  string // expected ETag value
	}{
		{
			name:  "empty input",
			input: nil,
			want:  `"e3b0c44298fc"`, // sha256 of empty = e3b0c44298fc...
		},
		{
			name:  "hello",
			input: []byte("hello"),
			want:  `"2cf24dba5fb0"`, // sha256("hello") prefix
		},
		{
			name:  "deterministic",
			input: []byte("same input twice"),
			want:  "", // checked separately
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newETagHasher()
			if tt.input != nil {
				h.Write(tt.input)
			}
			got := h.String()

			// Verify format: quoted, 12 hex chars inside quotes.
			if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
				t.Fatalf("ETag not quoted: %s", got)
			}
			inner := got[1 : len(got)-1]
			if len(inner) != 12 {
				t.Fatalf("expected 12 hex chars, got %d: %s", len(inner), inner)
			}

			if tt.want != "" && got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}

	// Determinism check.
	h1 := newETagHasher()
	h1.Write([]byte("test"))
	h2 := newETagHasher()
	h2.Write([]byte("test"))
	if h1.String() != h2.String() {
		t.Errorf("non-deterministic: %s != %s", h1.String(), h2.String())
	}

	// Different inputs produce different ETags.
	h3 := newETagHasher()
	h3.Write([]byte("a"))
	h4 := newETagHasher()
	h4.Write([]byte("b"))
	if h3.String() == h4.String() {
		t.Error("different inputs produced same ETag")
	}
}
