package memory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func newImportStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func rowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.reader.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// goldenTraverse reads golden/traverse_<name>.json: the v1.9.0 response body
// is the first line, an "HTTP 200" status line follows.
func goldenTraverse(t *testing.T, name string) (nodes []gleann.MemoryGraphNode, edges []gleann.MemoryGraphEdge) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "v190", "golden", "traverse_"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if !sc.Scan() {
		t.Fatalf("%s: empty golden", name)
	}
	var body struct {
		Nodes []gleann.MemoryGraphNode `json:"nodes"`
		Edges []gleann.MemoryGraphEdge `json:"edges"`
	}
	if err := json.Unmarshal(sc.Bytes(), &body); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return body.Nodes, body.Edges
}

func nodeSet(ns []gleann.MemoryGraphNode) map[string]string {
	out := map[string]string{}
	for _, n := range ns {
		a, _ := json.Marshal(n.Attributes) // map keys marshal sorted
		out[n.ID] = n.Type + "|" + n.Content + "|" + string(a)
	}
	return out
}

// edgeSet keys edges by from|to|type|weight|attributes with the timestamps
// replaced by <TS>, as the goldens record them. Weight 0 is omitted in the
// v1.9.0 JSON (omitempty), so it is omitted here too.
func edgeSet(es []gleann.MemoryGraphEdge) map[string]bool {
	out := map[string]bool{}
	for _, e := range es {
		attrs := map[string]any{}
		for k, v := range e.Attributes {
			if k == "created_at" || k == "updated_at" {
				v = "<TS>"
			}
			attrs[k] = v
		}
		a, _ := json.Marshal(attrs)
		out[fmt.Sprintf("%s|%s|%s|%v|%s", e.From, e.To, e.RelationType, e.Weight, a)] = true
	}
	return out
}

func assertSameSet[K comparable, V comparable](t *testing.T, what string, got, want map[K]V) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", what, got, want)
		return
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s: got %v, want %v", what, got, want)
			return
		}
	}
}

// TestImportGraph_V190Fixture replays the export of a real gleann v1.9.0
// store (testdata/v190/mem.kuzu-export.json, made by tools/kuzu-memory-export
// from gleann-review/fixtures/v190-memory) and compares traverse results with
// the goldens the v1.9.0 server returned. tools/kuzu-memory-export's own test
// checks that the committed export equals a fresh export of the real store.
func TestImportGraph_V190Fixture(t *testing.T) {
	s := newImportStore(t)
	file := filepath.Join("testdata", "v190", "mem.kuzu-export.json")

	r1, err := s.ImportGraphFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if r1.AlreadyImported || r1.Entities != 8 || r1.Relations != 8 || r1.Archive != 2 {
		t.Fatalf("first import: %+v", r1)
	}
	archiveAfterFirst := rowCount(t, s, "entity_archive")
	entitiesAfterFirst := rowCount(t, s, "entities")
	relationsAfterFirst := rowCount(t, s, "relations")
	if archiveAfterFirst != 2 {
		t.Fatalf("entity_archive rows = %d, want 2", archiveAfterFirst)
	}

	// A second run (same file) changes nothing.
	r2, err := s.ImportGraphFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.AlreadyImported || r2.Entities+r2.Relations+r2.Archive != 0 {
		t.Fatalf("second import: %+v", r2)
	}
	if got := rowCount(t, s, "entity_archive"); got != archiveAfterFirst {
		t.Fatalf("entity_archive rows changed by the second import: %d -> %d", archiveAfterFirst, got)
	}
	if rowCount(t, s, "entities") != entitiesAfterFirst || rowCount(t, s, "relations") != relationsAfterFirst {
		t.Fatal("second import changed entities or relations")
	}
	if m, _ := s.GraphImportMarker("mem"); !strings.Contains(m, `"sha256"`) || !strings.Contains(m, "mem.kuzu-export.json") {
		t.Fatalf("marker = %q", m)
	}

	svc := s.EntityService("mem", nil)
	cases := []struct {
		golden string
		start  string
		depth  int
	}{
		{"proj_d1", "proj", 1}, {"proj_d2", "proj", 2}, {"proj_d3", "proj", 3},
		{"alice_d1", "alice", 1}, {"alice_d2", "alice", 2},
		{"note1_d2", "note1", 2}, {"decision_d3", "decision", 3},
		{"ghost_d1", "ghost", 1},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			wantNodes, wantEdges := goldenTraverse(t, c.golden)
			nodes, edges, err := svc.Traverse(context.Background(), c.start, c.depth)
			if err != nil {
				t.Fatal(err)
			}
			if c.golden == "ghost_d1" {
				// v1.9.0 BUG frozen in this golden: Kùzu's traverse reported the
				// stored edge ghost -RELATED_TO-> unicode as "ghost -> note1 WROTE".
				// The store holds the right edge (see the export), SQLite reports
				// it, and the golden is deliberately not matched on that edge.
				wantEdges = []gleann.MemoryGraphEdge{{From: "ghost", To: "unicode", RelationType: "RELATED_TO",
					Attributes: map[string]any{"created_at": "<TS>", "updated_at": "<TS>"}}}
			}
			assertSameSet(t, "nodes", nodeSet(nodes), nodeSet(wantNodes))
			assertSameSet(t, "edges", edgeSet(edges), edgeSet(wantEdges))
		})
	}

	// The archive keeps the earlier versions with their original timestamps.
	var content, validTo string
	err = s.reader.QueryRow(`SELECT content, valid_to FROM entity_archive WHERE ns = 'mem' AND id = 'decision'`).Scan(&content, &validTo)
	if err != nil {
		t.Fatal(err)
	}
	if content != "remove kuzu in favour of sqlite" || validTo == "" {
		t.Fatalf("archive decision: %q %q", content, validTo)
	}
}

