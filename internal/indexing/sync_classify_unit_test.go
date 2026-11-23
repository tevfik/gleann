package indexing

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// The classification itself: only a store the builder tagged as needing a
// rebuild falls back to a full rebuild; transient embedder, plugin and
// source-read failures are returned.
func TestNeedsFullRebuildClassification(t *testing.T) {
	plugin := errors.New("plugin exited with status 1")
	read := &fs.PathError{Op: "open", Path: "/docs/a.md", Err: fs.ErrPermission}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"embedder unreachable", errEmbedderDown, false},
		{"embedder wrapped", fmt.Errorf("incremental update failed: %w", fmt.Errorf("compute embeddings: %w", errEmbedderDown)), false},
		{"plugin failure", plugin, false},
		{"plugin wrapped", fmt.Errorf("incremental: reading changed files: %w", fmt.Errorf("docling: %w", plugin)), false},
		{"source read error", read, false},
		{"source read wrapped", fmt.Errorf("incremental: reading changed files: %w", read), false},
		{"dimension mismatch", fmt.Errorf("compute embeddings: %w", gleann.ErrDimensionMismatch), false},
		{"context cancelled", fmt.Errorf("incremental update failed: %w", context.Canceled), false},
		{"damaged store", fmt.Errorf("incremental update failed: %w", fmt.Errorf("%w: opening format-2 store: file is not a database", gleann.ErrRebuildRequired)), true},
		{"store wrapped twice", fmt.Errorf("incremental update failed: %w", fmt.Errorf("x: %w", fmt.Errorf("%w: slot span", gleann.ErrRebuildRequired))), true},
		{"other sentinel", fs.ErrNotExist, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsFullRebuild(tc.err); got != tc.want {
				t.Errorf("needsFullRebuild(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
