package indexer

import "testing"

func TestBuildPkgPrefixOfMatchesBuildFQNOf(t *testing.T) {
	for _, tc := range []struct{ module, rel, want string }{
		{"m", "main.go", "m"},
		{"m", "pkg/a.go", "m/pkg"},
		{"m", `pkg\sub\a.go`, "m/pkg/sub"},
		{"", "pkg/a.go", "pkg"},
		{"", "main.go", ""},
	} {
		if got := buildPkgPrefixOf(tc.module, tc.rel); got != tc.want {
			t.Errorf("buildPkgPrefixOf(%q, %q) = %q, want %q", tc.module, tc.rel, got, tc.want)
		}
	}
}

// A type in a root-level file that implements an interface: the IMPLEMENTS
// source must be the declared FQN (before, the file name leaked into the
// prefix and ApplyFileDelta refused the whole file).
func TestExtractFileRootImplementsSourceIsDeclared(t *testing.T) {
	src := "package fx\n\ntype I interface{ M() }\n\n// T embeds I.\ntype T struct {\n\tI\n}\n"
	fg := ExtractFile("example.com/fx", "/r", "/r/main.go", src)
	declared := map[string]bool{}
	for _, s := range fg.Symbols {
		if s.File != "" {
			declared[s.FQN] = true
		}
	}
	if len(fg.Implements) == 0 {
		t.Fatal("no IMPLEMENTS edge extracted")
	}
	for _, e := range fg.Implements {
		if !declared[e.ImplFQN] {
			t.Errorf("IMPLEMENTS source %q is not a declared symbol (declared: %v)", e.ImplFQN, declared)
		}
	}
}
