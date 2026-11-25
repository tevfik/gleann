package indexing

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// modulelessFixture is a Go tree with no go.mod and no go.work: the exact
// go/types resolver cannot load it, so the graph is built syntactically and
// the health record must say why (T3 / E4).
func modulelessFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.go"), "package fx\n\n// Helper does the work.\nfunc Helper(x int) int { return x + 1 }\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package fx\n\nfunc main() { println(Helper(1)) }\n")
	return dir
}

// metaKeyValue reads one meta row of the format-2 index database.
func metaKeyValue(t *testing.T, db *sql.DB, key string) (string, bool) {
	t.Helper()
	var v string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("meta %q: %v", key, err)
	}
	return v, true
}

// confidenceCount counts edges of kind whose confidence label is conf.
func confidenceCount(t *testing.T, db *sql.DB, kind, conf string) int {
	t.Helper()
	q := `SELECT count(*) FROM graph_edges e JOIN graph_labels k ON k.id = e.kind
JOIN graph_labels c ON c.id = e.confidence WHERE k.label = ? AND c.label = ?`
	var n int
	if err := db.QueryRow(q, kind, conf).Scan(&n); err != nil {
		t.Fatalf("counting %s/%s edges: %v", kind, conf, err)
	}
	return n
}

// TestGraphHealthRecordedBySyntacticBuild checks that a full build records how
// it built the graph: the extractor version, the resolver it used and the
// reason the exact resolver was unavailable.
func TestGraphHealthRecordedBySyntacticBuild(t *testing.T) {
	env := newEnv(t)
	docs := modulelessFixture(t)
	buildFormat2(t, env, docs)

	db := openGraphDB(t, env)
	if got, ok := metaKeyValue(t, db, store.MetaGraphExtractorVersion); !ok || got != graphstore.GraphExtractorVersion {
		t.Errorf("%s = %q (present=%v), want %q", store.MetaGraphExtractorVersion, got, ok, graphstore.GraphExtractorVersion)
	}
	if got, ok := metaKeyValue(t, db, store.MetaGraphResolver); !ok || got != graphstore.ResolverSyntactic {
		t.Errorf("%s = %q (present=%v), want %q", store.MetaGraphResolver, got, ok, graphstore.ResolverSyntactic)
	}
	if got, _ := metaKeyValue(t, db, store.MetaGraphResolverReason); got != "no go.mod" {
		t.Errorf("%s = %q, want %q", store.MetaGraphResolverReason, got, "no go.mod")
	}
	got, ok := metaKeyValue(t, db, store.MetaGraphBuiltAt)
	if !ok {
		t.Fatalf("%s was not recorded", store.MetaGraphBuiltAt)
	}
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("%s = %q is not an RFC3339 timestamp: %v", store.MetaGraphBuiltAt, got, err)
	}
}

// TestGraphHealthRecordsExactResolver checks the positive case: a module the
// exact resolver can load is recorded as "exact" with no reason.
func TestGraphHealthRecordsExactResolver(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t) // go.mod module example.com/fx
	buildFormat2(t, env, docs)

	db := openGraphDB(t, env)
	if got, ok := metaKeyValue(t, db, store.MetaGraphResolver); !ok || got != graphstore.ResolverExact {
		t.Fatalf("%s = %q (present=%v), want %q", store.MetaGraphResolver, got, ok, graphstore.ResolverExact)
	}
	if got, ok := metaKeyValue(t, db, store.MetaGraphResolverReason); ok && got != "" {
		t.Errorf("%s = %q, want it absent or empty", store.MetaGraphResolverReason, got)
	}
	if n := confidenceCount(t, db, "CALLS", "exact"); n == 0 {
		t.Errorf("an exact build of a Go module stored no exact CALLS edge")
	}
}

// TestGraphHealthExactDisabledByEnv is the red-first test of the T3 brief:
// with GLEANN_GRAPH_EXACT=0 the record must say the resolver is syntactic and
// name the environment variable as the reason, and no edge may be exact.
func TestGraphHealthExactDisabledByEnv(t *testing.T) {
	t.Setenv("GLEANN_GRAPH_EXACT", "0")
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	db := openGraphDB(t, env)
	if got, ok := metaKeyValue(t, db, store.MetaGraphResolver); !ok || got != graphstore.ResolverSyntactic {
		t.Fatalf("%s = %q (present=%v), want %q", store.MetaGraphResolver, got, ok, graphstore.ResolverSyntactic)
	}
	if got, _ := metaKeyValue(t, db, store.MetaGraphResolverReason); got != "GLEANN_GRAPH_EXACT=0" {
		t.Errorf("%s = %q, want %q", store.MetaGraphResolverReason, got, "GLEANN_GRAPH_EXACT=0")
	}
	if n := confidenceCount(t, db, "CALLS", "exact"); n != 0 {
		t.Errorf("GLEANN_GRAPH_EXACT=0 still stored %d exact CALLS edge(s)", n)
	}
}

