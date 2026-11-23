package indexer

import (
	"fmt"
	"go/build/constraint"
	"os"
	"strings"
	"testing"
)

// TestNoWindowsExcludedTestFiles guards this package's test files against a
// build constraint that names the windows GOOS.
//
// While the code graph lived in Kùzu (a CGo/C++ dependency CI never built on
// Windows), three OS-independent test files were marked `//go:build !windows`.
// Kùzu is gone and the package is pure Go, so every test here must build and
// run on every supported platform. Any constraint that mentions windows,
// whether it includes it (`windows`) or excludes it (`!windows`), re-excludes
// one platform and is rejected.
func TestNoWindowsExcludedTestFiles(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	var testFiles, constrained int
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		testFiles++

		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		expr, err := fileBuildConstraint(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if expr == nil {
			continue
		}
		constrained++
		if mentionsBuildTag(expr, "windows") {
			t.Errorf("%s: build constraint %q names windows; indexer tests must not be OS-excluded", name, expr.String())
		}
	}

	if testFiles == 0 {
		t.Fatal("found no test files; the guard is not looking at the package sources")
	}
	t.Logf("checked %d test files, %d with a build constraint", testFiles, constrained)
}

// fileBuildConstraint parses the build constraints at the top of a Go source
// file (the //go:build line and/or legacy // +build lines). It returns nil
// when the file has no constraint.
func fileBuildConstraint(src []byte) (constraint.Expr, error) {
	var expr constraint.Expr
	for _, raw := range strings.Split(string(src), "\n") {
		// Constraints must start in column one; trimming the trailing CR keeps
		// a Windows checkout with autocrlf on parseable.
		line := strings.TrimRight(raw, " \t\r")
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "package" {
			// A build constraint may be preceded by other line comments but
			// must appear before the package clause.
			break
		}
		if !constraint.IsGoBuild(line) && !constraint.IsPlusBuild(line) {
			continue
		}
		parsed, err := constraint.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("parsing build constraint %q: %w", line, err)
		}
		if expr == nil {
			expr = parsed
			continue
		}
		expr = &constraint.AndExpr{X: expr, Y: parsed}
	}
	return expr, nil
}

// mentionsBuildTag reports whether expr references tag anywhere, positively
// or negatively.
func mentionsBuildTag(expr constraint.Expr, tag string) bool {
	switch e := expr.(type) {
	case *constraint.TagExpr:
		return e.Tag == tag
	case *constraint.NotExpr:
		return mentionsBuildTag(e.X, tag)
	case *constraint.AndExpr:
		return mentionsBuildTag(e.X, tag) || mentionsBuildTag(e.Y, tag)
	case *constraint.OrExpr:
		return mentionsBuildTag(e.X, tag) || mentionsBuildTag(e.Y, tag)
	default:
		return false
	}
}

// TestFileBuildConstraint checks the header parser used by the guard: the
// //go:build form, the legacy // +build form, a constraint after another line
// comment, and a file without a constraint.
func TestFileBuildConstraint(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		hasExpr   bool
		wantWinds bool
	}{
		{
			name:      "go:build excludes windows",
			src:       "//go:build !windows\r\n\r\npackage x\r\n",
			hasExpr:   true,
			wantWinds: true,
		},
		{
			name:      "other tag only",
			src:       "//go:build !treesitter\n\npackage x\n",
			hasExpr:   true,
			wantWinds: false,
		},
		{
			name:      "after a copyright comment",
			src:       "// Copyright 2026\n\n//go:build windows\n\npackage x\n",
			hasExpr:   true,
			wantWinds: true,
		},
		{
			name:      "legacy plus build",
			src:       "// +build !windows\n\npackage x\n",
			hasExpr:   true,
			wantWinds: true,
		},
		{
			name:    "no constraint",
			src:     "package x\n\nvar s = \"//go:build !windows\"\n",
			hasExpr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := fileBuildConstraint([]byte(tc.src))
			if err != nil {
				t.Fatalf("fileBuildConstraint: %v", err)
			}
			if (expr != nil) != tc.hasExpr {
				t.Fatalf("got expr=%v, want an expression: %v", expr, tc.hasExpr)
			}
			if got := mentionsBuildTag(expr, "windows"); got != tc.wantWinds {
				t.Errorf("mentionsBuildTag(windows) = %v, want %v", got, tc.wantWinds)
			}
		})
	}
}
