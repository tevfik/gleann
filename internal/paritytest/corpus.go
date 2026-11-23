package paritytest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/internal/quality"
	"github.com/tevfik/gleann/pkg/gleann"

	// Register the vector backends so a searcher can load without the
	// caller importing them (indexing.Build imports them too).
	_ "github.com/tevfik/gleann/pkg/backends"
)

// Doc is one source file of a corpus index: a relative slash-separated
// path and its content.
type Doc struct {
	Path    string
	Content string
}

// Index is one tiny index of the parity corpus.
type Index struct {
	Name string
	Docs []Doc
}

// Corpus returns the parity corpus: three tiny indexes — Go code, prose,
// and a code/prose mix — with disjoint subject vocabulary and no passage
// shared between indexes. Every file is well under one chunk, so each
// index holds a handful of passages and builds in milliseconds. A fresh
// copy is returned on each call; callers may edit it freely.
//
// Targeted queries for tests and goldens: "harvest apples" (orchard),
// "lighthouse keeper" (harbor), "telescope nebula" (observatory).
func Corpus() []Index {
	return []Index{
		{
			Name: "orchard",
			Docs: []Doc{
				{Path: "orchard.go", Content: `package orchard

// PlantAppleTree places a young apple tree into prepared soil.
func PlantAppleTree(variety string) int {
	return len(variety) + 7
}

// HarvestApples gathers ripe apples from every ladder row.
func HarvestApples(rows int) int {
	return rows * 12
}
`},
				{Path: "prune.go", Content: `package orchard

// PruneBranches cuts winter growth so sunlight reaches the fruit.
func PruneBranches(canopy float64) float64 {
	return canopy * 0.8
}
`},
			},
		},
		{
			Name: "harbor",
			Docs: []Doc{
				{Path: "harbor.md", Content: `# Harbor operations

Tugboats guide the ferry into the sheltered harbor at dawn.
The lighthouse keeper logs every arrival in a paper ledger.
`},
				{Path: "tide.md", Content: `# Tide tables

Spring tides flood the quay twice each lunar month.
Dredging keeps the channel deep for the fishing fleet.
`},
			},
		},
		{
			Name: "observatory",
			Docs: []Doc{
				{Path: "telescope.py", Content: `def focus_telescope(mirror_mm):
    """Adjust the primary mirror of the observatory telescope."""
    return mirror_mm / 2.0


def capture_spectra(target):
    """Record spectra of a nebula for later analysis."""
    return f"spectra:{target}"
`},
				{Path: "nebula.md", Content: `# Nebula survey

The observatory photographs the orange nebula each clear night.
Astronomers compare spectra to measure the expanding gas shell.
`},
			},
		},
	}
}

// Fixture is a built parity corpus: the fake that serves its embeddings,
// the config pointing at that fake, and the directories involved. Build
// it with New.
type Fixture struct {
	Fake    *Fake
	Config  gleann.Config // IndexDir=Dir, OllamaHost=Fake.URL, hash model
	Dir     string        // index directory, under t.TempDir()
	DocsDir string        // corpus sources, one subdirectory per index
	Indexes []Index       // the corpus as built
}

// New builds the whole parity corpus through the real builder
// (indexing.Build), with every embedding served by a fresh Fake over
// Ollama /api/embed. All files live under t.TempDir(); HOME is isolated so
// nothing reads or writes the real ~/.gleann, and the embedding cache is
// memory-only. The fake is closed via t.Cleanup.
func New(t *testing.T) *Fixture {
	t.Helper()
	fake := NewFake(t)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dir := filepath.Join(root, "indexes")
	docs := filepath.Join(root, "docs")
	for _, d := range []string{home, dir, docs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GLEANN_INDEX_DIR", dir)
	t.Setenv("GLEANN_MULTIMODAL_MODEL", "none")
	t.Setenv("OLLAMA_HOST", fake.URL)
	t.Setenv("OLLAMA_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")

	cfg := gleann.DefaultConfig()
	cfg.IndexDir = dir
	cfg.Backend = "hnsw" // the deterministic default backend (RFC 008 step 0b)
	cfg.EmbeddingProvider = "ollama"
	cfg.EmbeddingModel = (quality.HashEmbedder{}).ModelName()
	cfg.OllamaHost = fake.URL

	fx := &Fixture{Fake: fake, Config: cfg, Dir: dir, DocsDir: docs, Indexes: Corpus()}
	for _, idx := range fx.Indexes {
		BuildIndex(t, cfg, filepath.Join(docs, idx.Name), idx.Name, idx.Docs)
	}
	return fx
}

// BuildIndex writes docs under docsDir and builds one index through
// indexing.Build with the config's embedding endpoint (point cfg at a
// Fake, as Fixture.Config does). Plugins, the vault tracker and the
// embedding disk cache are disabled so the build stays inside docsDir and
// cfg.IndexDir.
func BuildIndex(t *testing.T, cfg gleann.Config, docsDir, name string, docs []Doc) {
	t.Helper()
	for _, d := range docs {
		p := filepath.Join(docsDir, filepath.FromSlash(d.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(d.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := indexing.Build(context.Background(), indexing.BuildOptions{
		Name:    name,
		DocsDir: docsDir,
		Options: indexing.Options{
			Config:               cfg,
			NoPlugins:            true,
			NoTracker:            true,
			NoEmbeddingDiskCache: true,
		},
	})
	if err != nil {
		t.Fatalf("building index %q: %v", name, err)
	}
	if res.Chunks == 0 {
		t.Fatalf("index %q built with 0 chunks", name)
	}
}

// Searcher loads one corpus index with an HTTP embedder pointed at the
// fake, so query embeddings are counted and recorded too. The searcher is
// closed via t.Cleanup.
func (fx *Fixture) Searcher(t *testing.T, name string) *gleann.LeannSearcher {
	t.Helper()
	return fx.load(t, fx.Config, name, indexing.NewEmbedder(fx.Config))
}

// DirectSearcher loads one corpus index with the in-process
// quality.HashEmbedder (no HTTP at query time). Its vectors are identical
// to Searcher's, because the fake serves quality.EmbedText.
func (fx *Fixture) DirectSearcher(t *testing.T, name string) *gleann.LeannSearcher {
	t.Helper()
	cfg := fx.Config
	cfg.EmbeddingProvider = "test"
	return fx.load(t, cfg, name, quality.HashEmbedder{})
}

func (fx *Fixture) load(t *testing.T, cfg gleann.Config, name string, emb gleann.EmbeddingComputer) *gleann.LeannSearcher {
	t.Helper()
	s := gleann.NewSearcher(cfg, emb)
	if err := s.Load(context.Background(), name); err != nil {
		t.Fatalf("loading index %q: %v", name, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Reranker returns a reranker that scores through the fake's /v1/rerank
// endpoint (llamacpp provider, the Jina-compatible wire shape).
func (fx *Fixture) Reranker() *gleann.CrossEncoderReranker {
	return gleann.NewReranker(gleann.RerankerConfig{
		Provider: gleann.RerankerLlamacpp,
		Model:    "test-reranker",
		BaseURL:  fx.Fake.URL,
	})
}
