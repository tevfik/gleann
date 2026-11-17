package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGraphExport(t *testing.T, dir, name, ns, entities string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	doc := `{"format":"gleann-kuzu-memory-export","version":1,"namespace":"` + ns + `","entities":` + entities + `,"relations":[],"archive":[]}`
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// ImportGraphPath imports a single file, or every *.json file of a directory
// in name order; a damaged file stops the run, keeping the imports done
// before it, and a re-run skips the files already imported.
func TestImportGraphPathDirectory(t *testing.T) {
	s := newImportStore(t)
	dir := t.TempDir()
	writeGraphExport(t, dir, "b.json", "beta", `[{"id":"b1","type":"t","content":"b1","attributes":"{}"}]`)
	writeGraphExport(t, dir, "a.json", "alpha", `[{"id":"a1","type":"t","content":"a1","attributes":"{}"},{"id":"a2","type":"t","content":"a2","attributes":"{}"}]`)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not an export"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := s.ImportGraphPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Namespace != "alpha" || got[0].Entities != 2 || got[1].Namespace != "beta" || got[1].Entities != 1 {
		t.Fatalf("results = %+v, want alpha (2) then beta (1)", got)
	}
	if n := rowCount(t, s, "entities"); n != 3 {
		t.Fatalf("%d entities, want 3", n)
	}

	again, err := s.ImportGraphPath(dir)
	if err != nil || len(again) != 2 || !again[0].AlreadyImported || !again[1].AlreadyImported {
		t.Fatalf("re-import = %+v, %v; want both already imported", again, err)
	}

	// A single file path imports that file only.
	single := writeGraphExport(t, t.TempDir(), "g.json", "gamma", `[{"id":"g1","type":"t","content":"g","attributes":"{}"}]`)
	one, err := s.ImportGraphPath(single)
	if err != nil || len(one) != 1 || one[0].Namespace != "gamma" {
		t.Fatalf("single file = %+v, %v", one, err)
	}

	// A damaged file stops the run after the files before it.
	bad := t.TempDir()
	writeGraphExport(t, bad, "1.json", "delta", `[{"id":"d1","type":"t","content":"d","attributes":"{}"}]`)
	if err := os.WriteFile(filepath.Join(bad, "2.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGraphExport(t, bad, "3.json", "epsilon", `[{"id":"e1","type":"t","content":"e","attributes":"{}"}]`)
	partial, err := s.ImportGraphPath(bad)
	if err == nil || !strings.Contains(err.Error(), "2.json") {
		t.Fatalf("err = %v, want the damaged file named", err)
	}
	if len(partial) != 1 || partial[0].Namespace != "delta" {
		t.Fatalf("partial results = %+v, want delta only", partial)
	}
	if m, _ := s.GraphImportMarker("epsilon"); m != "" {
		t.Fatal("a file after the damaged one was imported")
	}

	if _, err := s.ImportGraphPath(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no *.json") {
		t.Fatalf("empty directory: %v", err)
	}
}

// Manager.Export and Manager.Import round-trip the blocks through markdown
// files into another store.
func TestManagerExportImportRoundTrip(t *testing.T) {
	src := NewManager(openTestStore(t))
	if _, err := src.AddNote(TierLong, "conv", "use table tests", "testing"); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Remember("the index lock serialises builds"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	es, err := src.Export(dir)
	if err != nil || es.Blocks != 2 {
		t.Fatalf("Export = %+v, %v", es, err)
	}

	dst := NewManager(openTestStore(t))
	is, err := dst.Import(dir)
	if err != nil || is.BlocksImported != 2 {
		t.Fatalf("Import = %+v, %v", is, err)
	}
	found, err := dst.Search("index lock")
	if err != nil || len(found) != 1 || !strings.Contains(found[0].Content, "serialises builds") {
		t.Fatalf("imported block not searchable: %+v, %v", found, err)
	}
	// A second import changes nothing.
	is, err = dst.Import(dir)
	if err != nil || is.BlocksImported != 0 || is.BlocksUnchanged != 2 {
		t.Fatalf("re-import = %+v, %v", is, err)
	}
}

// DefaultManager opens the store under GLEANN_MEMORY_DIR.
func TestDefaultManagerUsesMemoryDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLEANN_MEMORY_DIR", dir)
	m, err := DefaultManager()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Remember("kept across opens"); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = DefaultManager()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if got, err := m.Search("kept across"); err != nil || len(got) != 1 {
		t.Fatalf("reopened store lost the block: %v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory.sqlite")); err != nil {
		t.Fatalf("the store is not under GLEANN_MEMORY_DIR: %v", err)
	}
}

func TestBudgetReportSummary(t *testing.T) {
	var nilReport *ContextBudgetReport
	if nilReport.Summary() != "" {
		t.Fatal("a nil report has no summary")
	}
	r := &ContextBudgetReport{InjectedCount: 2, UsedTokens: 120}
	if got := r.Summary(); got != "memory budget: injected 2 block(s), 120/unlimited tokens; dropped 0 block(s)" {
		t.Fatalf("Summary = %q", got)
	}
	r = &ContextBudgetReport{
		MaxTokens: 100, InjectedCount: 1, UsedTokens: 90, DroppedCount: 2,
		Dropped: []BlockBudget{{ID: "b1", Tier: TierLong, Label: "conv"}, {ID: "b2", Tier: TierShort}},
	}
	if got, want := r.Summary(), "memory budget: injected 1 block(s), 90/100 tokens; dropped 2 block(s) ([long]conv, [short]b2)"; got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}
}
