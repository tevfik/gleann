package gleann

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tevfik/gleann/internal/analysis"
	"github.com/tevfik/gleann/internal/store"
)

// testPassageStore opens (creating it when missing) the format-2 record store
// <basePath>.db for tests that seed passages directly instead of building an
// index: Add assigns dense vector slots after the current last one, like the
// builder's staged passages once their vectors are published. It panics on
// error, as the tests have no use for a half-open store.
func testPassageStore(basePath string) *storePassages {
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		panic(err)
	}
	st, err := store.Open(basePath+".db", store.Options{})
	if err != nil {
		panic(err)
	}
	lang, _ := st.Meta(context.Background(), "language")
	return &storePassages{st: st, basePath: basePath, analyzer: analysis.New(lang)}
}
