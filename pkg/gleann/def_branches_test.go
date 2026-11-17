package gleann

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSignatureFromMissingFile(t *testing.T) {
	sig, doc := ExtractSignatureFromFile(filepath.Join(t.TempDir(), "absent.go"), 3)
	if sig != "" || doc != "" {
		t.Errorf("got %q %q for a file that does not exist", sig, doc)
	}
}

func TestFindDefinitionsGraphScoring(t *testing.T) {
	root := t.TempDir()
	src := "package p\n\n// Handle serves.\nfunc Handle() {}\n"
	if err := os.WriteFile(filepath.Join(root, "h.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &LeannSearcher{graphDB: &mockGraphDBForDef{symbols: []Callee{
		{FQN: "p.Handle", Name: "Handle", Kind: "function", File: "h.go", Line: 4},              // exact name
		{FQN: "Handle", Name: "HandleX", Kind: "function", File: "h.go", Line: 4, IsTest: true}, // FQN == query, but a test
		{FQN: "pkg.sub.handle", Name: "other", Kind: "function"},                                // FQN suffix
		{FQN: "p.HandleAll", Name: "HandleAll", Kind: "method"},                                 // substring only
	}}}

	got, err := FindDefinitions(context.Background(), s, "Handle", DefinitionOptions{RootDir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d definitions: %+v", len(got), got)
	}
	// Results come back best first: exact name, FQN suffix, substring.
	if got[0].Name != "Handle" || got[0].Score != 1.0 {
		t.Errorf("best = %+v", got[0])
	}
	if !strings.Contains(got[0].Signature, "func Handle()") || got[0].Doc == "" {
		t.Errorf("signature not resolved against RootDir: sig=%q doc=%q", got[0].Signature, got[0].Doc)
	}
	scores := map[string]float32{}
	for _, d := range got {
		scores[d.Name] = d.Score
	}
	if scores["other"] != 0.85 || scores["HandleAll"] != 0.70 {
		t.Errorf("scores = %v", scores)
	}
	// FQN equality is 0.95, and a test symbol is demoted by a fifth.
	if want := float32(0.95) * 0.8; scores["HandleX"] != want {
		t.Errorf("test symbol score = %v, want %v", scores["HandleX"], want)
	}

	// Kind filter and TopK.
	if got, _ = FindDefinitions(context.Background(), s, "Handle", DefinitionOptions{Kind: "METHOD"}); len(got) != 1 || got[0].Name != "HandleAll" {
		t.Errorf("kind filter: %+v", got)
	}
	if got, _ = FindDefinitions(context.Background(), s, "Handle", DefinitionOptions{TopK: 2, RootDir: root}); len(got) != 2 {
		t.Errorf("TopK: got %d results", len(got))
	}
	// A blank query is refused.
	if _, err := FindDefinitions(context.Background(), s, "  ", DefinitionOptions{}); err == nil {
		t.Error("blank query must fail")
	}
}

func TestFindDefinitionsPassageFallback(t *testing.T) {
	dir := t.TempDir()
	buildFormat2Lexical(t, dir, []Item{
		{Text: "intro line\nfunc Resolve() {}\n", Metadata: map[string]any{"source": "r.go", "start_line": 10}},
		// An overlapping chunk of the same file holding the same declaration.
		{Text: "intro line\nfunc Resolve() {}\ntrailing", Metadata: map[string]any{"source": "r.go", "start_line": 10}},
		{Text: "type Resolve struct{}", Metadata: map[string]any{"source": "t.go"}},
		{Text: "nothing relevant here, Resolve is only mentioned", Metadata: map[string]any{"source": "n.md"}},
	})
	s := NewSearcher(Config{IndexDir: dir, Backend: "hnsw"}, nil)
	if err := s.Load(context.Background(), "idx"); err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := FindDefinitions(context.Background(), s, "Resolve", DefinitionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var fn, st *DefinitionResult
	for i := range got {
		switch got[i].Kind {
		case "function":
			fn = &got[i]
		case "struct":
			st = &got[i]
		}
	}
	if len(got) != 2 || fn == nil || st == nil {
		t.Fatalf("definitions = %+v, want the function once and the struct", got)
	}
	// The passage-relative line 2 shifts by the chunk start (10).
	if fn.File != "r.go" || fn.Line != 11 {
		t.Errorf("function def = %+v, want r.go:11", fn)
	}

	// Kind filter drops the function.
	got, _ = FindDefinitions(context.Background(), s, "Resolve", DefinitionOptions{Kind: "struct"})
	if len(got) != 1 || got[0].Kind != "struct" {
		t.Errorf("kind filter: %+v", got)
	}

	// A search failure is not an error: the answer is simply empty.
	got, err = FindDefinitions(context.Background(), NewSearcher(DefaultConfig(), nil), "Resolve", DefinitionOptions{})
	if err != nil || len(got) != 0 {
		t.Errorf("unloaded searcher: %+v, %v", got, err)
	}
}

func TestFormatDefinitionsShapes(t *testing.T) {
	if got := FormatDefinitions(nil); got != "No symbol definitions found." {
		t.Errorf("empty = %q", got)
	}
	out := FormatDefinitions([]DefinitionResult{
		{FQN: "a.A", Kind: "function", File: "a.go", Line: 7, Signature: "func A(\n\tx int)", Doc: "A does a."},
		{FQN: "b.B", Kind: "type", File: "b.go"},
		{FQN: "c.C", Kind: "class"},
	})
	for _, want := range []string{
		"[1] Symbol: a.A (function)", "Defined at: a.go:7", "Signature:\n  func A(\n  \tx int)", "Doc:\n  A does a.",
		"[2] Symbol: b.B (type)", "Defined at: b.go\n", "[3] Symbol: c.C (class)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Defined at: c.go") || strings.Count(out, "Defined at:") != 2 {
		t.Errorf("a definition without a file must not print a location:\n%s", out)
	}
}
