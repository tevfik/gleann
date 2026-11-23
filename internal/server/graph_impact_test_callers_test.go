package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/internal/graph/analysisgolden"
	"github.com/tevfik/gleann/internal/graphstore"
	"github.com/tevfik/gleann/internal/store"
)

// Roadmap 3.6: the store already computes ImpactResult.TestCallers, so the REST
// impact query must return them as `test_callers` without changing
// total_affected, which stays the production blast radius.

// impSym builds a symbol row the way the graph extractors do.
func impSym(fqn, kind, file string, line int64, isTest bool) graphstore.Symbol {
	return graphstore.Symbol{
		FQN: fqn, Kind: kind, File: file, Name: fqn[strings.LastIndex(fqn, ".")+1:],
		StartLine: line, EndLine: line + 3, StartCol: 1, ByteOffset: line * 30, IsTest: isTest,
	}
}

// newImpactCallersIndex writes a hand-made code graph into a graph-only
// format-2 index (<dir>/impcallers/impcallers.db) and returns its index dir:
//
//	impcallers.Hub        2 production callers + 25 test callers
//	impcallers.NoTests    1 production caller, no test caller
func newImpactCallersIndex(t *testing.T) (indexDir, name string) {
	t.Helper()
	name = "impcallers"
	dir := t.TempDir()
	sub := filepath.Join(dir, name)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, name+".meta.json"), []byte(`{"store_format":2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(sub, name+".db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	g := graphstore.New(st)
	ctx := t.Context()

	hub := graphstore.FileDelta{File: "core/hub.go", Symbols: []graphstore.Symbol{
		impSym(name+".Hub", "function", "core/hub.go", 10, false),
		impSym(name+".NoTests", "function", "core/hub.go", 20, false),
	}}
	prod := graphstore.FileDelta{File: "core/prod.go"}
	for i, c := range []string{name + ".CallerA", name + ".CallerB"} {
		prod.Symbols = append(prod.Symbols, impSym(c, "function", "core/prod.go", int64(1+i*5), false))
		prod.Edges = append(prod.Edges, graphstore.Edge{Src: c, Dst: name + ".Hub"})
	}
	prod.Symbols = append(prod.Symbols, impSym(name+".SingleProd", "function", "core/prod.go", 100, false))
	prod.Edges = append(prod.Edges, graphstore.Edge{Src: name + ".SingleProd", Dst: name + ".NoTests"})

	tests := graphstore.FileDelta{File: "core/hub_test.go"}
	for i := 0; i < 25; i++ {
		c := fmt.Sprintf("%s.TestHub%02d", name, i)
		tests.Symbols = append(tests.Symbols, impSym(c, "function", "core/hub_test.go", int64(1+i*5), true))
		tests.Edges = append(tests.Edges, graphstore.Edge{Src: c, Dst: name + ".Hub"})
	}
	for _, d := range []graphstore.FileDelta{hub, prod, tests} {
		if err := g.ApplyFileDelta(ctx, d); err != nil {
			t.Fatalf("ApplyFileDelta(%s): %v", d.File, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, name
}

// jsonKeysOf decodes a JSON body into an object.
func jsonKeysOf(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("body is not a JSON object: %v\n%s", err, body)
	}
	return v
}

func strSlice(t *testing.T, v any) []string {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("want a JSON array, got %#v", v)
	}
	out := make([]string, len(arr))
	for i, e := range arr {
		s, ok := e.(string)
		if !ok {
			t.Fatalf("array element %#v is not a string", e)
		}
		out[i] = s
	}
	return out
}

// The mock handle stands in for any graphDBHandle: the handler must copy the
// test callers of the handle's ImpactResponse into the body.
func TestHandleGraphQuery_ImpactReturnsTestCallers(t *testing.T) {
	var ir ImpactResponse
	raw := `{"symbol":"pkg.Foo","direct_callers":["pkg.A"],"transitive_callers":["pkg.B"],` +
		`"test_callers":["pkg.TestX","pkg.TestY"],"affected_files":["a.go"],"depth":5}`
	if err := json.Unmarshal([]byte(raw), &ir); err != nil {
		t.Fatal(err)
	}
	s := newTestServerWithGraph(&mockGraphDB{impactResult: &ir})

	rec := restQuery(s, "test-index", GraphQueryRequest{Query: "impact", Symbol: "pkg.Foo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := jsonKeysOf(t, rec.Body.Bytes())
	got := strSlice(t, body["test_callers"])
	if len(got) != 2 || got[0] != "pkg.TestX" || got[1] != "pkg.TestY" {
		t.Errorf("test_callers = %v, want [pkg.TestX pkg.TestY]\n%s", got, rec.Body)
	}
	// Blast radius stays the production caller count.
	if body["total_affected"] != float64(2) {
		t.Errorf("total_affected = %v, want 2 (direct + transitive, tests excluded)", body["total_affected"])
	}
	if n := len(strSlice(t, body["direct_callers"])); n != 1 {
		t.Errorf("direct_callers = %d, want 1", n)
	}
}

// An empty list must marshal as [] (like direct_callers and friends), never
// null and never missing, so clients can tell "no test callers" from "unknown".
func TestHandleGraphQuery_ImpactEmptyTestCallersIsNotNull(t *testing.T) {
	var ir ImpactResponse
	raw := `{"symbol":"pkg.Foo","direct_callers":["pkg.A"],"transitive_callers":[],` +
		`"test_callers":[],"affected_files":["a.go"],"depth":5}`
	if err := json.Unmarshal([]byte(raw), &ir); err != nil {
		t.Fatal(err)
	}
	s := newTestServerWithGraph(&mockGraphDB{impactResult: &ir})

	rec := restQuery(s, "test-index", GraphQueryRequest{Query: "impact", Symbol: "pkg.Foo"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := jsonKeysOf(t, rec.Body.Bytes())
	v, ok := body["test_callers"]
	if !ok {
		t.Fatalf("test_callers missing from:\n%s", rec.Body)
	}
	if got := strSlice(t, v); len(got) != 0 {
		t.Errorf("test_callers = %v, want an empty array", got)
	}
}

// The real storeHandle over the SQLite graph must fill TestCallers from the
// store result and normalize an empty one to []string{}.
func TestStoreHandleImpactPopulatesTestCallers(t *testing.T) {
	dir, name := newImpactCallersIndex(t)
	h, err := openStoreHandle(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	if h == nil {
		t.Fatal("no code graph in the hand-made index")
	}
	t.Cleanup(h.Close)

	res, err := h.Impact(name+".Hub", 3)
	if err != nil {
		t.Fatal(err)
	}
	// Marshaling is the contract check: it fails while the field is absent.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	body := jsonKeysOf(t, raw)
	v, ok := body["test_callers"]
	if !ok {
		t.Fatalf("storeHandle.Impact response has no test_callers:\n%s", raw)
	}
	got := strSlice(t, v)
	if len(got) != 25 {
		t.Fatalf("test_callers = %d entries, want 25:\n%s", len(got), raw)
	}
	seen := map[string]bool{}
	for _, s := range got {
		seen[s] = true
	}
	for _, want := range []string{name + ".TestHub00", name + ".TestHub24"} {
		if !seen[want] {
			t.Errorf("test_callers is missing %s:\n%v", want, got)
		}
	}
	if seen[name+".CallerA"] || seen[name+".Hub"] {
		t.Errorf("production symbols must not leak into test_callers: %v", got)
	}
	if res.TotalAffected != 2 {
		t.Errorf("TotalAffected = %d, want 2 (direct + transitive only)", res.TotalAffected)
	}

	// A symbol with no test caller gets [], not nil.
	empty, err := h.Impact(name+".NoTests", 3)
	if err != nil {
		t.Fatal(err)
	}
	rawEmpty, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	v, ok = jsonKeysOf(t, rawEmpty)["test_callers"]
	if !ok {
		t.Fatalf("test_callers missing from:\n%s", rawEmpty)
	}
	if got := strSlice(t, v); len(got) != 0 {
		t.Errorf("test_callers = %v, want []", got)
	}
	if empty.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1", empty.TotalAffected)
	}
}

// End to end through POST /api/graph/{name}/query on a real format-2 index
// built from the analysis fixture: example.com/fixture/store.NewMemStore is
// called by main and by the fixture's two test functions.
func TestGraphRESTImpactReturnsFixtureTestCallers(t *testing.T) {
	s, _ := newRESTServer(t)
	rec := restQuery(s, analysisgolden.IndexName, GraphQueryRequest{
		Query: "impact", Symbol: "example.com/fixture/store.NewMemStore", MaxDepth: 3,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := jsonKeysOf(t, rec.Body.Bytes())
	got := strSlice(t, body["test_callers"])
	want := map[string]bool{
		"example.com/fixture/service.TestSaveLoad": true,
		"example.com/fixture/service.TestReport":   true,
	}
	if len(got) != len(want) {
		t.Fatalf("test_callers = %v, want the two fixture test functions\n%s", got, rec.Body)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected test caller %q in %s", s, rec.Body)
		}
	}
	if body["total_affected"] != float64(1) {
		t.Errorf("total_affected = %v, want 1 (example.com/fixture.main only)", body["total_affected"])
	}
}
