package indexing

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// Regression: the old CLI implementation of dual-model builds re-ran itself
// with every argument equal to the index name renamed, so
// `gleann index build docs --docs docs --code-model a --doc-model b` tried to
// build from a non-existent "docs-code" directory. The library builds from
// the options directly, so the docs directory must be left untouched.
func TestBuildDualModel_IndexNameEqualsDocsDirName(t *testing.T) {
	env := newEnv(t)
	docs := filepath.Join(t.TempDir(), "docs")
	writeFile(t, filepath.Join(docs, "main.go"), "package main\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(docs, "README.md"), "# Docs\n\nSome documentation text.\n")

	res, err := Build(context.Background(), BuildOptions{
		Options:   env.options(nil),
		Name:      "docs",
		DocsDir:   docs,
		CodeModel: "code-embed",
		DocModel:  "doc-embed",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Parts) != 2 || res.Parts[0].Name != "docs-code" || res.Parts[1].Name != "docs-docs" {
		t.Fatalf("unexpected parts: %+v", res.Parts)
	}
	for _, name := range []string{"docs-code", "docs-docs"} {
		if _, err := gleann.GetIndexMeta(env.cfg.IndexDir, name); err != nil {
			t.Errorf("index %s missing: %v", name, err)
		}
	}
}
