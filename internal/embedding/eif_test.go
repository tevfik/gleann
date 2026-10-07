//go:build eif

package embedding

import (
	"context"
	"math"
	"testing"
	"time"
)

func cosineSimilarity(a, b []float32) float32 {
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

func TestEIFComputer(t *testing.T) {
	comp := NewComputer(Options{
		Provider: ProviderEIF,
		Model:    "minilm_bert.eifm",
	})

	ctx := context.Background()
	texts := []string{
		"function to sort an array of integers using quicksort",
		"algorithm to order a list of numbers efficiently",
		"delicious recipe for baking fresh Italian pizza dough",
	}

	t0 := time.Now()
	embeddings, err := comp.Compute(ctx, texts)
	elapsed := time.Since(t0)

	if err != nil {
		t.Fatalf("EIF Compute failed: %v", err)
	}

	if len(embeddings) != 3 {
		t.Fatalf("expected 3 embeddings, got %d", len(embeddings))
	}

	for i, emb := range embeddings {
		if len(emb) != 384 {
			t.Errorf("text %d: expected dim 384, got %d", i, len(emb))
		}
	}

	sim12 := cosineSimilarity(embeddings[0], embeddings[1])
	sim13 := cosineSimilarity(embeddings[0], embeddings[2])
	sim23 := cosineSimilarity(embeddings[1], embeddings[2])

	t.Logf("Computed %d embeddings in %v (avg %.2f ms/emb)", len(texts), elapsed, float64(elapsed.Milliseconds())/float64(len(texts)))
	t.Logf("Sim(1, 2) [Sorting vs Ordering]: %.4f", sim12)
	t.Logf("Sim(1, 3) [Sorting vs Pizza]:    %.4f", sim13)
	t.Logf("Sim(2, 3) [Ordering vs Pizza]:   %.4f", sim23)

	if sim12 <= sim13 || sim12 <= sim23 {
		t.Errorf("Semantic clustering check failed: sim12=%.4f should be greater than sim13=%.4f and sim23=%.4f", sim12, sim13, sim23)
	}
}
