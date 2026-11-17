package quality

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
	_ "github.com/tevfik/gleann/pkg/backends"
	"github.com/tevfik/gleann/pkg/benchmark"
	"github.com/tevfik/gleann/pkg/gleann"
)

var update = flag.Bool("update", false, "rewrite testdata/baseline.json with the measured numbers")

const (
	baselinePath  = "testdata/baseline.json"
	tasksPath     = "testdata/tasks.json"
	corpusArchive = "testdata/corpus.tar.gz"
	topK          = 10
)

// Metrics are file-level scores as fractions in [0,1].
type Metrics struct {
	R1  float64 `json:"r1"`
	R10 float64 `json:"r10"`
	MRR float64 `json:"mrr"`
}

// Baseline is the stored reference: the numbers measured on the pinned corpus
// and how far below them a run may fall.
type Baseline struct {
	Corpus     string             `json:"corpus"`
	Tasks      int                `json:"tasks"`
	Tolerance  float64            `json:"tolerance"`
	Strategies map[string]Metrics `json:"strategies"`
}

// The order of strategies in the table.
var strategyOrder = []string{"BM25", "Dense", "Hybrid"}

func TestRetrievalQuality(t *testing.T) {
	if testing.Short() {
		t.Skip("retrieval quality gate skipped in -short mode")
	}
	if raceEnabled && os.Getenv("GLEANN_QUALITY_RACE") != "1" {
		// The run is CPU-bound vector math, about 12 s normally and about 170 s
		// under the race detector, which finds nothing here that the packages'
		// own tests do not. CI runs the gate in a separate step without -race.
		t.Skip("retrieval quality gate skipped under -race (set GLEANN_QUALITY_RACE=1 to run it)")
	}
	// Keep the embedding cache and the vault out of the real home directory.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	var tasks []benchmark.Task
	readJSON(t, tasksPath, &tasks)
	commit, _ := os.ReadFile("testdata/COMMIT")

	corpusDir := t.TempDir()
	extractCorpus(t, corpusArchive, corpusDir)

	ctx := context.Background()
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.Backend = "hnsw"
	cfg.EmbeddingModel = hashEmbedder{}.ModelName()
	cfg.EmbeddingProvider = "test"

	const name = "quality"
	res, err := indexing.Build(ctx, indexing.BuildOptions{
		Name:    name,
		DocsDir: corpusDir,
		Options: indexing.Options{
			Config:    cfg,
			NoPlugins: true,
			NoTracker: true,
			Embedder:  hashEmbedder{},
		},
	})
	if err != nil {
		t.Fatalf("building the corpus index: %v", err)
	}
	if res.Chunks < 1000 {
		t.Fatalf("corpus produced only %d chunks; the snapshot was not read", res.Chunks)
	}

	got := measureQuality(t, ctx, cfg, name, tasks, strings.TrimSpace(string(commit)))

	if *update || os.Getenv("GLEANN_QUALITY_UPDATE") == "1" {
		writeBaseline(t, got)
		t.Logf("baseline rewritten at %s", baselinePath)
		logTable(t, got, nil)
		return
	}

	compareToBaseline(t, got)
}

// measureQuality loads the index and runs every task through the three
// strategies. The caller built the index however it wants to test.
func measureQuality(t *testing.T, ctx context.Context, cfg gleann.Config, name string, tasks []benchmark.Task, commit string) Baseline {
	t.Helper()
	searcher := gleann.NewSearcher(cfg, hashEmbedder{})
	searcher.SetScorer(gleann.NewBM25Adapter())
	if err := searcher.Load(ctx, name); err != nil {
		t.Fatalf("loading the index: %v", err)
	}
	defer searcher.Close()
	if !searcher.VectorAvailable() {
		t.Fatal("vector index is not available; dense and hybrid numbers would be BM25 in disguise")
	}

	files := func(results []gleann.SearchResult) []string {
		var paths []string
		for _, r := range results {
			for _, k := range []string{"file", "source"} {
				if s, _ := r.Metadata[k].(string); s != "" {
					paths = append(paths, s)
					break
				}
			}
		}
		return paths
	}
	run := func(opts ...gleann.SearchOption) benchmark.SearcherFunc {
		return func(ctx context.Context, q string, k int) ([]string, int, error) {
			rs, err := searcher.Search(ctx, q, append([]gleann.SearchOption{gleann.WithTopK(k)}, opts...)...)
			return files(rs), 0, err
		}
	}
	runner := benchmark.NewRunner(tasks)
	runner.RegisterStrategy("BM25", func(ctx context.Context, q string, k int) ([]string, int, error) {
		rs, err := searcher.SearchBM25(ctx, q, k)
		return files(rs), 0, err
	})
	runner.RegisterStrategy("Dense", run(gleann.WithHybridAlpha(1.0)))
	runner.RegisterStrategy("Hybrid", run()) // what `gleann search` does by default
	summaries, err := runner.Run(ctx, topK)
	if err != nil {
		t.Fatalf("running the tasks: %v", err)
	}

	got := Baseline{
		Corpus:     commit,
		Tasks:      len(tasks),
		Tolerance:  0.01,
		Strategies: map[string]Metrics{},
	}
	for _, s := range strategyOrder {
		r := summaries[s]
		got.Strategies[s] = Metrics{R1: round4(r.AvgRecall1 / 100), R10: round4(r.AvgRecall10 / 100), MRR: r.AvgMRR}
	}

	return got
}

