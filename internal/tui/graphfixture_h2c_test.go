package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
	"github.com/tevfik/gleann/pkg/gleann"
)

// h2cGraphFixture is a tiny Go module whose code graph is known: Add calls
// helper, Twice calls Add.
const h2cGraphSource = `package calc

// Add returns a+b after passing a through helper.
func Add(a, b int) int { return helper(a) + b }

func helper(x int) int { return x }

// Twice doubles x with Add.
func Twice(x int) int { return Add(x, x) }
`

// h2cGraphIndex builds a lexical-only index named name in dir and the code
// graph of a one-file Go module into the same database, the way
// `gleann index build --graph` does.
func h2cGraphIndex(t *testing.T, dir, name string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.com/calc\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "calc.go"), []byte(h2cGraphSource), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := gleann.NewBuilder(gleann.Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BuildLexicalOnly(name, []gleann.Item{{Text: h2cGraphSource, Metadata: map[string]any{"source": "calc.go"}}}); err != nil {
		t.Fatalf("build %s: %v", name, err)
	}
	var out strings.Builder
	indexing.BuildGraphIndex(indexing.GraphOptions{Name: name, DocsDir: src, IndexDir: dir, Stdout: &out, Stderr: &out})
	if strings.Contains(out.String(), "warning") {
		t.Fatalf("graph build warned:\n%s", out.String())
	}
}

// h2cGraphSearcher loads the fixture index; its GraphDB is the code graph.
func h2cGraphSearcher(t *testing.T) *gleann.LeannSearcher {
	t.Helper()
	dir := t.TempDir()
	h2cGraphIndex(t, dir, "code")
	s := gleann.NewSearcher(gleann.Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err := s.Load(context.Background(), "code"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.GraphDB() == nil {
		t.Fatal("fixture index has no code graph")
	}
	return s
}
