//go:build !treesitter

package indexing

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/indexer"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/pkg/gleann"
)

// liteMarkerIterations repeats the full-build + sync cycle: the v4 defect was
// an unordered map application, so one cycle could pass by luck (rv-lite-
// nongo-v4 lost the marker in ~3 of 20 builds). The per-cycle state is
// asserted every time.
const liteMarkerIterations = 40

// liteNonGoFixture is a tiny module with a non-Go file in its own directory:
// the sibling rule that marks files dirty does not link main.go to pylib/, so
// a Go-only sync must rebuild metadata from the markers alone.
func liteNonGoFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/fx\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package fx\n\nfunc main() { println(\"v0\") }\n")
	writeFile(t, filepath.Join(dir, "pylib", "util.py"), "def helper():\n    return 1\n")
	return dir
}

func indexMetaValue(t *testing.T, st *store.Store, key string) (string, bool) {
	t.Helper()
	var v string
	err := st.ReaderDB().QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("reading meta %q: %v", key, err)
	}
	return v, true
}

// TestFullBuildLiteNonGoMarkersSurviveGoOnlySync is the end-to-end regression
// for rv-lite-nongo-v4: a full lite build must keep the per-file
// graph_nongo_lite marker on EVERY run, and a later Go-only body edit must
// not flip graph_treesitter to "1", so NonGoSymbolNotice keeps warning that
// the non-Go symbols were extracted without tree-sitter.
func TestFullBuildLiteNonGoMarkersSurviveGoOnlySync(t *testing.T) {
	if chunking.TreeSitterAvailable() {
		t.Skip("lite-only regression: a treesitter build has real non-Go edges")
	}
	env := newEnv(t)
	docs := liteNonGoFixture(t)
	buildFormat2(t, env, docs)

	st, err := store.Open(filepath.Join(env.cfg.IndexDir, "g", "g.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	g := graphstore.New(st)

	graphDB, err := gleann.OpenGraphDB(env.cfg.IndexDir, "g")
	if err != nil {
		t.Fatalf("gleann.OpenGraphDB: %v", err)
	}
	if graphDB == nil {
		t.Fatal("gleann.OpenGraphDB returned no graph")
	}
	t.Cleanup(graphDB.Close)

	module := indexer.DetectGoModule(docs)
	var pyFQN string
	if err := st.ReaderDB().QueryRow(`SELECT fqn FROM symbols WHERE file = 'pylib/util.py' ORDER BY fqn LIMIT 1`).Scan(&pyFQN); err != nil {
		t.Fatalf("no symbol for pylib/util.py after the initial build: %v", err)
	}

	var out, warn bytes.Buffer
	printer := newPrinter(&out, &warn)
	mainGo := filepath.Join(docs, "main.go")
	const markerKey = "graph_nongo_lite:pylib/util.py"

	checkState := func(stage string, iter int) {
		t.Helper()
		if _, ok := indexMetaValue(t, st, markerKey); !ok {
			t.Fatalf("%s iteration %d: BUG: %s missing after a full lite build (the prefix deletion ran after the marker set)", stage, iter, markerKey)
		}
		if got, ok := indexMetaValue(t, st, store.MetaGraphTreeSitter); !ok || got != "0" {
			t.Fatalf("%s iteration %d: %s = %q (exists=%v), want \"0\": lite non-Go symbols exist", stage, iter, store.MetaGraphTreeSitter, got, ok)
		}
		if g.HasTreeSitterEdges() {
			t.Fatalf("%s iteration %d: HasTreeSitterEdges = true, want false", stage, iter)
		}
		if notice := NonGoSymbolNotice(pyFQN, graphDB, gleann.DirectionCallers); !strings.Contains(notice, "without tree-sitter") {
			t.Fatalf("%s iteration %d: NonGoSymbolNotice(%q) = %q, want the \"without tree-sitter\" notice", stage, iter, pyFQN, notice)
		}
	}

	for iter := 0; iter < liteMarkerIterations; iter++ {
		// Body-only edit: the same declaration, a different literal. No new
		// symbol can dirty the Python file or re-extract it.
		writeFile(t, mainGo, fmt.Sprintf("package fx\n\nfunc main() { println(\"v%d\") }\n", iter+1))

		if err := fullGraphSQLite(context.Background(), g, module, docs, false, printer); err != nil {
			t.Fatalf("iteration %d: fullGraphSQLite: %v", iter, err)
		}
		checkState("full build", iter)

		if err := incrementalGraphSQLite(context.Background(), g, []string{mainGo}, module, docs, printer); err != nil {
			t.Fatalf("iteration %d: incrementalGraphSQLite: %v", iter, err)
		}
		checkState("Go-only sync", iter)
	}
}