// compareToBaseline fails when a strategy fell below the stored numbers by more
// than the tolerance.
func compareToBaseline(t *testing.T, got Baseline) {
	t.Helper()
	var want Baseline
	readJSON(t, baselinePath, &want)
	logTable(t, got, &want)
	if want.Corpus != got.Corpus || want.Tasks != got.Tasks {
		t.Fatalf("baseline was made for corpus %s with %d tasks, this run has %s with %d; rerun with -update",
			want.Corpus, want.Tasks, got.Corpus, got.Tasks)
	}
	for _, s := range strategyOrder {
		b, ok := want.Strategies[s]
		if !ok {
			t.Errorf("%s: missing from the baseline", s)
			continue
		}
		m := got.Strategies[s]
		if m.MRR < b.MRR-want.Tolerance {
			t.Errorf("%s: MRR %.3f fell below baseline %.3f by more than %.3f", s, m.MRR, b.MRR, want.Tolerance)
		}
		if m.R10 < b.R10-want.Tolerance {
			t.Errorf("%s: R@10 %.3f fell below baseline %.3f by more than %.3f", s, m.R10, b.R10, want.Tolerance)
		}
		if m.MRR > b.MRR+want.Tolerance || m.R10 > b.R10+want.Tolerance {
			t.Logf("%s improved beyond the tolerance; if intended, run: go test ./internal/quality -run TestRetrievalQuality -update", s)
		}
	}
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

func logTable(t *testing.T, got Baseline, want *Baseline) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\nretrieval quality, %d tasks, corpus %.7s (file-level, top-%d chunks)\n", got.Tasks, got.Corpus, topK)
	fmt.Fprintf(&sb, "%-8s %8s %8s %8s", "strategy", "R@1", "R@10", "MRR")
	if want != nil {
		fmt.Fprintf(&sb, " %10s %10s", "base R@10", "base MRR")
	}
	sb.WriteString("\n")
	for _, s := range strategyOrder {
		m := got.Strategies[s]
		fmt.Fprintf(&sb, "%-8s %8.3f %8.3f %8.3f", s, m.R1, m.R10, m.MRR)
		if want != nil {
			b := want.Strategies[s]
			fmt.Fprintf(&sb, " %10.3f %10.3f", b.R10, b.MRR)
		}
		sb.WriteString("\n")
	}
	t.Log(sb.String())
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
}

func writeBaseline(t *testing.T, b Baseline) {
	t.Helper()
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.FromSlash(baselinePath), append(data, '\n'), 0o644); err != nil {
		t.Fatalf("writing %s: %v", baselinePath, err)
	}
}

// extractCorpus unpacks the gzipped tar at archive into dst. Entry names must
// be relative, slash-separated paths that stay inside dst.
func extractCorpus(t *testing.T, archive, dst string) {
	t.Helper()
	f, err := os.Open(filepath.FromSlash(archive))
	if err != nil {
		t.Fatalf("opening %s: %v", archive, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("reading %s: %v", archive, err)
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("reading %s: %v", archive, err)
		}
		if hdr.Typeflag != tar.TypeReg {
			t.Fatalf("%s: unexpected entry %q of type %c", archive, hdr.Name, hdr.Typeflag)
		}
		if !fs.ValidPath(hdr.Name) || strings.Contains(hdr.Name, `\`) || hdr.Name == "." {
			t.Fatalf("%s: unsafe entry name %q", archive, hdr.Name)
		}
		out := filepath.Join(dst, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("reading %s in %s: %v", hdr.Name, archive, err)
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
