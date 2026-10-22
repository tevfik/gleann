package gleann

import "testing"

func TestSameEmbeddingModel(t *testing.T) {
	same := [][2]string{
		{"granite-30m-q8_0", "/home/u/.gleann/models/granite-30m-q8_0.gguf"},
		{"BGE-M3-q8_0.gguf", "bge-m3-q8_0"},
		{" nomic ", "nomic"},
	}
	for _, c := range same {
		if !SameEmbeddingModel(c[0], c[1]) {
			t.Errorf("%q and %q should be the same model", c[0], c[1])
		}
	}
	if SameEmbeddingModel("granite-30m-q8_0", "bge-m3-q8_0") {
		t.Error("different models reported as the same")
	}
}
