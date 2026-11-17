package graphstore

import (
	"reflect"
	"testing"
)

func TestFilesInDir(t *testing.T) {
	g, _ := openTestGraph(t)
	for i, f := range []string{"root.go", "p/a.go", "p/b.go", "p/sub/c.go", "pp/d.go", "p_x/e.go"} {
		mustApply(t, g, FileDelta{File: f, Symbols: []Symbol{mkSym("m."+f, f, int64(i+1))}})
	}
	for dir, want := range map[string][]string{
		"p":       {"p/a.go", "p/b.go"}, // not p/sub, not pp or p_x (prefix lookalikes)
		"p/sub":   {"p/sub/c.go"},
		".":       {"root.go"},
		"":        {"root.go"},
		"p/":      {"p/a.go", "p/b.go"},
		"nowhere": nil,
	} {
		got, err := g.FilesInDir(dir)
		if err != nil {
			t.Fatalf("FilesInDir(%q): %v", dir, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FilesInDir(%q) = %v, want %v", dir, got, want)
		}
	}
}