func TestImportGraph_ExistingRowsWinAndChangedFileReimports(t *testing.T) {
	s := newImportStore(t)
	svc := s.EntityService("ns", nil)
	if err := svc.InjectEntities(context.Background(), gleann.GraphInjectionPayload{
		Nodes: []gleann.MemoryGraphNode{{ID: "a", Type: "t", Content: "mine"}}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, entities string) string {
		p := filepath.Join(dir, name)
		doc := `{"format":"gleann-kuzu-memory-export","version":1,"namespace":"ns","entities":` + entities + `,"relations":[],"archive":[]}`
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	r, err := s.ImportGraphFile(write("1.json", `[{"id":"a","type":"t","content":"old","attributes":""},{"id":"b","type":"t","content":"b","attributes":"{}"}]`))
	if err != nil || r.Entities != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	var c string
	_ = s.reader.QueryRow(`SELECT content FROM entities WHERE ns='ns' AND id='a'`).Scan(&c)
	if c != "mine" {
		t.Fatalf("existing entity overwritten: %q", c)
	}
	// Another file for the same namespace is imported (hash differs), adding only new rows.
	r, err = s.ImportGraphFile(write("2.json", `[{"id":"b","type":"t","content":"b2","attributes":"{}"},{"id":"c","type":"t","content":"c","attributes":"{}"}]`))
	if err != nil || r.AlreadyImported || r.Entities != 1 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestImportGraph_Validation(t *testing.T) {
	s := newImportStore(t)
	dir := t.TempDir()
	bad := map[string]string{
		`not json`:                       "invalid JSON",
		`{"format":"other","version":1}`: `format "other"`,
		`{"format":"gleann-kuzu-memory-export","version":2,"namespace":"x"}`:                                            "unsupported export version 2",
		`{"format":"gleann-kuzu-memory-export","version":1,"namespace":"../x"}`:                                         "namespace",
		`{"format":"gleann-kuzu-memory-export","version":1,"namespace":"x","entities":[{"id":""}]}`:                     "no id",
		`{"format":"gleann-kuzu-memory-export","version":1,"namespace":"x","entities":[{"id":"a","attributes":"[1]"}]}`: "attributes",
	}
	i := 0
	for doc, want := range bad {
		i++
		p := filepath.Join(dir, fmt.Sprintf("%d.json", i))
		_ = os.WriteFile(p, []byte(doc), 0o644)
		_, err := s.ImportGraphFile(p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want containing %q", doc, err, want)
		}
	}
	if _, err := s.ImportGraphPath(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing path must fail")
	}
	if _, err := s.ImportGraphPath(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no *.json") {
		t.Errorf("empty dir: %v", err)
	}
	if rowCount(t, s, "entities") != 0 {
		t.Fatal("a rejected file wrote rows")
	}
}

func TestImportGraph_WritesMarker(t *testing.T) {
	s := newImportStore(t)
	p := filepath.Join(t.TempDir(), "x.json")
	doc := `{"format":"gleann-kuzu-memory-export","version":1,"namespace":"x","entities":[{"id":"a","type":"t","content":"c","attributes":"{}"}],"relations":[],"archive":[]}`
	_ = os.WriteFile(p, []byte(doc), 0o644)
	if _, err := s.ImportGraphFile(p); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.GraphImportMarker("x"); m == "" {
		t.Fatal("marker missing")
	}
}

func TestLegacyKuzuDetection(t *testing.T) {
	s := newImportStore(t)
	idx := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(idx, "one_memory"), []byte("kuzu"), 0o644))            // go-kuzu 0.11 file
	must(os.WriteFile(filepath.Join(idx, "one_memory.wal"), []byte("w"), 0o644))           // sibling, ignored
	must(os.MkdirAll(filepath.Join(idx, "two_memory"), 0o755))                             // older directory layout
	must(os.MkdirAll(filepath.Join(idx, "three_memory_vectors"), 0o755))                   // derived data, ignored
	must(os.MkdirAll(filepath.Join(idx, "idx_memory"), 0o755))                             // an index named idx_memory
	must(os.WriteFile(filepath.Join(idx, "idx_memory", "meta.json"), []byte("{}"), 0o644)) // ... has meta.json
	must(os.WriteFile(filepath.Join(idx, "empty_memory"), nil, 0o644))                     // empty file, ignored

	got := s.LegacyKuzuStores(idx)
	names := []string{}
	for _, l := range got {
		names = append(names, l.Name)
	}
	if strings.Join(names, ",") != "one,two" {
		t.Fatalf("legacy stores = %v", names)
	}
	exp, imp := LegacyImportCommands(got[0])
	if !strings.Contains(exp, "kuzu-memory-export") || !strings.Contains(exp, "one_memory") ||
		!strings.Contains(imp, "gleann memory import-graph") || !strings.Contains(imp, "one.kuzu-export.json") {
		t.Fatalf("commands: %q / %q", exp, imp)
	}

	// An import marker removes the store from the list.
	p := filepath.Join(t.TempDir(), "one.json")
	must(os.WriteFile(p, []byte(`{"format":"gleann-kuzu-memory-export","version":1,"namespace":"one","entities":[],"relations":[],"archive":[]}`), 0o644))
	if _, err := s.ImportGraphFile(p); err != nil {
		t.Fatal(err)
	}
	if got := s.LegacyKuzuStores(idx); len(got) != 1 || got[0].Name != "two" {
		t.Fatalf("after import: %v", got)
	}
}

func TestWarnLegacyKuzuOnce(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	s := newImportStore(t)
	idx := t.TempDir()
	if err := os.WriteFile(filepath.Join(idx, "p_memory"), []byte("kuzu"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.WarnLegacyKuzuOnce(idx, "nothing") // no store: silent
	if buf.Len() != 0 {
		t.Fatalf("unexpected log: %s", buf.String())
	}
	s.WarnLegacyKuzuOnce(idx, "p")
	s.WarnLegacyKuzuOnce(idx, "p")
	out := buf.String()
	if strings.Count(out, "level=WARN") != 1 || !strings.Contains(out, "kuzu-memory-export") ||
		!strings.Contains(out, "gleann memory import-graph") || strings.Contains(out, "gleann index migrate") {
		t.Fatalf("log = %s", out)
	}
}