// TestGraphHealthSurvivesIncrementalSync: a sync rewrites the changed files'
// rows in their own transactions, none of which carries the health keys. The
// record must survive and its built_at must advance.
func TestGraphHealthSurvivesIncrementalSync(t *testing.T) {
	env := newEnv(t)
	docs := goFixture(t)
	buildFormat2(t, env, docs)

	db := openGraphDB(t, env)
	before, ok := metaKeyValue(t, db, store.MetaGraphBuiltAt)
	if !ok {
		t.Fatalf("%s was not recorded by the build", store.MetaGraphBuiltAt)
	}
	writeFile(t, filepath.Join(docs, "main.go"), "package fx\n\nfunc main() { println(Helper(2), Helper(3)) }\n")
	syncTestChanged(t, env, docs, "main.go")

	if got, ok := metaKeyValue(t, db, store.MetaGraphExtractorVersion); !ok || got != graphstore.GraphExtractorVersion {
		t.Errorf("after sync %s = %q (present=%v), want %q", store.MetaGraphExtractorVersion, got, ok, graphstore.GraphExtractorVersion)
	}
	if got, ok := metaKeyValue(t, db, store.MetaGraphResolver); !ok || got != graphstore.ResolverExact {
		t.Errorf("after sync %s = %q (present=%v), want %q", store.MetaGraphResolver, got, ok, graphstore.ResolverExact)
	}
	after, ok := metaKeyValue(t, db, store.MetaGraphBuiltAt)
	if !ok {
		t.Fatalf("%s was dropped by the sync", store.MetaGraphBuiltAt)
	}
	tBefore, err1 := time.Parse(time.RFC3339, before)
	tAfter, err2 := time.Parse(time.RFC3339, after)
	if err1 != nil || err2 != nil {
		t.Fatalf("built_at not RFC3339: %q %q (%v %v)", before, after, err1, err2)
	}
	if tAfter.Before(tBefore) {
		t.Errorf("built_at went backwards: %q then %q", before, after)
	}
	if tAfter.Equal(tBefore) {
		t.Logf("built_at unchanged within the same second: %q", after)
	}
}

// TestNoGoModWarningOncePerIndex is the E4 fix: the "no Go module" warning is
// printed by the build that first needs it and not again on every later sync,
// until a Go module appears (which resets the flag) and disappears again.
func TestNoGoModWarningOncePerIndex(t *testing.T) {
	env := newEnv(t)
	docs := modulelessFixture(t)

	var buildOut bytes.Buffer
	if _, err := Build(context.Background(), BuildOptions{Options: env.options(&buildOut), Name: "g", DocsDir: docs, Graph: true}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(buildOut.String(), noGoModuleWarningMarker) {
		t.Fatalf("the build of a moduleless tree must warn, got:\n%s", buildOut.String())
	}

	for _, rel := range [][]string{{"a.go"}, {"main.go"}, {"a.go", "main.go"}} {
		if out := syncTestChanged(t, env, docs, rel...); strings.Contains(out, noGoModuleWarningMarker) {
			t.Errorf("sync of %v repeated the no-go.mod warning:\n%s", rel, out)
		}
	}

	// A go.mod appears: the exact resolver loads, and the flag is reset.
	writeFile(t, filepath.Join(docs, "go.mod"), "module example.com/fx\n\ngo 1.21\n")
	if out := syncTestChanged(t, env, docs, "a.go", "main.go"); strings.Contains(out, noGoModuleWarningMarker) {
		t.Errorf("sync with a go.mod warned anyway:\n%s", out)
	}
	// The go.mod is removed: the warning is due once again, and only once.
	if err := os.Remove(filepath.Join(docs, "go.mod")); err != nil {
		t.Fatalf("removing go.mod: %v", err)
	}
	if out := syncTestChanged(t, env, docs, "a.go", "main.go"); !strings.Contains(out, noGoModuleWarningMarker) {
		t.Errorf("the no-go.mod warning did not return after the module disappeared:\n%s", out)
	}
	if out := syncTestChanged(t, env, docs, "a.go"); strings.Contains(out, noGoModuleWarningMarker) {
		t.Errorf("the warning repeated right after it was re-armed:\n%s", out)
	}
}

// noGoModuleWarningMarker is the fragment every "no Go module" warning shares.
const noGoModuleWarningMarker = "no Go module"
