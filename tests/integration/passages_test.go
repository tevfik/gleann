package integration

import (
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// openPassages opens the passage store of a built index for inspection.
func openPassages(t *testing.T, basePath string) gleann.PassageStore {
	t.Helper()
	ps, err := gleann.OpenPassageStore(basePath, true)
	if err != nil {
		t.Fatalf("opening the passage store at %s: %v", basePath, err)
	}
	return ps
}
