package indexing

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/tevfik/gleann/internal/vault"
	"github.com/tevfik/gleann/pkg/gleann"
)

const bowDims = 64

// bowEmbed is a deterministic bag-of-words embedding: related texts get
// similar vectors, so retrieval quality is meaningful (unlike length-based
// fakes) and a vector/passage mix-up changes the results.
func bowEmbed(text string) []float32 {
	v := make([]float32, bowDims)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%bowDims]++
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	if n == 0 {
		v[0] = 1
		return v
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}

func newBowOllama(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input any `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var texts []string
		switch v := req.Input.(type) {
		case []any:
			for _, s := range v {
				str, _ := s.(string)
				texts = append(texts, str)
			}
		case string:
			texts = []string{v}
		}
		embs := make([][]float32, len(texts))
		for i, txt := range texts {
			embs[i] = bowEmbed(txt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": embs})
	}))
	t.Cleanup(srv.Close)
	return srv
}

type bowComputer struct{ model string }

func (b bowComputer) Compute(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, s := range texts {
		out[i] = bowEmbed(s)
	}
	return out, nil
}
func (b bowComputer) ComputeSingle(_ context.Context, s string) ([]float32, error) {
	return bowEmbed(s), nil
}
func (b bowComputer) Dimensions() int   { return bowDims }
func (b bowComputer) ModelName() string { return b.model }

type hit struct {
	key   string
	score float32
}

func search(t *testing.T, cfg gleann.Config, name, query string) []hit {
	t.Helper()
	s := gleann.NewSearcher(cfg, bowComputer{model: cfg.EmbeddingModel})
	if err := s.Load(context.Background(), name); err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	defer s.Close()
	res, err := s.Search(context.Background(), query, gleann.WithTopK(10))
	if err != nil {
		t.Fatalf("search %s %q: %v", name, query, err)
	}
	var hits []hit
	for _, r := range res {
		src, _ := r.Metadata["source"].(string)
		txt := strings.Join(strings.Fields(r.Text), " ")
		if len(txt) > 60 {
			txt = txt[:60]
		}
		hits = append(hits, hit{key: src + "|" + txt, score: r.Score})
	}
	return hits
}

var equivalenceQueries = []string{
	"greeting for a name",
	"add two numbers",
	"subtract b from a",
	"multiply values",
	"database connection pooling",
	"installation guide for the project",
	"architecture and design decisions",
	"release notes changelog",
}

// exactSearch reports whether a backend's search is deterministic enough that
// two independent builds over the same data must return the same hits. DiskANN
// is approximate: even two fresh builds of unchanged files can return different
// candidate sets (5 vs 6 hits) and shifted normalised scores, so for it only the
// stored passages are compared; vector/passage alignment is covered by
// TestSyncSelfRetrieval.
func exactSearch(backend string) bool { return backend == "hnsw" }

// passageSet returns the index's passages as sorted "source|text" strings.
func passageSet(t *testing.T, cfg gleann.Config, name string) []string {
	t.Helper()
	pm := gleann.NewReadOnlyPassageManager(filepath.Join(cfg.IndexDir, name, name))
	if err := pm.Load(); err != nil {
		t.Fatal(err)
	}
	defer pm.Close()
	var out []string
	for _, p := range pm.All() {
		out = append(out, fmt.Sprintf("%v|%s", p.Metadata["source"], p.Text))
	}
	sort.Strings(out)
	return out
}

// assertEquivalent compares the synced index to a fresh full build over the
// same files. The passage stores must match for every backend; when the backend
// is exact, the search results must too: same passages, same scores, same top
// hit.
func assertEquivalent(t *testing.T, cfg gleann.Config, synced, fresh string, exact bool) {
	t.Helper()
	if a, b := passageSet(t, cfg, synced), passageSet(t, cfg, fresh); strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("passage stores differ\nsynced=%q\nfresh=%q", a, b)
	}
	if !exact {
		return
	}
	for _, q := range equivalenceQueries {
		a, b := search(t, cfg, synced, q), search(t, cfg, fresh, q)
		if len(a) != len(b) {
			t.Errorf("query %q: synced returned %d results, fresh %d\nsynced=%v\nfresh=%v", q, len(a), len(b), a, b)
			continue
		}
		// Near-tied chunks (e.g. the "package main" import chunks) may swap
		// places between two independent builds, so compare the retrieved
		// passage set, the multiset of scores and (when unambiguous) rank 1.
		if len(a) > 1 && b[0].score-b[1].score > 1e-3 && a[0].key != b[0].key {
			t.Errorf("query %q: top hit synced=%q fresh=%q", q, a[0].key, b[0].key)
		}
		fk := map[string]bool{}
		for _, h := range b {
			fk[h.key] = true
		}
		for _, h := range a {
			if !fk[h.key] {
				t.Errorf("query %q: synced-only result %q", q, h.key)
			}
		}
		sa, sb := scoresOf(a), scoresOf(b)
		for i := range sa {
			if math.Abs(float64(sa[i]-sb[i])) > 1e-3 {
				t.Errorf("query %q: score[%d] synced=%.4f fresh=%.4f\nsynced=%v\nfresh=%v", q, i, sa[i], sb[i], a, b)
				break
			}
		}
	}
}

func scoresOf(h []hit) []float32 {
	out := make([]float32, len(h))
	for i, x := range h {
		out[i] = x.score
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

func equivFixture(t *testing.T) string {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\n// Add adds two numbers together.\nfunc Add(a, b int) int { return a + b }\n\n// Greet returns a greeting for the given name.\nfunc Greet(name string) string { return \"hello \" + name }\n")
	writeFile(t, filepath.Join(dir, "README.md"), "# Guide\n\nInstallation guide for the project: run the installer.\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "Architecture and design decisions of the system are recorded here.\n")
	writeFile(t, filepath.Join(dir, "db.go"), "package main\n\n// Pool manages database connection pooling.\nfunc Pool(size int) int { return size }\n")
	return dir
}

func TestSyncEquivalentToFreshBuild(t *testing.T) {
	scenarios := []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{"unchanged", func(t *testing.T, d string) {}},
		{"modify-grow", func(t *testing.T, d string) {
			writeFile(t, filepath.Join(d, "main.go"), "package main\n\n// Add adds two numbers together.\nfunc Add(a, b int) int { return a + b }\n\n// Greet returns a greeting for the given name.\nfunc Greet(name string) string { return \"hello \" + name }\n\n// Sub subtracts b from a.\nfunc Sub(a, b int) int { return a - b }\n")
		}},
		{"modify-shrink", func(t *testing.T, d string) {
			writeFile(t, filepath.Join(d, "main.go"), "package main\n\n// Greet returns a greeting for the given name.\nfunc Greet(name string) string { return \"hello \" + name }\n")
		}},
		{"add-file", func(t *testing.T, d string) {
			writeFile(t, filepath.Join(d, "CHANGELOG.md"), "# Changelog\n\nRelease notes changelog for version one.\n")
		}},
		{"delete-file", func(t *testing.T, d string) {
			if err := os.Remove(filepath.Join(d, "notes.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"rename-file", func(t *testing.T, d string) {
			if err := os.Rename(filepath.Join(d, "README.md"), filepath.Join(d, "GUIDE.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"multiple", func(t *testing.T, d string) {
			writeFile(t, filepath.Join(d, "main.go"), "package main\n\n// Mul multiplies values.\nfunc Mul(a, b int) int { return a * b }\n")
			writeFile(t, filepath.Join(d, "CHANGELOG.md"), "# Changelog\n\nRelease notes changelog.\n")
			if err := os.Remove(filepath.Join(d, "db.go")); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, backend := range []string{"hnsw", "diskann"} {
		for _, prune := range []bool{true, false} {
			if backend == "diskann" && !prune {
				continue // prune is an hnsw-only knob
			}
			for _, sc := range scenarios {
				t.Run(fmt.Sprintf("%s/prune=%v/%s", backend, prune, sc.name), func(t *testing.T) {
					runEquivalence(t, backend, prune, sc.mutate)
				})
			}
		}
	}
}

func equivEnv(t *testing.T, backend string, prune bool) (Options, gleann.Config) {
	srv := newBowOllama(t)
	cfg := gleann.DefaultConfig()
	cfg.IndexDir = t.TempDir()
	cfg.Backend = backend
	cfg.HNSWConfig.PruneEmbeddings = prune
	cfg.EmbeddingProvider = "ollama"
	cfg.EmbeddingModel = fmt.Sprintf("bow-%d", time.Now().UnixNano())
	cfg.OllamaHost = srv.URL
	tr, err := vault.NewTracker(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return Options{Config: cfg, Tracker: tr, NoPlugins: true, MultimodalModel: "none"}, cfg
}

func runEquivalence(t *testing.T, backend string, prune bool, mutate func(*testing.T, string)) {
	ctx := context.Background()
	opts, cfg := equivEnv(t, backend, prune)
	docs := equivFixture(t)

	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "inc", DocsDir: docs}); err != nil {
		t.Fatalf("build: %v", err)
	}
	before := search(t, cfg, "inc", "greeting for a name")
	if len(before) == 0 {
		t.Fatal("no results before sync")
	}

	mutate(t, docs)
	// Sync with no --backend flag: must pick the backend from the index meta.
	syncOpts := opts
	syncOpts.Config.Backend = ""
	if _, err := Sync(ctx, SyncOptions{Options: syncOpts, Name: "inc"}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Second sync must be a no-op.
	if r2, err := Sync(ctx, SyncOptions{Options: syncOpts, Name: "inc"}); err != nil || !r2.UpToDate {
		t.Fatalf("second sync: %+v %v", r2, err)
	}

	if _, err := Build(ctx, BuildOptions{Options: opts, Name: "fresh", DocsDir: docs}); err != nil {
		t.Fatalf("fresh build: %v", err)
	}
	assertEquivalent(t, cfg, "inc", "fresh", exactSearch(backend))

	mi, _ := gleann.GetIndexMeta(cfg.IndexDir, "inc")
	mf, _ := gleann.GetIndexMeta(cfg.IndexDir, "fresh")
	if mi.NumPassages != mf.NumPassages {
		t.Errorf("num_passages synced=%d fresh=%d", mi.NumPassages, mf.NumPassages)
	}
	if mi.Backend != backend {
		t.Errorf("meta backend=%q want %q", mi.Backend, backend)
	}
	manI, err1 := gleann.LoadManifest(manifestPath(cfg.IndexDir, "inc"))
	manF, err2 := gleann.LoadManifest(manifestPath(cfg.IndexDir, "fresh"))
	if err1 != nil || err2 != nil {
		t.Fatalf("manifests: %v %v", err1, err2)
	}
	if len(manI.Entries) != len(manF.Entries) {
		t.Errorf("manifest entries synced=%d fresh=%d", len(manI.Entries), len(manF.Entries))
	}
	for p := range manF.Entries {
		if _, ok := manI.Entries[p]; !ok {
			t.Errorf("manifest missing %s", p)
		}
	}
}

// Several consecutive syncs (sequence keeps growing in the passage DB) must
// stay equivalent to a fresh build.
func TestSyncTwiceEquivalentToFreshBuild(t *testing.T) {
	for _, backend := range []string{"hnsw", "diskann"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			opts, cfg := equivEnv(t, backend, false)
			docs := equivFixture(t)
			if _, err := Build(ctx, BuildOptions{Options: opts, Name: "inc", DocsDir: docs}); err != nil {
				t.Fatal(err)
			}
			steps := []func(){
				func() {
					writeFile(t, filepath.Join(docs, "main.go"), "package main\n\n// Sub subtracts b from a.\nfunc Sub(a, b int) int { return a - b }\n")
				},
				func() { writeFile(t, filepath.Join(docs, "CHANGELOG.md"), "# Changelog\n\nRelease notes changelog.\n") },
				func() { _ = os.Remove(filepath.Join(docs, "notes.txt")) },
			}
			for i, step := range steps {
				step()
				if _, err := Sync(ctx, SyncOptions{Options: opts, Name: "inc"}); err != nil {
					t.Fatalf("sync %d: %v", i, err)
				}
				name := fmt.Sprintf("fresh%d", i)
				if _, err := Build(ctx, BuildOptions{Options: opts, Name: name, DocsDir: docs}); err != nil {
					t.Fatal(err)
				}
				assertEquivalent(t, cfg, "inc", name, exactSearch(backend))
			}
		})
	}
}

// Every vector the backend returns must resolve to the passage it was
// computed from: query with each passage's own text and expect it back first.
func TestSyncSelfRetrieval(t *testing.T) {
	for _, backend := range []string{"hnsw", "diskann"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			opts, cfg := equivEnv(t, backend, true)
			docs := equivFixture(t)
			if _, err := Build(ctx, BuildOptions{Options: opts, Name: "inc", DocsDir: docs}); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(docs, "main.go"), "package main\n\n// Sub subtracts b from a.\nfunc Sub(a, b int) int { return a - b }\n")
			if _, err := Sync(ctx, SyncOptions{Options: opts, Name: "inc"}); err != nil {
				t.Fatal(err)
			}
			pm := gleann.NewReadOnlyPassageManager(filepath.Join(cfg.IndexDir, "inc", "inc"))
			if err := pm.Load(); err != nil {
				t.Fatal(err)
			}
			all := pm.All()
			_ = pm.Close()
			if len(all) == 0 {
				t.Fatal("no passages")
			}
			sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
			s := gleann.NewSearcher(cfg, bowComputer{model: cfg.EmbeddingModel})
			if err := s.Load(ctx, "inc"); err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			for _, p := range all {
				res, err := s.Search(ctx, p.Text, gleann.WithTopK(3))
				if err != nil || len(res) == 0 {
					t.Fatalf("search for passage %d: %v (%d results)", p.ID, err, len(res))
				}
				// An exact backend must put a passage first for its own text; an
				// approximate one (DiskANN) may rank a near-tie ahead of it, but a
				// vector attached to the wrong passage would miss the top 3 entirely.
				found := res[0].ID == p.ID
				if !exactSearch(backend) {
					for _, r := range res {
						found = found || r.ID == p.ID
					}
				}
				if !found {
					t.Errorf("passage %d (%q) not its own top hit: got %d", p.ID, p.Metadata["source"], res[0].ID)
				}
			}
		})
	}
}
