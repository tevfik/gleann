package server

import (
	"reflect"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func TestActiveEmbeddingModels(t *testing.T) {
	cfg := gleann.Config{CodeEmbeddingModel: "granite.gguf", DocEmbeddingModel: "bge-m3.gguf"}
	got := activeEmbeddingModels("/models/general.gguf", cfg)
	want := []string{"/models/general.gguf", "granite.gguf", "bge-m3.gguf"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// Empty and duplicate entries are dropped.
	cfg = gleann.Config{CodeEmbeddingModel: "m", DocEmbeddingModel: "m"}
	if got := activeEmbeddingModels("m", cfg); !reflect.DeepEqual(got, []string{"m"}) {
		t.Errorf("duplicates not collapsed: %v", got)
	}
	if got := activeEmbeddingModels("", gleann.Config{}); len(got) != 0 {
		t.Errorf("expected none, got %v", got)
	}
}
