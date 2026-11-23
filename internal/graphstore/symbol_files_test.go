package graphstore

import (
	"reflect"
	"testing"
)

func TestSymbolFiles(t *testing.T) {
	g, _ := openTestGraph(t)
	if got, err := g.SymbolFiles(); err != nil || len(got) != 0 {
		t.Fatalf("SymbolFiles of an empty graph = %v, %v; want none, nil", got, err)
	}
	for i, f := range []string{"p/b.go", "root.go", "p/a.go", "README.md"} {
		mustApply(t, g, FileDelta{File: f, Symbols: []Symbol{mkSym("m."+f, f, int64(i+1))}})
	}
	// Two symbols in one file list it once.
	mustApply(t, g, FileDelta{File: "p/a.go", Symbols: []Symbol{mkSym("m.p/a.go", "p/a.go", 1), mkSym("m.other", "p/a.go", 2)}})
	got, err := g.SymbolFiles()
	if err != nil {
		t.Fatalf("SymbolFiles: %v", err)
	}
	want := []string{"README.md", "p/a.go", "p/b.go", "root.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SymbolFiles = %v, want %v", got, want)
	}
}
