package indexing

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func buildFor(t *testing.T, env *testEnv, name string) string {
	t.Helper()
	docs := fixture(t)
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(nil), Name: name, DocsDir: docs}); err != nil {
		t.Fatal(err)
	}
	return docs
}

func assertUntouched(t *testing.T, env *testEnv, name string, want gleann.IndexMeta) {
	t.Helper()
	got, err := gleann.GetIndexMeta(env.cfg.IndexDir, name)
	if err != nil {
		t.Fatal(err)
	}
	if got.EmbeddingModel != want.EmbeddingModel || got.Dimensions != want.Dimensions || got.NumPassages != want.NumPassages {
		t.Errorf("index changed: %+v -> %+v", want, got)
	}
	if h, err := gleann.CheckIndexHealth(env.cfg.IndexDir, name); err != nil || h.NeedsRepair() {
		t.Errorf("index unhealthy: %+v (%v)", h, err)
	}
}

// An inherited default model that differs from the index's must not be used:
// it used to push vectors of another size into the index.
func TestSyncUsesTheModelTheIndexWasBuiltWith(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "m")
	built, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")

	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Mul(a, b int) int { return a * b }\n")
	var out bytes.Buffer
	opts := env.options(&out)
	opts.Config.EmbeddingModel = "some-other-default"
	if _, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "m"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	after, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")
	if after.EmbeddingModel != built.EmbeddingModel {
		t.Errorf("model changed %q -> %q", built.EmbeddingModel, after.EmbeddingModel)
	}
	if !strings.Contains(out.String(), "recorded in index") {
		t.Errorf("no notice about the model in use:\n%s", out.String())
	}
}

// A model chosen on purpose that differs from the index's is an error that
// tells the user to re-index, and leaves the index untouched.
func TestSyncRejectsAnExplicitlyChosenOtherModel(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "m")
	built, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")

	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Mul(a, b int) int { return a * b }\n")
	opts := env.options(nil)
	opts.Config.EmbeddingModel = "bge-m3-q8_0.gguf"
	opts.ModelExplicit = true
	_, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "m"})
	var mm *ModelMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("err = %v, want *ModelMismatchError", err)
	}
	if mm.IndexModel != built.EmbeddingModel || !strings.Contains(err.Error(), "gleann index rebuild m") {
		t.Errorf("unhelpful error: %v", err)
	}
	assertUntouched(t, env, "m", *built)
}

func TestSyncAcceptsTheSameModelGivenAsAPath(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "m")
	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Mul(a, b int) int { return a * b }\n")
	opts := env.options(nil)
	opts.Config.EmbeddingModel = "/models/" + env.cfg.EmbeddingModel + ".gguf"
	opts.ModelExplicit = true
	if _, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "m"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

func TestRepairRejectsAnExplicitlyChosenOtherModel(t *testing.T) {
	env := newEnv(t)
	buildFor(t, env, "m")
	built, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")
	opts := env.options(nil)
	opts.Config.EmbeddingModel = "another-model"
	opts.ModelExplicit = true
	_, err := Repair(context.Background(), RepairOptions{Options: opts, Name: "m", Force: true})
	var mm *ModelMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("err = %v, want *ModelMismatchError", err)
	}
	assertUntouched(t, env, "m", *built)
}

// shortComputer returns vectors of another size than the index holds.
type shortComputer struct{ model string }

func (s shortComputer) Compute(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 2, 3}
	}
	return out, nil
}
func (s shortComputer) ComputeSingle(context.Context, string) ([]float32, error) {
	return []float32{1, 2, 3}, nil
}
func (s shortComputer) Dimensions() int   { return 3 }
func (s shortComputer) ModelName() string { return s.model }

// Even when the model name matches, vectors of the wrong size are refused
// before anything is written (this used to panic inside the backend and leave
// the passage store ahead of the vector file).
func TestSyncRefusesVectorsOfTheWrongSize(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "m")
	built, _ := gleann.GetIndexMeta(env.cfg.IndexDir, "m")

	writeFile(t, filepath.Join(docs, "extra.go"), "package main\n\nfunc UniqueNewFunctionForDimensionTest() {}\n")
	opts := env.options(nil)
	opts.Embedder = shortComputer{model: env.cfg.EmbeddingModel}
	_, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "m"})
	if !errors.Is(err, gleann.ErrDimensionMismatch) {
		t.Fatalf("err = %v, want ErrDimensionMismatch", err)
	}
	assertUntouched(t, env, "m", *built)
}

// A sync that has to re-embed (here: a modified file makes the index compact)
// reports progress; before, it printed nothing between "Starting embedding"
// and the end, which looks like a hang on a cold embedding cache.
func TestSyncReportsEmbeddingProgress(t *testing.T) {
	env := newEnv(t)
	docs := buildFor(t, env, "m")
	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\nfunc Mul(a, b int) int { return a * b }\n")

	var events []Progress
	opts := env.options(nil)
	opts.Progress = func(p Progress) { events = append(events, p) }
	if _, err := Sync(context.Background(), SyncOptions{Options: opts, Name: "m"}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no progress reported")
	}
	last := events[len(events)-1]
	if last.Phase != "vector_index" || last.TotalItems == 0 || last.ItemsDone != last.TotalItems {
		t.Errorf("final progress = %+v, want vector_index complete", last)
	}
}
