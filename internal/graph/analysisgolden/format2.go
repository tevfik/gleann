package analysisgolden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/indexing"
)

// BuildIndexFormat2 builds the code graph of the fixture into a format-2
// index (<IndexDir>/fixture/fixture.db, the SQLite graph) the way
// `gleann index build --graph` does after the passages are written: the index
// directory carries a meta file with store_format 2 and
// indexing.BuildGraphIndex writes the FileDelta rows. It works in every build;
// in a build without tree-sitter only the Go files yield symbols.
func BuildIndexFormat2(t testing.TB) *Env {
	t.Helper()
	env := &Env{IndexDir: t.TempDir(), Fixture: FixtureDir(t)}
	dir := filepath.Join(env.IndexDir, IndexName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, IndexName+".meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	indexing.BuildGraphIndex(indexing.GraphOptions{
		Name:     IndexName,
		DocsDir:  env.Fixture,
		IndexDir: env.IndexDir,
		Stdout:   &out,
		Stderr:   &out,
	})
	if strings.Contains(out.String(), "warning") {
		t.Fatalf("graph build warned:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, IndexName+".db")); err != nil {
		t.Fatalf("format-2 graph not built: %v\n%s", err, out.String())
	}
	return env
}
