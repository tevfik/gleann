//go:build native

package embedding

import (
	"context"
	"testing"
)

func TestNativeComputer_ComputeSingle(t *testing.T) {
	c := NewNativeComputer("mock-native-model")

	ctx := context.Background()
	text := "hello world"

	emb, err := c.ComputeSingle(ctx, text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(emb) != 384 {
		t.Errorf("expected length 384, got %d", len(emb))
	}
}

func TestNativeComputer_Compute(t *testing.T) {
	c := NewNativeComputer("mock-native-model")

	ctx := context.Background()
	texts := []string{"test 1", "test 22", "test 333"}

	embs, err := c.Compute(ctx, texts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(embs) != len(texts) {
		t.Errorf("expected %d embeddings, got %d", len(texts), len(embs))
	}
}
