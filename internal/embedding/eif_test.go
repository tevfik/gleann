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

func testEmbeddingWithModel(t *testing.T, modelName string) {
	comp := NewComputer(Options{
		Provider: ProviderEIF,
		Model:    modelName,
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
		t.Fatalf("[%s] EIF Compute failed: %v", modelName, err)
	}

	if len(embeddings) != 3 {
		t.Fatalf("[%s] expected 3 embeddings, got %d", modelName, len(embeddings))
	}

	for i, emb := range embeddings {
		if len(emb) != 384 {
			t.Errorf("[%s] text %d: expected dim 384, got %d", modelName, i, len(emb))
		}
	}

	sim12 := cosineSimilarity(embeddings[0], embeddings[1])
	sim13 := cosineSimilarity(embeddings[0], embeddings[2])
	sim23 := cosineSimilarity(embeddings[1], embeddings[2])

	t.Logf("[%s] Computed %d embeddings in %v (avg %.2f ms/emb)",
		modelName, len(texts), elapsed, float64(elapsed.Milliseconds())/float64(len(texts)))
	t.Logf("[%s] Sim(1, 2) [Sorting vs Ordering]: %.4f", modelName, sim12)
	t.Logf("[%s] Sim(1, 3) [Sorting vs Pizza]:    %.4f", modelName, sim13)
	t.Logf("[%s] Sim(2, 3) [Ordering vs Pizza]:   %.4f", modelName, sim23)

	if sim12 <= sim13 || sim12 <= sim23 {
		t.Errorf("[%s] Semantic clustering check failed: sim12=%.4f should be greater than sim13=%.4f and sim23=%.4f",
			modelName, sim12, sim13, sim23)
	}
}

func TestEIFComputerGGUF(t *testing.T) {
	testEmbeddingWithModel(t, "minilm.gguf")
}

func TestEIFComputerEIFM(t *testing.T) {
	testEmbeddingWithModel(t, "minilm_bert.eifm")
}

func TestEIFComputerMultilingualE5(t *testing.T) {
	testEmbeddingWithModel(t, "multilingual-e5-small-q8_0.gguf")
}
