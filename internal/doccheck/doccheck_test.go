// Package doccheck holds a test that compares the documentation with the
// source: REST routes against docs/api.md, GLEANN_* environment variables
// against docs/env-vars.md, MCP tools against docs/mcp.md and CLI commands
// against the docs and README. It parses source files; nothing is run.
package doccheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// repoRoot finds the directory with go.mod, starting from the test's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// goFiles lists the non-test .go files under root, skipping the directories
// in skip (relative to root, slash separated) and hidden directories.
func goFiles(t *testing.T, root string, skip ...string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			for _, s := range skip {
				if rel == s {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func parseFile(t *testing.T, path string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return f
}

// stringArg returns the unquoted value of a string literal expression.
func stringArg(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// callName returns "pkg.Func" or "Func" for a call expression.
func callName(c *ast.CallExpr) string {
	switch fn := c.Fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := fn.X.(*ast.Ident); ok {
			return id.Name + "." + fn.Sel.Name
		}
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// diff reports the two directions of a comparison.
func diff(t *testing.T, what string, inCode, inDocs map[string]bool, docFile string) {
	t.Helper()
	var undocumented, stale []string
	for k := range inCode {
		if !inDocs[k] {
			undocumented = append(undocumented, k)
		}
	}
	for k := range inDocs {
		if !inCode[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(undocumented)
	sort.Strings(stale)
	if len(undocumented) > 0 {
		t.Errorf("%s in code but undocumented in %s:\n  %s", what, docFile, strings.Join(undocumented, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("%s documented in %s but not in code:\n  %s", what, docFile, strings.Join(stale, "\n  "))
	}
}

// ---------------------------------------------------------------- routes

var placeholder = regexp.MustCompile(`\{[^}]*\}`)

// normRoute turns "GET /api/x/{name}?q=" into "GET /api/x/{}".
func normRoute(method, path string) string {
	if i := strings.IndexAny(path, "?"); i >= 0 {
		path = path[:i]
	}
	path = placeholder.ReplaceAllString(path, "{}")
	return strings.ToUpper(method) + " " + path
}

// routesInCode collects the patterns given to HandleFunc/Handle on a mux in
// the packages that serve HTTP. Patterns without a method ("/") are catch-alls
// and are not API routes.
func routesInCode(t *testing.T, root string) map[string]bool {
	t.Helper()
	routes := map[string]bool{}
	for _, dir := range []string{"internal/server", "internal/a2a", "internal/packs/packshttp"} {
		for _, file := range goFiles(t, filepath.Join(root, dir)) {
			ast.Inspect(parseFile(t, file), func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok || len(c.Args) == 0 {
					return true
				}
				sel, ok := c.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
					return true
				}
				pat, ok := stringArg(c.Args[0])
				if !ok {
					return true
				}
				method, path, found := strings.Cut(pat, " ")
				if !found {
					return true
				}
				routes[normRoute(method, path)] = true
				return true
			})
		}
	}
	return routes
}

// internalRoutes are registered but are not part of the documented REST API.
var internalRoutes = map[string]string{
	"GET /assets/": "static files of the embedded web UI",
}

var docRouteRow = regexp.MustCompile("(?m)^\\|\\s*([A-Z]+)\\s*\\|\\s*`([^`]+)`")

func TestRoutesMatchAPIDoc(t *testing.T) {
	root := repoRoot(t)
	code := routesInCode(t, root)
	for r := range internalRoutes {
		delete(code, r)
	}
	if len(code) < 20 {
		t.Fatalf("found only %d routes in code; the scanner is broken", len(code))
	}
	docs := map[string]bool{}
	for _, m := range docRouteRow.FindAllStringSubmatch(readFile(t, filepath.Join(root, "docs/api.md")), -1) {
		docs[normRoute(m[1], m[2])] = true
	}
	diff(t, "routes", code, docs, "docs/api.md")
}

// ------------------------------------------------------------ environment

// envNotDocumented lists GLEANN_* variables that are read on purpose without
// an entry in docs/env-vars.md. Keep it short; give each a reason.
var envNotDocumented = map[string]string{
	"GLEANN_TEST_MODE": "test hook of the chat TUI (skips the model fetch and the memory manager); not for users",
}

// envVarsInCode collects the GLEANN_* names passed to os.Getenv/os.LookupEnv
// in non-test Go files outside ext/, ui/ and tests/.
func envVarsInCode(t *testing.T, root string) map[string]bool {
	t.Helper()
	vars := map[string]bool{}
	for _, file := range goFiles(t, root, "ext", "ui", "tests") {
		ast.Inspect(parseFile(t, file), func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || len(c.Args) == 0 {
				return true
			}
			if name := callName(c); name != "os.Getenv" && name != "os.LookupEnv" {
				return true
			}
			if s, ok := stringArg(c.Args[0]); ok && strings.HasPrefix(s, "GLEANN_") {
				vars[s] = true
			}
			return true
		})
	}
	return vars
}

var gleannVar = regexp.MustCompile(`GLEANN_[A-Z0-9_]*[A-Z0-9]`)

func TestEnvVarsMatchEnvDoc(t *testing.T) {
	root := repoRoot(t)
	code := envVarsInCode(t, root)
	if len(code) < 15 {
		t.Fatalf("found only %d GLEANN_* variables in code; the scanner is broken", len(code))
	}
	for v := range envNotDocumented {
		delete(code, v)
	}
	docs := map[string]bool{}
	for _, v := range gleannVar.FindAllString(readFile(t, filepath.Join(root, "docs/env-vars.md")), -1) {
		docs[v] = true
	}
	// A documented name need not be read through os.Getenv (config loaders and
	// helpers read some); it only has to appear as a string literal in code.
	literals := map[string]bool{}
	for _, file := range goFiles(t, root, "ui") {
		for _, v := range gleannVar.FindAllString(readFile(t, file), -1) {
			literals[v] = true
		}
	}
	for v := range docs {
		if literals[v] {
			code[v] = true
		}
	}
	diff(t, "GLEANN_* variables", code, docs, "docs/env-vars.md")
}

// ------------------------------------------------------------------- MCP

// mcpToolsInCode collects tool names from mcp.Tool{Name: "..."} literals and
// mcp.NewTool("...") calls in internal/mcp.
func mcpToolsInCode(t *testing.T, root string) map[string]bool {
	t.Helper()
	tools := map[string]bool{}
	for _, file := range goFiles(t, filepath.Join(root, "internal/mcp")) {
		ast.Inspect(parseFile(t, file), func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CompositeLit:
				sel, ok := x.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Tool" {
					return true
				}
				for _, el := range x.Elts {
					kv, ok := el.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Name" {
						if s, ok := stringArg(kv.Value); ok {
							tools[s] = true
						}
					}
				}
			case *ast.CallExpr:
				if strings.HasSuffix(callName(x), ".NewTool") && len(x.Args) > 0 {
					if s, ok := stringArg(x.Args[0]); ok {
						tools[s] = true
					}
				}
			}
			return true
		})
	}
	return tools
}

var mcpToolName = regexp.MustCompile("`((?:gleann|memory)_[a-z0-9_]*[a-z0-9])`")

func TestMCPToolsMatchMCPDoc(t *testing.T) {
	root := repoRoot(t)
	code := mcpToolsInCode(t, root)
	if len(code) < 20 {
		t.Fatalf("found only %d MCP tools in code; the scanner is broken", len(code))
	}
	text := readFile(t, filepath.Join(root, "docs/mcp.md"))
	docs := map[string]bool{}
	for _, m := range mcpToolName.FindAllStringSubmatch(text, -1) {
		docs[m[1]] = true
	}
	// Tools without a gleann_/memory_ prefix (inject_knowledge_graph, ...) can
	// only be checked in the code direction: mention anywhere in the file.
	codeNamed := map[string]bool{}
	var other []string
	for k := range code {
		if strings.HasPrefix(k, "gleann_") || strings.HasPrefix(k, "memory_") {
			codeNamed[k] = true
		} else if !strings.Contains(text, k) {
			other = append(other, k)
		}
	}
	diff(t, "MCP tools", codeNamed, docs, "docs/mcp.md")
	sort.Strings(other)
	if len(other) > 0 {
		t.Errorf("MCP tools in code but undocumented in docs/mcp.md:\n  %s", strings.Join(other, "\n  "))
	}
}

// ------------------------------------------------------------------- CLI

// cliNotDocumented lists top-level commands that need no documentation.
var cliNotDocumented = map[string]string{
	"help":       "prints the usage text",
	"version":    "prints the version",
	"quickstart": "deprecated: only prints a pointer to `gleann setup --auto` and exits 1 (alias: go)",
}

// cliCommandsInCode returns, for each case clause of the command switch in
// cmd/gleann/main.go, the first name of the clause.
func cliCommandsInCode(t *testing.T, root string) map[string]bool {
	t.Helper()
	cmds := map[string]bool{}
	f := parseFile(t, filepath.Join(root, "cmd/gleann/main.go"))
	ast.Inspect(f, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		id, ok := sw.Tag.(*ast.Ident)
		if !ok || id.Name != "cmd" {
			return true
		}
		for _, st := range sw.Body.List {
			cc := st.(*ast.CaseClause)
			if len(cc.List) == 0 {
				continue
			}
			if s, ok := stringArg(cc.List[0]); ok && !strings.HasPrefix(s, "-") {
				cmds[s] = true
			}
		}
		return false
	})
	return cmds
}

func TestCLICommandsAreDocumented(t *testing.T) {
	root := repoRoot(t)
	cmds := cliCommandsInCode(t, root)
	if len(cmds) < 15 {
		t.Fatalf("found only %d commands in code; the scanner is broken", len(cmds))
	}
	var text strings.Builder
	text.WriteString(readFile(t, filepath.Join(root, "README.md")))
	docFiles, _ := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	for _, f := range docFiles {
		text.WriteString(readFile(t, f))
	}
	all := text.String()
	var missing []string
	for c := range cmds {
		if _, skip := cliNotDocumented[c]; skip {
			continue
		}
		if !regexp.MustCompile(`gleann ` + regexp.QuoteMeta(c) + `\b`).MatchString(all) {
			missing = append(missing, c)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("commands in code but undocumented (no `gleann <command>` in README.md or docs/):\n  %s", strings.Join(missing, "\n  "))
	}
}
