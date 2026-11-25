package main

import (
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

// seedTiers stores one block in each of the medium and long tiers.
func seedTiers(t *testing.T, e *cliEnv) {
	t.Helper()
	for _, a := range [][]string{{"memory", "add", "medium", "scratch note"}, {"memory", "add", "long", "permanent fact"}} {
		if r := e.run(a...); r.Code != 0 {
			t.Fatalf("seed %v: exit %d\n%s%s", a, r.Code, r.Out, r.Err)
		}
	}
	if n := len(bMemList(t, e)); n != 2 {
		t.Fatalf("seeded %d blocks, want 2", n)
	}
}

// Second review F2: every spelling of "one tier" clears only that tier, and
// every malformed form exits 2 and deletes nothing.
func TestCLIMemoryClearTierForms(t *testing.T) {
	for _, args := range [][]string{
		{"memory", "clear", "medium"},
		{"memory", "clear", "--tier", "medium"},
		{"memory", "clear", "--tier=medium"},
		{"memory", "clear", "-t", "medium"},
	} {
		e := newCLIEnv(t)
		seedTiers(t, e)
		r := e.run(args...)
		wantExit(t, r, 0)
		left := bMemList(t, e)
		if len(left) != 1 || string(left[0].Tier) != "long" {
			t.Errorf("%v: left %+v, want only the long block", args, left)
		}
	}
}

func TestCLIMemoryClearMalformedExitsTwo(t *testing.T) {
	for _, args := range [][]string{
		{"memory", "clear", "--tier"},
		{"memory", "clear", "-t"},
		{"memory", "clear", "--tier", "bogus"},
		{"memory", "clear", "--tier", "galaxy"},
		{"memory", "clear", "bogus"},
		{"memory", "clear", "-x"},
		{"memory", "clear", "medium", "long"},
	} {
		e := newCLIEnv(t)
		seedTiers(t, e)
		r := e.run(args...)
		if r.Code != 2 {
			t.Errorf("%v: exit %d, want 2\n%s%s", args, r.Code, r.Out, r.Err)
		}
		if n := len(bMemList(t, e)); n != 2 {
			t.Errorf("%v: %d blocks left, want 2 (nothing deleted)", args, n)
		}
	}
}

func TestCLIMemoryClearBareClearsEverything(t *testing.T) {
	e := newCLIEnv(t)
	seedTiers(t, e)
	wantExit(t, e.run("memory", "clear"), 0)
	if n := len(bMemList(t, e)); n != 0 {
		t.Errorf("bare clear left %d blocks", n)
	}
}

// Second review G1: diagnostics must not migrate or modify the main .db file
// (content hash and mtime unchanged; -wal/-shm side files are not asserted).
func TestDiagnosticsDoNotWriteIndexDatabase(t *testing.T) {
	e := newCGraphEnv(t)
	graphHealthCompleteMeta(t, e.gdir, "fixture")
	dbPath := filepath.Join(e.gdir, "fixture", "fixture.db")

	// Make one migration step pending, as an index from an older gleann has.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM meta WHERE key = 'migration:doc_nodes_rpath'`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	snap := func() (int64, [32]byte) {
		data, err := os.ReadFile(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		return fi.ModTime().UnixNano(), sha256.Sum256(data)
	}
	mt0, h0 := snap()

	e.graph("stats")
	e.run("index", "info", "fixture", "--index-dir", e.gdir)
	warnStaleGraphs(e.gdir, []gleann.IndexMeta{{Name: "fixture", HasGraph: true}}, func(string) {})
	_ = graphHealthSummary(e.gdir, "fixture")

	mt1, h1 := snap()
	if mt0 != mt1 || h0 != h1 {
		t.Fatalf("diagnostics changed the index database (mtime %d -> %d, hash equal=%v)", mt0, mt1, h0 == h1)
	}
	// And the health record is still readable through the read-only open.
	if got := graphHealthSummary(e.gdir, "fixture"); got == "" {
		t.Error("graphHealthSummary empty through the read-only open")
	}
}

// Maintainer decision: `memory forget <phrase>` with several matches exits 2,
// lists them and deletes nothing; --all deletes every match.
func TestCLIMemoryForgetMultiMatchNeedsAll(t *testing.T) {
	e := newCLIEnv(t)
	for _, text := range []string{"deploy runs on friday", "deploy needs a green CI", "unrelated fact"} {
		if r := e.run("memory", "remember", text); r.Code != 0 {
			t.Fatalf("seed %q: exit %d\n%s%s", text, r.Code, r.Out, r.Err)
		}
	}
	r := e.run("memory", "forget", "deploy")
	if r.Code != 2 {
		t.Fatalf("exit %d, want 2\n%s%s", r.Code, r.Out, r.Err)
	}
	for _, want := range []string{"matches 2 memory blocks", "deploy runs on friday", "deploy needs a green CI", "--all"} {
		if !strings.Contains(r.Err, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.Err)
		}
	}
	if n := len(bMemList(t, e)); n != 3 {
		t.Fatalf("refused forget left %d blocks, want 3", n)
	}

	r = e.run("memory", "forget", "--all", "deploy")
	wantExit(t, r, 0)
	mustContain(t, "forget --all", r.Out, "Forgot 2 memory block(s)")
	if n := len(bMemList(t, e)); n != 1 {
		t.Fatalf("after --all %d blocks left, want 1", n)
	}
}
