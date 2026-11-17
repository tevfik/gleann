package quality

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/pkg/benchmark"
	"github.com/tevfik/gleann/pkg/gleann"
)

// G5 of RFC 005: an index built from half of the corpus and grown to the whole
// corpus through incremental syncs retrieves as well as one built in one go,
// at the format-2 thresholds. The delta variant keeps the added files in the
// delta file (exact search merged with the main file's); the merge variant
// folds the delta into the main file on every sync.
func TestRetrievalQualityGrownIncrementally(t *testing.T) {
	if testing.Short() {
		t.Skip("retrieval quality gate skipped in -short mode")
	}
	if raceEnabled && os.Getenv("GLEANN_QUALITY_RACE") != "1" {
		t.Skip("retrieval quality gate skipped under -race (set GLEANN_QUALITY_RACE=1 to run it)")
	}
	for _, variant := range []struct {
		name  string
		merge bool
	}{{"delta", false}, {"merge", true}} {
		t.Run(variant.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			if variant.merge {
				t.Setenv("GLEANN_DELTA_MIN", "0")
				t.Setenv("GLEANN_DELTA_SHARE", "0")
			} else {
				// Half of the corpus is more than the default delta limit: raise it
				// so every step stays in the delta.
				t.Setenv("GLEANN_DELTA_MIN", "1000000")
			}

			var tasks []benchmark.Task
			readJSON(t, tasksPath, &tasks)
			commit, _ := os.ReadFile("testdata/COMMIT")

			full := t.TempDir()
			extractCorpus(t, corpusArchive, full)
			var files []string
			if err := filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					rel, _ := filepath.Rel(full, p)
					files = append(files, rel)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			sort.Strings(files)
			copyFile := func(rel, dstDir string) {
				data, err := os.ReadFile(filepath.Join(full, rel))
				if err != nil {
					t.Fatal(err)
				}
				out := filepath.Join(dstDir, rel)
				if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(out, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// Even-numbered files first (a deterministic half of every
			// directory), the odd ones in four steps afterwards.
			docs := t.TempDir()
			var rest []string
			for i, rel := range files {
				if i%2 == 0 {
					copyFile(rel, docs)
				} else {
					rest = append(rest, rel)
				}
			}

			ctx := context.Background()
			cfg := gleann.DefaultConfig()
			cfg.IndexDir = t.TempDir()
			cfg.Backend = "hnsw"
			cfg.EmbeddingModel = hashEmbedder{}.ModelName()
			cfg.EmbeddingProvider = "test"
			opts := indexing.Options{Config: cfg, NoPlugins: true, NoTracker: true, Embedder: hashEmbedder{}}

			const name = "quality"
			if _, err := indexing.Build(ctx, indexing.BuildOptions{Name: name, DocsDir: docs, Options: opts}); err != nil {
				t.Fatalf("building half of the corpus: %v", err)
			}
			const steps = 4
			for s := 0; s < steps; s++ {
				for i := s; i < len(rest); i += steps {
					copyFile(rest[i], docs)
				}
				res, err := indexing.Sync(ctx, indexing.SyncOptions{Options: opts, Name: name, DocsDir: docs})
				if err != nil {
					t.Fatalf("sync step %d: %v", s+1, err)
				}
				if res.Update == nil || res.Update.Embedded != res.Update.Added {
					t.Fatalf("sync step %d: %+v, want only the added passages embedded", s+1, res.Update)
				}
				wantMode := gleann.UpdateInPlace
				if variant.merge {
					wantMode = gleann.UpdateMerge
				}
				if res.Update.Mode != wantMode {
					t.Fatalf("sync step %d took the %q path, want %q", s+1, res.Update.Mode, wantMode)
				}
			}
			h, err := gleann.CheckIndexHealth(cfg.IndexDir, name)
			if err != nil || len(h.Problems) != 0 {
				t.Fatalf("doctor after the syncs: %+v (%v)", h, err)
			}
			if !variant.merge && h.DeltaEntries == 0 {
				t.Fatal("the delta variant must leave the added passages in the delta file")
			}
			if variant.merge && h.DeltaEntries != 0 {
				t.Fatalf("the merge variant left %d entries in the delta", h.DeltaEntries)
			}

			got := measureQuality(t, ctx, cfg, name, tasks, strings.TrimSpace(string(commit)))
			compareToBaseline(t, got)
		})
	}
}
