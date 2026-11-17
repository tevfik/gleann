package embedding

import (
	"context"
	"testing"
)

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// The mock provider must give each text its own deterministic vector. A
// constant vector made every vector distance equal, so search order was
// arbitrary and an E2E sync-then-search check failed about one run in ten.
func TestMockVectorsDependOnText(t *testing.T) {
	c := &Computer{}
	vecs, err := c.computeMock(context.Background(), []string{
		"The axolotl gateway was added later.",
		"The axolotl gateway was added later.",
		"axolotl gateway",
		"connection pool acquire timeout",
		"",
	})
	if err != nil {
		t.Fatal(err)
	}
	same, near, far := dot(vecs[0], vecs[1]), dot(vecs[0], vecs[2]), dot(vecs[0], vecs[3])
	if same < 0.999 {
		t.Errorf("same text gives different vectors: cosine %f", same)
	}
	if !(near > far) {
		t.Errorf("text sharing words should be closer: shared %f, unrelated %f", near, far)
	}
	var nonZero bool
	for _, v := range vecs[4] {
		if v != 0 {
			nonZero = true
		}
	}
	if !nonZero {
		t.Error("empty text gives a zero vector; cosine distance would be undefined")
	}
}
