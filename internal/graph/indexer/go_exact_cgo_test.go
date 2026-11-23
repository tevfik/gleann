package indexer

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const cgoMixedSrc = `package cgopkg

/*
#include <stdlib.h>
*/
import "C"

// Wrap calls a C function.
func Wrap() int { return int(C.abs(-3)) }

// Plain is a plain Go function in the cgo file.
func Plain() int { return Wrap() }
`

const cgoPlainSrc = `package cgopkg

// Other calls Plain from the non-cgo file of the package.
func Other() int { return Plain() }
`

const cgoOnlySrc = `package purecgo

/*
#include <stdlib.h>
*/
import "C"

// Hello calls a C function.
func Hello() int { return int(C.abs(-1)) }

// Call calls Hello.
func Call() int { return Hello() }
`

// go/packages reports the cgo-generated files (in GOCACHE) as the package's
// syntax files: the resolver must map them back to the original repo sources
// (GoFiles/CompiledGoFiles) and take the package prefix from the package, not
// from a cache path — otherwise HasFile is false for cgo files (they fall
// back to the syntactic resolver) and a cgo-only package's FQN prefix is
// corrupted (review G2).
func TestExactCgoFilesKeepTheirRepoPaths(t *testing.T) {
	needGo(t)
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("no C compiler for cgo")
	}
	const module = "example.com/cgotest"
	root := writeTree(t, map[string]string{
		"go.mod":          "module " + module + "\n\ngo 1.21\n",
		"cgopkg/c.go":     cgoMixedSrc,
		"cgopkg/plain.go": cgoPlainSrc,
		"purecgo/p.go":    cgoOnlySrc,
	})

	check := func(t *testing.T, r *ExactResolver) {
		t.Helper()
		if r == nil {
			t.Fatal("no resolver")
		}
		for _, rel := range []string{"cgopkg/c.go", "cgopkg/plain.go", "purecgo/p.go"} {
			if !r.HasFile(rel) {
				t.Errorf("HasFile(%s) = false, want true", rel)
			}
		}
		for rel := range r.files {
			if strings.HasPrefix(rel, "..") || strings.Contains(rel, "go-build") {
				t.Errorf("resolver holds a file outside the repo: %q", rel)
			}
		}
		// The Go call of the cgo file resolves exactly, under the package's
		// real FQN prefix (a cache-path prefix corrupts it).
		fg := ExtractFileExact(module, root, filepath.Join(root, "cgopkg", "c.go"), cgoMixedSrc, r)
		if !hasExactCall(fg, module+"/cgopkg.Plain", module+"/cgopkg.Wrap") {
			t.Errorf("cgopkg/c.go: no exact CALLS Plain -> Wrap under the real prefix; calls: %+v", fg.Calls)
		}
		fg = ExtractFileExact(module, root, filepath.Join(root, "purecgo", "p.go"), cgoOnlySrc, r)
		if !hasExactCall(fg, module+"/purecgo.Call", module+"/purecgo.Hello") {
			t.Errorf("purecgo/p.go: no exact CALLS Call -> Hello under the real prefix; calls: %+v", fg.Calls)
		}
	}

	t.Run("LoadExactDirs", func(t *testing.T) {
		r, err := LoadExactDirs(module, root, []string{"cgopkg", "purecgo"})
		if err != nil {
			t.Fatalf("LoadExactDirs: %v", err)
		}
		check(t, r)
	})
	t.Run("LoadExact", func(t *testing.T) {
		r, err := LoadExact(module, root)
		if err != nil {
			t.Fatalf("LoadExact: %v", err)
		}
		check(t, r)
	})
}

func hasExactCall(fg FileGraph, caller, callee string) bool {
	for _, e := range fg.Calls {
		if e.CallerFQN == caller && e.CalleeFQN == callee && e.Confidence == "exact" {
			return true
		}
	}
	return false
}
