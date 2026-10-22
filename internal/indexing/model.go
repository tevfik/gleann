package indexing

import (
	"fmt"

	"github.com/tevfik/gleann/pkg/gleann"
)

// ModelMismatchError reports that the embedding model selected for an update is
// not the one the index was built with. Vectors from different models are not
// comparable, so such an index has to be rebuilt rather than updated.
type ModelMismatchError struct {
	Index         string
	IndexModel    string
	SelectedModel string
}

func (e *ModelMismatchError) Error() string {
	return fmt.Sprintf("index %q was built with embedding model %q but %q was selected; "+
		"vectors from different models cannot be mixed. Re-index it with the new model: "+
		"gleann index rebuild %s --docs <dir> --model %s  (or drop --model to keep using %q)",
		e.Index, e.IndexModel, e.SelectedModel, e.Index, e.SelectedModel, e.IndexModel)
}

func sameModel(a, b string) bool { return gleann.SameEmbeddingModel(a, b) }

// reconcileModel makes an update use the embedding model recorded in meta.
// An inherited default that differs is replaced (with a note); an explicit
// choice that differs returns a *ModelMismatchError. Indexes that never
// recorded a model are left alone.
func reconcileModel(meta *gleann.IndexMeta, opts *Options, p *printer) error {
	if meta.EmbeddingModel == "" || sameModel(opts.Config.EmbeddingModel, meta.EmbeddingModel) {
		return nil
	}
	if opts.ModelExplicit {
		return &ModelMismatchError{Index: meta.Name, IndexModel: meta.EmbeddingModel, SelectedModel: opts.Config.EmbeddingModel}
	}
	p.Printf("ℹ️  Using embedding model %q recorded in index %q (the default %q differs; rebuild the index to switch models)\n",
		meta.EmbeddingModel, meta.Name, opts.Config.EmbeddingModel)
	opts.Config.EmbeddingModel = meta.EmbeddingModel
	return nil
}
