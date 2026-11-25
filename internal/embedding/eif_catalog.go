package embedding

import (
	"os"

	"github.com/tevfik/gleann/internal/modelcatalog"
)

// catalogEmbeddingPath maps a catalogue id or file name of an embedding model
// (gleann models download <id>) to its file in the models directory, when the
// file is there. The catalogue file name need not match the id ("embeddinggemma-2"
// is stored as embeddinggemma-2-Q8_0.gguf), which the eif name guesses cannot know.
func catalogEmbeddingPath(model string) (string, bool) {
	e, ok := modelcatalog.Find(model)
	if !ok || e.Kind != modelcatalog.Embedding {
		return "", false
	}
	if fi, err := os.Stat(e.Path()); err == nil && !fi.IsDir() {
		return e.Path(), true
	}
	return "", false
}
