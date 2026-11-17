//go:build treesitter && !windows

package analysisgolden

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	kgraph "github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/pkg/gleann"
)

// symbolCalls is the recorded answer for one symbol.
type symbolCalls struct {
	Kind    string          `json:"kind"`
	File    string          `json:"file"`
	Line    int64           `json:"line"`
	Callees []gleann.Callee `json:"callees"`
	Callers []gleann.Callee `json:"callers"`
}

func sortCallees(in []gleann.Callee) []gleann.Callee {
	out := append([]gleann.Callee{}, in...)
	sort.Slice(out, func(i, j int) bool { return out[i].FQN < out[j].FQN })
	return out
}

// callsBaseline lists every symbol of the Kùzu graph whose FQN starts with one
// of the prefixes, with its callees and callers as Kùzu answers them.
func callsBaseline(t *testing.T, db *kgraph.DB, prefixes []string) map[string]symbolCalls {
	t.Helper()
	// SymbolSearch is capped at 50 rows in engine order, so enumerate each
	// prefix separately (each has far fewer than 50 symbols).
	var all []gleann.Callee
	for _, p := range prefixes {
		found, err := db.SymbolSearch(p)
		if err != nil {
			t.Fatalf("SymbolSearch(%s): %v", p, err)
		}
		if len(found) >= 50 {
			t.Fatalf("SymbolSearch(%s) hit the 50-row cap", p)
		}
		all = append(all, found...)
	}
	out := map[string]symbolCalls{}
	for _, s := range all {
		match := false
		for _, p := range prefixes {
			if strings.HasPrefix(s.FQN, p) {
				match = true
			}
		}
		if !match {
			continue
		}
		callees, err := db.Callees(s.FQN)
		if err != nil {
			t.Fatalf("Callees(%s): %v", s.FQN, err)
		}
		callers, err := db.Callers(s.FQN)
		if err != nil {
			t.Fatalf("Callers(%s): %v", s.FQN, err)
		}
		out[s.FQN] = symbolCalls{Kind: s.Kind, File: s.File, Line: s.Line,
			Callees: sortCallees(callees), Callers: sortCallees(callers)}
	}
	return out
}

// TestKuzuCallsBaseline freezes the CALLS edges Kùzu holds for the fixture
// (RFC 004 A0 item 3). The Python, TypeScript and Rust edges come from the
// tree-sitter extractor (internal/graph/indexer/ts_calls.go): RFC 004 A1 must
// reproduce them on the SQLite graph. calls_go.json is the same record for the
// Go packages (go/ast extractor). Both include the file-less callee stubs
// (builtins and member calls such as py.strip, ts.map): Kùzu stores them as
// Symbol rows without a source anchor, which AGENTS.md section 2 forbids for
// first-party symbols, so A1 is expected to differ there and must say so.
func TestKuzuCallsBaseline(t *testing.T) {
	env := BuildIndex(t)
	db, err := kgraph.Open(filepath.Join(env.IndexDir, IndexName+"_graph"))
	if err != nil {
		t.Fatalf("kuzu open: %v", err)
	}
	defer db.Close()

	for _, c := range []struct {
		golden   string
		prefixes []string
	}{
		{"calls_nongo.json", []string{"example.com/fixture/py.", "example.com/fixture/ts.", "example.com/fixture/rs."}},
		{"calls_go.json", []string{"example.com/fixture.", "example.com/fixture/service.", "example.com/fixture/store."}},
	} {
		got := callsBaseline(t, db, c.prefixes)
		if len(got) == 0 {
			t.Fatalf("%s: no symbols", c.golden)
		}
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		Check(t, c.golden, env.CanonicalJSON(t, string(b)))
	}
}
