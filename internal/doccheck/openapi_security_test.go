package doccheck

// The OpenAPI spec declares bearerAuth once at the top level; a public route
// overrides it with an empty security list. docs/api.md states which routes
// those are. This test keeps the sentence true: the routes it lists must be
// exactly the operations the spec builders mark public (cross-review
// v1202-release.qwen.md finding F3 — the docs claimed four overrides while the
// spec only had two).

import (
	"go/ast"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// collectPublicSpecRoutes reads one paths map literal and records the
// operations that carry an empty `security` list. Like collectSpecRoutes, one
// level below a path key is enough (OpenAPI 3.0 paths do not nest).
func collectPublicSpecRoutes(lit *ast.CompositeLit, out map[string]bool) {
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		k, ok := stringArg(kv.Key)
		if !ok {
			continue
		}
		cl, ok := kv.Value.(*ast.CompositeLit)
		if !ok {
			continue
		}
		if !strings.HasPrefix(k, "/") {
			collectPublicSpecRoutes(cl, out)
			continue
		}
		for _, op := range cl.Elts {
			okv, ok := op.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			method, ok := stringArg(okv.Key)
			if !ok || !isHTTPMethod(method) {
				continue
			}
			ocl, ok := okv.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, f := range ocl.Elts {
				fkv, ok := f.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				fk, ok := stringArg(fkv.Key)
				if !ok || fk != "security" {
					continue
				}
				if fcl, ok := fkv.Value.(*ast.CompositeLit); ok && len(fcl.Elts) == 0 {
					out[normRoute(method, k)] = true
				}
			}
		}
	}
}

// specPublicRoutes collects the operations the OpenAPI builders mark public
// with an empty security list (the override of the global bearerAuth).
func specPublicRoutes(t *testing.T, root string) map[string]bool {
	t.Helper()
	routes := map[string]bool{}
	for _, file := range goFiles(t, filepath.Join(root, "internal/server")) {
		if !strings.Contains(filepath.Base(file), "openapi") {
			continue
		}
		ast.Inspect(parseFile(t, file), func(n ast.Node) bool {
			if lit, ok := n.(*ast.CompositeLit); ok {
				collectPublicSpecRoutes(lit, routes)
			}
			return true
		})
	}
	return routes
}

// publicRoutesClaim matches the docs/api.md sentence that lists the public
// routes, e.g. "the four public routes (`GET /health`, ...)".
var publicRoutesClaim = regexp.MustCompile(`the ([a-z]+) public routes\s*\(([^)]*)\)`)

var publicRouteItem = regexp.MustCompile("`([A-Z]+) (/[^`]+)`")

// TestPublicRoutesDocsMatchSpec keeps the docs/api.md public-routes claim in
// step with the spec: every operation with an empty security list is listed in
// the sentence, every listed route really carries the override, and the count
// word matches the number of routes.
func TestPublicRoutesDocsMatchSpec(t *testing.T) {
	root := repoRoot(t)
	spec := specPublicRoutes(t, root)
	if len(spec) == 0 {
		t.Fatal("no empty security override found in the OpenAPI builders; the scanner is broken")
	}
	text := readFile(t, filepath.Join(root, "docs/api.md"))
	m := publicRoutesClaim.FindStringSubmatch(text)
	if m == nil {
		t.Fatal("docs/api.md no longer states which public routes override the global security scheme")
	}
	docs := map[string]bool{}
	for _, it := range publicRouteItem.FindAllStringSubmatch(m[2], -1) {
		docs[normRoute(it[1], it[2])] = true
	}
	diff(t, "public routes", spec, docs, "the docs/api.md public-routes claim")

	words := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five", 6: "six", 7: "seven", 8: "eight"}
	if w, ok := words[len(spec)]; !ok || m[1] != w {
		t.Errorf("docs/api.md says %q public routes; the spec marks %d public", m[1], len(spec))
	}
}
