package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	gtypes "github.com/tevfik/gleann/internal/graph/types"
	"github.com/tevfik/gleann/modules/chunking"
)

// collectGoCallQueries inspects Go source with go/ast and returns CALLS edges
// plus callee stub symbols (to be batched into a transaction by the caller).
func collectGoCallQueries(module, relPath, source string, chunks []chunking.CodeChunk) (nodes []gtypes.SymbolNode, edges []gtypes.EdgeCalls, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}

	localFQN, pkgPrefix := buildGoFQNMaps(module, relPath, chunks)

	importMap := make(map[string]string)
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		} else {
			parts := strings.Split(impPath, "/")
			alias = parts[len(parts)-1]
		}
		if alias != "_" && alias != "." {
			importMap[alias] = impPath
		}
	}

	seen := make(map[string]bool) // avoid duplicate edges in same file

	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Body == nil {
			continue
		}

		callerFQN := goCallerFQN(funcDecl, localFQN, pkgPrefix)

		ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			calleeFQN := resolveCallFQN(call.Fun, importMap, localFQN, pkgPrefix)
			if calleeFQN == "" || calleeFQN == callerFQN {
				return true
			}
			edgeKey := callerFQN + "→" + calleeFQN
			if seen[edgeKey] {
				return true
			}
			seen[edgeKey] = true

			nodes = append(nodes, calleeStub(calleeFQN))
			edges = append(edges, gtypes.EdgeCalls{CallerFQN: callerFQN, CalleeFQN: calleeFQN, Confidence: "extracted"})
			return true
		})
	}
	return nodes, edges, nil
}

// resolveCallFQN attempts to convert a call expression function node into a FQN.
// Returns "" for calls that cannot be resolved statically (receiver methods,
// chained field access, etc.).
func resolveCallFQN(fun ast.Expr, imports map[string]string, local map[string]string, pkgPrefix string) string {
	switch f := fun.(type) {
	case *ast.Ident:
		name := f.Name
		if fqn, ok := local[name]; ok {
			return fqn
		}
		// Builtins or same-package functions without a chunk entry.
		return pkgPrefix + "." + name

	case *ast.SelectorExpr:
		ident, ok := f.X.(*ast.Ident)
		if !ok {
			// Chained calls like a.field.Method() — can't resolve statically.
			return ""
		}
		pkgAlias := ident.Name
		funcName := f.Sel.Name

		// Known import: forge.NewClient() → github.com/tevfik/.../forge.NewClient
		if importPath, ok := imports[pkgAlias]; ok {
			return importPath + "." + funcName
		}

		// Local type method: Agent.Run() — check localFQN for "Agent.Run"
		combined := pkgAlias + "." + funcName
		if fqn, ok := local[combined]; ok {
			return fqn
		}

		// Unknown receiver variable (a.Run(), ctx.Done()) — skip.
		// Without type information we'd create bogus FQNs like pkg.a.Run.
		return ""

	case *ast.IndexExpr:
		return resolveCallFQN(f.X, imports, local, pkgPrefix)
	}
	return ""
}

// exprTypeName extracts the type name string from a receiver field type expression.
func exprTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return exprTypeName(t.X)
	case *ast.IndexExpr:
		return exprTypeName(t.X)
	}
	return ""
}

// calleeStub builds a minimal SymbolNode for an unknown callee.
func calleeStub(fqn string) gtypes.SymbolNode {
	parts := strings.SplitN(fqn, ".", -1)
	name := parts[len(parts)-1]
	return gtypes.SymbolNode{FQN: fqn, Kind: "function", Name: name}
}

// buildGoFQNMaps builds the name → FQN lookup map shared by the syntactic and
// the exact (go/types) call collectors so both attribute edges to the same
// caller symbols. localFQN maps (logical, already merged) chunk symbol names to
// their FQN; pkgPrefix is the package FQN prefix.
func buildGoFQNMaps(module, relPath string, chunks []chunking.CodeChunk) (localFQN map[string]string, pkgPrefix string) {
	localFQN = make(map[string]string)
	for _, ch := range chunks {
		if ch.Name != "" {
			localFQN[ch.Name] = buildFQNOf(module, relPath, ch.Name)
		}
	}
	pkgDir := filepath.Dir(relPath)
	pkgDir = strings.ReplaceAll(pkgDir, string(filepath.Separator), "/")
	pkgPrefix = module
	if pkgDir != "." {
		pkgPrefix = module + "/" + pkgDir
	}
	return localFQN, pkgPrefix
}

// goCallerFQN resolves the FQN of the function declaring a call site: the
// declared symbol when the file has one, else the package-qualified guess
// (an edge from an undeclared caller is dropped by the sink).
func goCallerFQN(funcDecl *ast.FuncDecl, localFQN map[string]string, pkgPrefix string) string {
	callerName := funcDecl.Name.Name
	if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
		recvType := exprTypeName(funcDecl.Recv.List[0].Type)
		callerName = recvType + "." + callerName
	}
	if fqn, ok := localFQN[callerName]; ok {
		return fqn
	}
	return pkgPrefix + "." + callerName
}
