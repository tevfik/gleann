package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// metaOrderIterations is deliberately larger than one: Go map iteration order
// is randomized, so a single ReplaceAllWithMeta call that mixes a prefix
// deletion with key sets under that prefix can pass by luck. Repeating the
// call with a freshly built map makes the unordered implementation fail with
// overwhelming probability (rv-lite-nongo-v4 lost the marker in ~3 of 20
// builds).
const metaOrderIterations = 60

func metaValue(t *testing.T, st *store.Store, key string) (string, bool) {
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

// liteMarkerKeys returns per-file lite markers under the prefix, plus one key
// that is only ever set, so the ordering of the three mutation phases is
// exercised together.
func liteMarkerKeys() []string {
	return []string{
		"graph_nongo_lite:pylib/one.py",
		"graph_nongo_lite:pylib/two.py",
		"graph_nongo_lite:pylib/three.py",
		"graph_nongo_lite:pylib/four.py",
	}
}

// assertLiteMarkerState is the contract every meta-mutating graph write must
// uphold: the prefix deletion runs before the sets, so the markers under the
// prefix survive and stale keys under it are gone.
func assertLiteMarkerState(t *testing.T, st *store.Store, op string, iter int, markers []string) {
	t.Helper()
	one := "1"
	for _, key := range markers {
		got, ok := metaValue(t, st, key)
		if !ok {
			t.Fatalf("%s iteration %d: BUG: %q was deleted by the prefix mutation that ran after the set", op, iter, key)
		}
		if got != one {
			t.Fatalf("%s iteration %d: %q = %q, want %q", op, iter, key, got, one)
		}
	}
	if _, ok := metaValue(t, st, "graph_nongo_lite:pylib/stale.py"); ok {
		t.Fatalf("%s iteration %d: BUG: stale marker graph_nongo_lite:pylib/stale.py survived the prefix deletion", op, iter)
	}
	if _, ok := metaValue(t, st, "graph_extra:drop"); ok {
		t.Fatalf("%s iteration %d: BUG: exact-delete key graph_extra:drop survived", op, iter)
	}
	if got, ok := metaValue(t, st, "graph_extra:keep"); !ok || got != one {
		t.Fatalf("%s iteration %d: graph_extra:keep = %q (exists=%v), want %q", op, iter, got, ok, one)
	}
}

// TestApplyMetaMutationsTxPhaseOrder pins the helper's deterministic phases
// directly (prefix deletions, then exact deletions, then sets), independent of
// Go's map iteration order: the final state is the contract.
func TestApplyMetaMutationsTxPhaseOrder(t *testing.T) {
	ctx := context.Background()
	g, st := openTestGraph(t)

	tx, err := g.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	for k, v := range map[string]string{
		"prefix:old":  "1",
		"prefix:gone": "1",
		"other:drop":  "1",
		"other:keep":  "old",
		"other:set":   "old",
	} {
		if err := g.SetMetaTx(ctx, tx, k, v); err != nil {
			t.Fatalf("seeding %s: %v", k, err)
		}
	}
	two, three := "2", "3"
	if err := g.applyMetaMutationsTx(ctx, tx, map[string]*string{
		"prefix:%":    nil,
		"prefix:gone": nil,
		"other:drop":  nil,
		"prefix:keep": &two,
		"other:keep":  &three,
		"other:set":   nil,
	}); err != nil {
		t.Fatalf("applyMetaMutationsTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	want := map[string]string{"prefix:keep": "2", "other:keep": "3"}
	rows, err := st.ReaderDB().Query(`SELECT key, value FROM meta WHERE key LIKE 'prefix:%' OR key LIKE 'other:%' ORDER BY key`)
	if err != nil {
		t.Fatalf("listing meta: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("meta rows = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("meta %q = %q, want %q (all rows %v)", k, got[k], v, got)
		}
	}
}

// TestReplaceAllWithMetaPrefixDeleteRunsBeforeMarkerSets is the regression
// test for the v4 defect: ReplaceAllWithMeta applied its map in Go's
// randomized iteration order, so "graph_nongo_lite:%" (prefix deletion) could
// run after the per-file "graph_nongo_lite:<rel>" sets and silently wipe the
// markers of a full lite build. A later Go-only sync then recomputed
// graph_treesitter to "1" and silenced NonGoSymbolNotice.
func TestReplaceAllWithMetaPrefixDeleteRunsBeforeMarkerSets(t *testing.T) {
	ctx := context.Background()
	g, st := openTestGraph(t)

	deltas := []FileDelta{
		{
			File: "pylib/one.py", GraphHash: "h1",
			Symbols: []Symbol{mkSym("example.com/fx/pylib.one", "pylib/one.py", 1)},
		},
		{
			File: "pylib/two.py", GraphHash: "h2",
			Symbols: []Symbol{mkSym("example.com/fx/pylib.two", "pylib/two.py", 1)},
		},
	}

	for iter := 0; iter < metaOrderIterations; iter++ {
		// State the call must clear (stale prefix row) and delete exactly.
		if err := g.SetMeta(ctx, "graph_nongo_lite:pylib/stale.py", "1"); err != nil {
			t.Fatalf("seeding stale marker: %v", err)
		}
		if err := g.SetMeta(ctx, "graph_extra:drop", "1"); err != nil {
			t.Fatalf("seeding exact-delete key: %v", err)
		}

		one := "1"
		mm := map[string]*string{
			"graph_nongo_lite:%": nil,
			"graph_extra:drop":   nil,
			"graph_extra:keep":   &one,
		}
		for _, key := range liteMarkerKeys() {
			mm[key] = &one
		}

		if err := g.ReplaceAllWithMeta(ctx, deltas, mm); err != nil {
			t.Fatalf("iteration %d: ReplaceAllWithMeta: %v", iter, err)
		}
		assertLiteMarkerState(t, st, "ReplaceAllWithMeta", iter, liteMarkerKeys())
	}
}

// TestApplyFileDeltaWithMetaPrefixDeleteRunsBeforeMarkerSets covers the
// incremental path: a delta can clear the prefix and re-set this file's
// marker in the same map, and the accompanying tree-sitter recompute must see
// the marker (graph_treesitter "0", not "1").
func TestApplyFileDeltaWithMetaPrefixDeleteRunsBeforeMarkerSets(t *testing.T) {
	ctx := context.Background()
	g, st := openTestGraph(t)

	d := FileDelta{
		File: "pylib/one.py", GraphHash: "h1",
		Symbols: []Symbol{mkSym("example.com/fx/pylib.one", "pylib/one.py", 1)},
	}

	for iter := 0; iter < metaOrderIterations; iter++ {
		if err := g.SetMeta(ctx, "graph_nongo_lite:pylib/stale.py", "1"); err != nil {
			t.Fatalf("seeding stale marker: %v", err)
		}
		one := "1"
		mm := map[string]*string{
			"graph_nongo_lite:%": nil,
			"graph_extra:keep":   &one,
		}
		for _, key := range liteMarkerKeys() {
			mm[key] = &one
		}

		if err := g.ApplyFileDeltaWithMeta(ctx, d, mm); err != nil {
			t.Fatalf("iteration %d: ApplyFileDeltaWithMeta: %v", iter, err)
		}
		assertLiteMarkerState(t, st, "ApplyFileDeltaWithMeta", iter, liteMarkerKeys())
		if got, ok := metaValue(t, st, store.MetaGraphTreeSitter); !ok || got != "0" {
			t.Fatalf("iteration %d: %s = %q (exists=%v), want \"0\" beside the lite markers", iter, store.MetaGraphTreeSitter, got, ok)
		}
	}
}

// TestRemoveFileWithMetaPrefixDeleteRunsBeforeMarkerSets covers the deletion
// path: removing one file while clearing the prefix and re-setting the
// surviving file's marker must keep that marker (and graph_treesitter "0").
func TestRemoveFileWithMetaPrefixDeleteRunsBeforeMarkerSets(t *testing.T) {
	ctx := context.Background()
	g, st := openTestGraph(t)

	one := "1"
	dOne := FileDelta{
		File: "pylib/one.py", GraphHash: "h1",
		Symbols: []Symbol{mkSym("example.com/fx/pylib.one", "pylib/one.py", 1)},
	}
	dTwo := FileDelta{
		File: "pylib/two.py", GraphHash: "h2",
		Symbols: []Symbol{mkSym("example.com/fx/pylib.two", "pylib/two.py", 1)},
	}
	if err := g.ApplyFileDelta(ctx, dOne); err != nil {
		t.Fatalf("seeding one.py: %v", err)
	}
	if err := g.ApplyFileDelta(ctx, dTwo); err != nil {
		t.Fatalf("seeding two.py: %v", err)
	}

	for iter := 0; iter < metaOrderIterations; iter++ {
		// one.py is re-created each round; its removal is what the call does.
		if err := g.ApplyFileDelta(ctx, dOne); err != nil {
			t.Fatalf("iteration %d: re-seeding one.py: %v", iter, err)
		}
		for _, key := range []string{"graph_nongo_lite:pylib/one.py", "graph_nongo_lite:pylib/two.py", "graph_nongo_lite:pylib/stale.py"} {
			if err := g.SetMeta(ctx, key, "1"); err != nil {
				t.Fatalf("iteration %d: seeding %s: %v", iter, key, err)
			}
		}
		if err := g.SetMeta(ctx, "graph_extra:drop", "1"); err != nil {
			t.Fatalf("iteration %d: seeding exact-delete key: %v", iter, err)
		}

		mm := map[string]*string{
			"graph_nongo_lite:%":            nil,
			"graph_nongo_lite:pylib/one.py": nil,
			"graph_nongo_lite:pylib/two.py": &one,
			"graph_extra:drop":              nil,
			"graph_extra:keep":              &one,
		}
		if err := g.RemoveFileWithMeta(ctx, "pylib/one.py", mm); err != nil {
			t.Fatalf("iteration %d: RemoveFileWithMeta: %v", iter, err)
		}

		assertLiteMarkerState(t, st, "RemoveFileWithMeta", iter, []string{"graph_nongo_lite:pylib/two.py"})
		// The removed file's own marker must stay deleted even though the
		// prefix deletion also covers it.
		if _, ok := metaValue(t, st, "graph_nongo_lite:pylib/one.py"); ok {
			t.Fatalf("iteration %d: BUG: removed file's marker graph_nongo_lite:pylib/one.py survived", iter)
		}
		if got, ok := metaValue(t, st, store.MetaGraphTreeSitter); !ok || got != "0" {
			t.Fatalf("iteration %d: %s = %q (exists=%v), want \"0\" beside the lite markers", iter, store.MetaGraphTreeSitter, got, ok)
		}
	}
}
