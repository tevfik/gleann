package graphstore

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/store"
)

// seedTwoFiles gives file a.go (symbols a.One, a.Two) and b.go (b.Caller
// calling a.One), the base fixture for the retraction tests.
func seedTwoFiles(t *testing.T, g *SQLite) {
	t.Helper()
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v1",
		Symbols: []Symbol{mkSym("a.One", "a.go", 1), mkSym("a.Two", "a.go", 10)},
	})
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "b-v1",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		Edges:   []Edge{{Src: "b.Caller", Dst: "a.One", Confidence: "inferred"}},
	})
}

func symbolFiles(t *testing.T, g *SQLite) map[string]string {
	t.Helper()
	rows, err := g.reader.Query(`SELECT fqn, file FROM symbols`)
	if err != nil {
		t.Fatalf("listing symbols: %v", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var fqn, file string
		if err := rows.Scan(&fqn, &file); err != nil {
			t.Fatal(err)
		}
		out[fqn] = file
	}
	return out
}

func edgeCount(t *testing.T, g *SQLite, src, dst string) int {
	t.Helper()
	var n int
	if err := g.reader.QueryRow(`SELECT count(*) FROM edges WHERE src = ? AND dst = ?`, src, dst).Scan(&n); err != nil {
		t.Fatalf("counting edges: %v", err)
	}
	return n
}

func graphHash(t *testing.T, g *SQLite, file string) string {
	t.Helper()
	var h *string
	if err := g.reader.QueryRow(`SELECT graph_hash FROM files WHERE path = ?`, file).Scan(&h); err != nil {
		t.Fatalf("reading graph_hash of %s: %v", file, err)
	}
	if h == nil {
		return ""
	}
	return *h
}

// TestApplyFileDeltaRollbackOnInjectedFailure injects a trigger that aborts
// symbol inserts mid-delta (RFC 003 section 7.1 "ApplyFileDelta rollback"):
// the transaction must roll back completely, leaving symbols, inbound
// edges, graph_hash and the dirty flags exactly as they were.
func TestApplyFileDeltaRollbackOnInjectedFailure(t *testing.T) {
	g, s := openTestGraph(t)
	ctx := context.Background()
	seedTwoFiles(t, g)

	if _, err := s.WriterDB().ExecContext(ctx, `CREATE TRIGGER injected_failure BEFORE INSERT ON symbols
BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("creating trigger: %v", err)
	}
	err := g.ApplyFileDelta(ctx, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.One", "a.go", 100), mkSym("a.Three", "a.go", 200)},
		Edges:   []Edge{{Src: "a.Three", Dst: "a.One"}},
	})
	if err == nil || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("ApplyFileDelta error = %v, want the injected failure", err)
	}
	if _, err := s.WriterDB().ExecContext(ctx, `DROP TRIGGER injected_failure`); err != nil {
		t.Fatalf("dropping trigger: %v", err)
	}

	wantSyms := map[string]string{"a.One": "a.go", "a.Two": "a.go", "b.Caller": "b.go"}
	if got := symbolFiles(t, g); !reflect.DeepEqual(got, wantSyms) {
		t.Fatalf("symbols after rollback = %v, want %v", got, wantSyms)
	}
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 1 {
		t.Fatalf("inbound edge count after rollback = %d, want 1", n)
	}
	if h := graphHash(t, g, "a.go"); h != "a-v1" {
		t.Fatalf("graph_hash after rollback = %q, want a-v1 (manifest must not advance)", h)
	}
	if dirty, err := g.DirtyFiles(); err != nil || len(dirty) != 0 {
		t.Fatalf("DirtyFiles after rollback = %v, %v; want none", dirty, err)
	}

	// A validation failure after the deletes (an edge from a symbol the
	// file does not declare) must roll back just the same.
	err = g.ApplyFileDelta(ctx, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.One", "a.go", 100)},
		Edges:   []Edge{{Src: "a.Ghost", Dst: "a.One"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no declaration") {
		t.Fatalf("dangling-edge delta error = %v, want endpoint error", err)
	}
	if got := symbolFiles(t, g); !reflect.DeepEqual(got, wantSyms) {
		t.Fatalf("symbols after dangling-edge rollback = %v, want %v", got, wantSyms)
	}
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 1 {
		t.Fatalf("inbound edge count after dangling-edge rollback = %d, want 1", n)
	}

	// The same file applies cleanly once the delta is valid.
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.One", "a.go", 100), mkSym("a.Three", "a.go", 200)},
		Edges:   []Edge{{Src: "a.Three", Dst: "a.One"}},
	})
	if h := graphHash(t, g, "a.go"); h != "a-v2" {
		t.Fatalf("graph_hash after successful delta = %q, want a-v2", h)
	}
}

// TestInboundEdgeRestoredWhenSymbolSurvives: a delta that keeps the target
// symbol (new anchors) deletes the inbound edge in step 2 and restores it in
// step 4, with its original owner and confidence; the owner is not dirty.
func TestInboundEdgeRestoredWhenSymbolSurvives(t *testing.T) {
	g, _ := openTestGraph(t)
	seedTwoFiles(t, g)
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.One", "a.go", 50), mkSym("a.Two", "a.go", 60)},
	})
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 1 {
		t.Fatalf("inbound edge count = %d, want restored 1", n)
	}
	var owner, confidence string
	if err := g.reader.QueryRow(`SELECT file, confidence FROM edges WHERE src = 'b.Caller' AND dst = 'a.One'`).Scan(&owner, &confidence); err != nil {
		t.Fatalf("reading restored edge: %v", err)
	}
	if owner != "b.go" || confidence != "inferred" {
		t.Fatalf("restored edge owner/confidence = %q/%q, want b.go/inferred", owner, confidence)
	}
	if dirty, err := g.DirtyFiles(); err != nil || len(dirty) != 0 {
		t.Fatalf("DirtyFiles = %v, %v; want none when the symbol survived", dirty, err)
	}
}

// TestInboundEdgeDeletedAndDirtyWhenSymbolGone covers RFC 003 section 6.5
// step 4's other branch: the inbound edge is deleted, its owner is listed by
// DirtyFiles, and the owner's next delta clears the flag.
func TestInboundEdgeDeletedAndDirtyWhenSymbolGone(t *testing.T) {
	g, _ := openTestGraph(t)
	seedTwoFiles(t, g)

	// a.One disappears from a.go's delta.
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.Two", "a.go", 10)},
	})
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 0 {
		t.Fatalf("inbound edge count = %d, want deleted 0", n)
	}
	dirty, err := g.DirtyFiles()
	if err != nil || !reflect.DeepEqual(dirty, []string{"b.go"}) {
		t.Fatalf("DirtyFiles = %v, %v; want [b.go]", dirty, err)
	}

	// The symbol reappearing later does not resurrect the edge by itself:
	// the referencing file must be re-extracted (that is what dirty means).
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v3",
		Symbols: []Symbol{mkSym("a.One", "a.go", 1), mkSym("a.Two", "a.go", 10)},
	})
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 0 {
		t.Fatalf("inbound edge count = %d, want still 0 until b.go re-extracts", n)
	}

	// The next sync re-applies the dirty file's delta; the flag clears in
	// the same transaction (section 6.5 consumer).
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "b-v2",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		Edges:   []Edge{{Src: "b.Caller", Dst: "a.One"}},
	})
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 1 {
		t.Fatalf("inbound edge count after re-extraction = %d, want 1", n)
	}
	if dirty, err := g.DirtyFiles(); err != nil || len(dirty) != 0 {
		t.Fatalf("DirtyFiles after re-extraction = %v, %v; want cleared", dirty, err)
	}
}

// TestExternalTargetRetractedWithOwner: dst_external edges never create
// symbol rows and are retracted only with their owning file (RFC 003
// section 3.4 and 6.5).
func TestExternalTargetRetractedWithOwner(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	seedTwoFiles(t, g)
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "b-v2",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		Edges: []Edge{
			{Src: "b.Caller", Dst: "a.One"},
			{Src: "b.Caller", Dst: "ext.Std", DstExternal: true},
		},
	})
	if _, ok := symbolFiles(t, g)["ext.Std"]; ok {
		t.Fatal("external target became a symbol row (phantom declaration)")
	}
	// External references are still queryable as call targets of their owner.
	if hops, err := g.Callers("ext.Std", 1); err != nil || len(hops) != 1 || hops[0].FQN != "b.Caller" {
		t.Fatalf("Callers(ext.Std) = %+v, %v; want b.Caller", hops, err)
	}

	if err := g.RemoveFile(ctx, "b.go"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	if n := edgeCount(t, g, "b.Caller", "ext.Std"); n != 0 {
		t.Fatalf("external edge count after RemoveFile = %d, want 0", n)
	}
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 0 {
		t.Fatalf("internal edge count after RemoveFile = %d, want 0", n)
	}
	if got := symbolFiles(t, g); !reflect.DeepEqual(got, map[string]string{"a.One": "a.go", "a.Two": "a.go"}) {
		t.Fatalf("symbols after RemoveFile = %v, want a.go's only", got)
	}
	if h := graphHash(t, g, "b.go"); h != "" {
		t.Fatalf("graph_hash of removed file = %q, want cleared", h)
	}

	// An empty delta retracts the external edge the same way.
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "b-v3",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		Edges:   []Edge{{Src: "b.Caller", Dst: "ext.Std", DstExternal: true}},
	})
	mustApply(t, g, FileDelta{File: "b.go", GraphHash: "b-v4", Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)}})
	if n := edgeCount(t, g, "b.Caller", "ext.Std"); n != 0 {
		t.Fatalf("external edge count after empty delta = %d, want 0", n)
	}
}

// TestRemoveFileMarksReferrersDirty: deleting a file retracts inbound edges
// and marks their owners dirty, exactly like a delta that drops the symbol.
func TestRemoveFileMarksReferrersDirty(t *testing.T) {
	g, _ := openTestGraph(t)
	seedTwoFiles(t, g)
	if err := g.RemoveFile(context.Background(), "a.go"); err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 0 {
		t.Fatalf("inbound edge count = %d, want 0", n)
	}
	dirty, err := g.DirtyFiles()
	if err != nil || !reflect.DeepEqual(dirty, []string{"b.go"}) {
		t.Fatalf("DirtyFiles = %v, %v; want [b.go]", dirty, err)
	}
}

// TestDuplicateFQNAcrossFilesIsError: RFC 003 section 6.5 requires an error,
// not an ignored conflict, when two files declare the same FQN; both files'
// previous state must survive.
func TestDuplicateFQNAcrossFilesIsError(t *testing.T) {
	g, _ := openTestGraph(t)
	seedTwoFiles(t, g)
	err := g.ApplyFileDelta(context.Background(), FileDelta{
		File: "b.go", GraphHash: "b-v2",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1), mkSym("a.One", "b.go", 99)},
	})
	if err == nil || !strings.Contains(err.Error(), "already declared") || !strings.Contains(err.Error(), "a.go") {
		t.Fatalf("duplicate FQN error = %v, want it to name the owning file a.go", err)
	}
	wantSyms := map[string]string{"a.One": "a.go", "a.Two": "a.go", "b.Caller": "b.go"}
	if got := symbolFiles(t, g); !reflect.DeepEqual(got, wantSyms) {
		t.Fatalf("symbols after duplicate error = %v, want %v", got, wantSyms)
	}
	if h := graphHash(t, g, "b.go"); h != "b-v1" {
		t.Fatalf("graph_hash of b.go = %q, want b-v1 (rolled back)", h)
	}

	// The same FQN twice within one delta is the same error class.
	err = g.ApplyFileDelta(context.Background(), FileDelta{
		File:    "c.go",
		Symbols: []Symbol{mkSym("c.X", "c.go", 1), mkSym("c.X", "c.go", 2)},
	})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("within-delta duplicate error = %v, want duplicate error", err)
	}
	if _, ok := symbolFiles(t, g)["c.X"]; ok {
		t.Fatal("c.X survived a failed delta")
	}
}

// TestRandomDeltaSequenceIntegrity: after any random sequence of deltas and
// removals, no edge may have a missing internal endpoint (RFC 003 section
// 7.1, AGENTS.md section 2.3). Deltas are generated against the current
// symbol set so they are valid; symbols appear and disappear across files.
func TestRandomDeltaSequenceIntegrity(t *testing.T) {
	for _, seed := range []int64{5, 6, 7} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			g, _ := openTestGraph(t)
			ctx := context.Background()
			rng := rand.New(rand.NewSource(seed))
			files := []string{"f0.go", "f1.go", "f2.go", "f3.go", "f4.go"}
			baseSyms := make(map[string][]Symbol)
			for i, f := range files {
				for j := 0; j < 8; j++ {
					baseSyms[f] = append(baseSyms[f], mkSym(fmt.Sprintf("p%d.S%d", i, j), f, int64(j+1)))
				}
			}
			// currentSyms lists the declarations that will still exist when
			// the delta for excludeFile commits: the file's own old symbols
			// are about to be retracted, so they must not be edge targets.
			currentSyms := func(excludeFile string) []string {
				rows, err := g.reader.Query(`SELECT fqn FROM symbols WHERE file <> ?`, excludeFile)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				var out []string
				for rows.Next() {
					var f string
					if err := rows.Scan(&f); err != nil {
						t.Fatal(err)
					}
					out = append(out, f)
				}
				return out
			}
			checkIntegrity := func(step int) {
				t.Helper()
				var n int
				err := g.reader.QueryRow(`SELECT count(*) FROM edges e
WHERE NOT EXISTS (SELECT 1 FROM symbols s WHERE s.fqn = e.src)
   OR (e.dst_external = 0 AND NOT EXISTS (SELECT 1 FROM symbols s WHERE s.fqn = e.dst))`).Scan(&n)
				if err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatalf("step %d: %d edges with a missing internal endpoint", step, n)
				}
			}
			// genDelta builds a valid delta: a random subset of the file's
			// symbols, edges to symbols that exist now or in this delta, and
			// occasional external targets.
			genDelta := func(f string) FileDelta {
				var syms []Symbol
				for _, s := range baseSyms[f] {
					if rng.Intn(3) != 0 { // ~2/3 survive, so symbols come and go
						s.StartLine += int64(rng.Intn(5))
						syms = append(syms, s)
					}
				}
				known := append(currentSyms(f), fqnsOf(syms)...)
				var edges []Edge
				for _, s := range syms {
					for k := 0; k < 3; k++ {
						if len(known) == 0 || rng.Intn(4) == 0 {
							edges = append(edges, Edge{Src: s.FQN, Dst: fmt.Sprintf("ext.X%d", rng.Intn(3)), DstExternal: true})
							continue
						}
						edges = append(edges, Edge{Src: s.FQN, Dst: known[rng.Intn(len(known))]})
					}
				}
				return FileDelta{File: f, GraphHash: fmt.Sprintf("h%d", rng.Int63()), Symbols: syms, Edges: edges}
			}

			for _, f := range files {
				mustApply(t, g, genDelta(f))
				checkIntegrity(0)
			}
			for step := 1; step <= 60; step++ {
				f := files[rng.Intn(len(files))]
				if rng.Intn(5) == 0 {
					if err := g.RemoveFile(ctx, f); err != nil {
						t.Fatalf("step %d RemoveFile(%s): %v", step, f, err)
					}
				} else {
					mustApply(t, g, genDelta(f))
				}
				checkIntegrity(step)
			}

			// Final pass: restore every file's full symbol set with
			// self-contained edges. No symbol disappears, so no inbound edge
			// breaks and no file is dirtied; all flags clear.
			for _, f := range files {
				syms := baseSyms[f]
				var edges []Edge
				for i, s := range syms {
					edges = append(edges, Edge{Src: s.FQN, Dst: syms[(i+1)%len(syms)].FQN})
				}
				mustApply(t, g, FileDelta{File: f, GraphHash: "final", Symbols: syms, Edges: edges})
			}
			if dirty, err := g.DirtyFiles(); err != nil || len(dirty) != 0 {
				t.Fatalf("DirtyFiles after re-extracting all files = %v, %v; want none", dirty, err)
			}
			checkIntegrity(999)
		})
	}
}

func fqnsOf(syms []Symbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.FQN)
	}
	return out
}

// A reference to an internal symbol that is not declared yet (its file is
// applied later in a fresh build) does not block the delta: the edge is not
// written, the file is dirty, and re-applying it after the target exists
// resolves the edge and clears the flag.
func TestUnresolvedTargetMarksDirtyAndResolvesLater(t *testing.T) {
	g, _ := openTestGraph(t)
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v1",
		Symbols: []Symbol{mkSym("a.Caller", "a.go", 1)},
		Edges:   []Edge{{Src: "a.Caller", Dst: "b.Later"}},
	})
	if n := edgeCount(t, g, "a.Caller", "b.Later"); n != 0 {
		t.Fatalf("edge to an undeclared target was written (%d)", n)
	}
	if h := graphHash(t, g, "a.go"); h != "a-v1" {
		t.Fatalf("graph_hash = %q, want a-v1: the delta must commit", h)
	}
	if dirty, err := g.DirtyFiles(); err != nil || !reflect.DeepEqual(dirty, []string{"a.go"}) {
		t.Fatalf("DirtyFiles = %v, %v; want [a.go]", dirty, err)
	}

	mustApply(t, g, FileDelta{File: "b.go", GraphHash: "b-v1", Symbols: []Symbol{mkSym("b.Later", "b.go", 1)}})
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v1",
		Symbols: []Symbol{mkSym("a.Caller", "a.go", 1)},
		Edges:   []Edge{{Src: "a.Caller", Dst: "b.Later"}},
	})
	if n := edgeCount(t, g, "a.Caller", "b.Later"); n != 1 {
		t.Fatalf("edge after the target exists = %d, want 1", n)
	}
	if dirty, err := g.DirtyFiles(); err != nil || len(dirty) != 0 {
		t.Fatalf("DirtyFiles after resolution = %v, %v; want none", dirty, err)
	}
}

// TestEdgeSourceMustBeDeclaredInSameFile: an edge whose src is declared, but
// by another file, is an extractor bug and must be refused (AGENTS.md
// section 2.3: the caller of a reference is a declaration of the file
// itself). Existence of the FQN somewhere in symbols is not enough.
func TestEdgeSourceMustBeDeclaredInSameFile(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	seedTwoFiles(t, g)
	err := g.ApplyFileDelta(ctx, FileDelta{
		File: "b.go", GraphHash: "b-v2",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		// a.One exists, but it is declared by a.go, not by this delta's file.
		Edges: []Edge{{Src: "a.One", Dst: "a.Two"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no declaration") {
		t.Fatalf("foreign-source delta error = %v, want a declaration error", err)
	}
	if h := graphHash(t, g, "b.go"); h != "b-v1" {
		t.Fatalf("graph_hash of b.go = %q, want b-v1 (rolled back)", h)
	}
	if n := edgeCount(t, g, "a.One", "a.Two"); n != 0 {
		t.Fatalf("foreign-source edge was written (%d)", n)
	}
	if n := edgeCount(t, g, "b.Caller", "a.One"); n != 1 {
		t.Fatalf("b.go's old edge count = %d, want 1 (rolled back)", n)
	}
}

// TestRestoreInboundMarksEachFileOnce: a file with several broken inbound
// references is marked graph_dirty once, not once per edge. A trigger
// counts the dirty upserts so the per-edge writes of the old code are
// observable (RFC 003 section 6.5 step 4).
func TestRestoreInboundMarksEachFileOnce(t *testing.T) {
	g, s := openTestGraph(t)
	ctx := context.Background()
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v1",
		Symbols: []Symbol{mkSym("a.One", "a.go", 1), mkSym("a.Two", "a.go", 10)},
	})
	// Two referencing files; b.go points at both disappearing symbols.
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "b-v1",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
		Edges:   []Edge{{Src: "b.Caller", Dst: "a.One"}, {Src: "b.Caller", Dst: "a.Two"}},
	})
	mustApply(t, g, FileDelta{
		File: "c.go", GraphHash: "c-v1",
		Symbols: []Symbol{mkSym("c.Caller", "c.go", 1)},
		Edges:   []Edge{{Src: "c.Caller", Dst: "a.One"}},
	})

	if _, err := s.WriterDB().ExecContext(ctx, `CREATE TABLE dirty_marks (n INTEGER NOT NULL)`); err != nil {
		t.Fatalf("creating counter: %v", err)
	}
	if _, err := s.WriterDB().ExecContext(ctx, `INSERT INTO dirty_marks VALUES (0)`); err != nil {
		t.Fatalf("seeding counter: %v", err)
	}
	// Count only the dirty-setting upserts (graph_dirty = 1); the delta's
	// own files row clears its flag and must not be counted.
	if _, err := s.WriterDB().ExecContext(ctx, `CREATE TRIGGER count_dirty AFTER UPDATE OF graph_dirty ON files
WHEN NEW.graph_dirty = 1 BEGIN UPDATE dirty_marks SET n = n + 1; END`); err != nil {
		t.Fatalf("creating trigger: %v", err)
	}

	// Both of a.go's symbols disappear: b.go loses two inbound edges, c.go
	// one. Each file needs re-extraction once.
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.Three", "a.go", 1)},
	})
	var marks int
	if err := s.WriterDB().QueryRowContext(ctx, `SELECT n FROM dirty_marks`).Scan(&marks); err != nil {
		t.Fatalf("reading counter: %v", err)
	}
	if marks != 2 {
		t.Fatalf("dirty marks = %d, want 2 (one per referencing file, not per edge)", marks)
	}
	if _, err := s.WriterDB().ExecContext(ctx, `DROP TRIGGER count_dirty`); err != nil {
		t.Fatalf("dropping trigger: %v", err)
	}
	dirty, err := g.DirtyFiles()
	if err != nil || !reflect.DeepEqual(dirty, []string{"b.go", "c.go"}) {
		t.Fatalf("DirtyFiles = %v, %v; want [b.go c.go]", dirty, err)
	}
}

// TestApplyFileDeltaNormalisesBackslashPaths: a delta carrying Windows-style
// paths stores them in the slash form the tables use, on every OS (the path
// string is built with explicit backslashes, not filepath.Join). RemoveFile
// accepts the same spelling.
func TestApplyFileDeltaNormalisesBackslashPaths(t *testing.T) {
	g, _ := openTestGraph(t)
	ctx := context.Background()
	win := `pkg\sub\a.go`
	mustApply(t, g, FileDelta{
		File: win, GraphHash: "h1",
		Symbols: []Symbol{mkSym("a.F", win, 1), mkSym("a.G", "", 5)}, // "" defaults to the delta's file
		Edges:   []Edge{{Src: "a.F", Dst: "a.G"}},
	})
	want := map[string]string{"a.F": "pkg/sub/a.go", "a.G": "pkg/sub/a.go"}
	if got := symbolFiles(t, g); !reflect.DeepEqual(got, want) {
		t.Fatalf("symbol anchors = %v, want %v", got, want)
	}
	var path, edgeFile string
	if err := g.reader.QueryRow(`SELECT path FROM files`).Scan(&path); err != nil {
		t.Fatalf("reading files row: %v", err)
	}
	if path != "pkg/sub/a.go" {
		t.Fatalf("files.path = %q, want pkg/sub/a.go", path)
	}
	if err := g.reader.QueryRow(`SELECT file FROM edges WHERE src = 'a.F'`).Scan(&edgeFile); err != nil {
		t.Fatalf("reading edge owner: %v", err)
	}
	if edgeFile != "pkg/sub/a.go" {
		t.Fatalf("edges.file = %q, want pkg/sub/a.go", edgeFile)
	}
	if err := g.RemoveFile(ctx, win); err != nil {
		t.Fatalf("RemoveFile(%q): %v", win, err)
	}
	if got := symbolFiles(t, g); len(got) != 0 {
		t.Fatalf("symbols after RemoveFile = %v, want none", got)
	}
}

// TestGraph06SyncFailureLeavesGraphAndManifestUnchanged is RFC 003 section
// 7.1 GRAPH-06: the passages and the graph delta of one file commit in ONE
// transaction (section 6.5). A graph failure after ApplySourceTx must roll
// back the passages, the lex rows and files.hash too, so the next sync
// retries the file instead of skipping it on an advanced hash.
func TestGraph06SyncFailureLeavesGraphAndManifestUnchanged(t *testing.T) {
	g, s := openTestGraph(t)
	ctx := context.Background()
	mustApply(t, g, FileDelta{
		File: "a.go", GraphHash: "a-v1",
		Symbols: []Symbol{mkSym("a.One", "a.go", 1)},
	})
	mustApply(t, g, FileDelta{
		File: "b.go", GraphHash: "b-v1",
		Symbols: []Symbol{mkSym("b.Caller", "b.go", 1)},
	})
	if _, err := s.ApplySource(ctx, store.SourceDelta{
		Source:   "a.go",
		File:     store.FileRow{Hash: "v1"},
		Passages: []store.NewPassage{{Text: "old text", Tokens: "old text"}},
	}); err != nil {
		t.Fatalf("ApplySource: %v", err)
	}
	lexMatches := func(q string) int {
		t.Helper()
		var n int
		if err := g.reader.QueryRow(`SELECT count(*) FROM lex WHERE lex MATCH ?`, q).Scan(&n); err != nil {
			t.Fatalf("lex MATCH %q: %v", q, err)
		}
		return n
	}

	// One transaction: the new passages first, then a graph delta that fails
	// because b.Caller is already declared by b.go.
	tx, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	ids, err := s.ApplySourceTx(ctx, tx, store.SourceDelta{
		Source: "a.go",
		File:   store.FileRow{Hash: "v2"},
		Passages: []store.NewPassage{
			{Text: "new text", Tokens: "new text"},
			{Text: "more new", Tokens: "more new"},
		},
	})
	if err != nil {
		tx.Rollback()
		t.Fatalf("ApplySourceTx: %v", err)
	}
	if len(ids) != 2 {
		tx.Rollback()
		t.Fatalf("ApplySourceTx ids = %v, want 2", ids)
	}
	err = g.ApplyFileDeltaTx(ctx, tx, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.One", "a.go", 1), mkSym("b.Caller", "a.go", 9)},
	})
	if err == nil || !strings.Contains(err.Error(), "already declared") {
		tx.Rollback()
		t.Fatalf("ApplyFileDeltaTx error = %v, want the duplicate-declaration failure", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// Nothing advanced: passages, lex rows, files.hash and graph rows are
	// all exactly the old state.
	if n, err := s.Count(ctx); err != nil || n != 1 {
		t.Fatalf("Count after rollback = %d, %v; want 1", n, err)
	}
	if p, err := s.Get(ctx, 1); err != nil || p.Text != "old text" {
		t.Fatalf("passage after rollback = %+v, %v; want old text", p, err)
	}
	if lexMatches("old") != 1 || lexMatches("new") != 0 {
		t.Fatalf("lex rows after rollback: old=%d new=%d, want 1/0", lexMatches("old"), lexMatches("new"))
	}
	files, err := s.Files(ctx)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if files["a.go"].Hash != "v1" {
		t.Fatalf("files.hash advanced despite the graph failure: %+v", files["a.go"])
	}
	if h := graphHash(t, g, "a.go"); h != "a-v1" {
		t.Fatalf("graph_hash after rollback = %q, want a-v1", h)
	}
	wantSyms := map[string]string{"a.One": "a.go", "b.Caller": "b.go"}
	if got := symbolFiles(t, g); !reflect.DeepEqual(got, wantSyms) {
		t.Fatalf("symbols after rollback = %v, want %v", got, wantSyms)
	}
	if dirty, err := g.DirtyFiles(); err != nil || len(dirty) != 0 {
		t.Fatalf("DirtyFiles after rollback = %v, %v; want none", dirty, err)
	}

	// The next sync retries the file (its hash never advanced) and succeeds
	// with both halves in one transaction.
	tx2, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatalf("BeginWrite retry: %v", err)
	}
	defer tx2.Rollback()
	if _, err := s.ApplySourceTx(ctx, tx2, store.SourceDelta{
		Source: "a.go",
		File:   store.FileRow{Hash: "v2"},
		Passages: []store.NewPassage{
			{Text: "new text", Tokens: "new text"},
			{Text: "more new", Tokens: "more new"},
		},
	}); err != nil {
		t.Fatalf("ApplySourceTx retry: %v", err)
	}
	if err := g.ApplyFileDeltaTx(ctx, tx2, FileDelta{
		File: "a.go", GraphHash: "a-v2",
		Symbols: []Symbol{mkSym("a.One", "a.go", 1), mkSym("a.Two", "a.go", 10)},
		Edges:   []Edge{{Src: "a.Two", Dst: "a.One"}},
	}); err != nil {
		t.Fatalf("ApplyFileDeltaTx retry: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatalf("Commit retry: %v", err)
	}
	if n, err := s.Count(ctx); err != nil || n != 2 {
		t.Fatalf("Count after commit = %d, %v; want 2", n, err)
	}
	if lexMatches("new") != 2 || lexMatches("old") != 0 {
		t.Fatalf("lex rows after commit: new=%d old=%d, want 2/0", lexMatches("new"), lexMatches("old"))
	}
	files, err = s.Files(ctx)
	if err != nil {
		t.Fatalf("Files after commit: %v", err)
	}
	if files["a.go"].Hash != "v2" {
		t.Fatalf("files.hash after commit = %q, want v2", files["a.go"].Hash)
	}
	if h := graphHash(t, g, "a.go"); h != "a-v2" {
		t.Fatalf("graph_hash after commit = %q, want a-v2", h)
	}
	if n := edgeCount(t, g, "a.Two", "a.One"); n != 1 {
		t.Fatalf("new edge count after commit = %d, want 1", n)
	}
}
