package store

import (
	"encoding/hex"
	"testing"
)

func TestPassagePID(t *testing.T) {
	p := PassagePID("a.go", "hello world", 0)
	if len(p) != 32 {
		t.Fatalf("pid length = %d, want 32", len(p))
	}
	if _, err := hex.DecodeString(p); err != nil {
		t.Fatalf("pid %q is not hex: %v", p, err)
	}
	if p != PassagePID("a.go", "hello world", 0) {
		t.Error("pid is not deterministic")
	}
	if p == PassagePID("b.go", "hello world", 0) {
		t.Error("pid ignores the source")
	}
	if p == PassagePID("a.go", "hello worlds", 0) {
		t.Error("pid ignores the text")
	}
	if p == PassagePID("a.go", "hello world", 1) {
		t.Error("pid ignores dup_index")
	}
}
