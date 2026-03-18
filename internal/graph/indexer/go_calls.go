//go:build treesitter

package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/modules/chunking"
)

// collectGoCallQueries inspects Go source with go/ast and returns Cypher queries
// for CALLS edges (to be batched into a transaction by the caller).
func collectGoCallQueries(idx *Indexer, relPath, source string, chunks []chunking.CodeChunk) (nodes []kuzu.SymbolNode, edges []kuzu.EdgeCalls, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}

	// Build a quick name → FQN map for locally known symbols.
	localFQN := make(map[string]string)
	for _, ch := range chunks {
		if ch.Name != "" {
			localFQN[ch.Name] = idx.buildFQN(relPath, ch.Name)
		}
	}

	// Build a secondary map for chunker-split functions (e.g. "Agent.Run" → "Agent.Run_part1").
	// When the chunker splits a large function into parts, the Go AST still sees
	// the original declaration name. Map the original name to the _part1 FQN so
	// CALLS edges reference a Symbol that actually exists.
	splitFQN := make(map[string]string)
	for name, fqn := range localFQN {
		if strings.HasSuffix(name, "_part1") {
			origName := strings.TrimSuffix(name, "_part1")
			if _, exists := localFQN[origName]; !exists {
				splitFQN[origName] = fqn
			}
		}
	}

	pkgDir := filepath.Dir(relPath)
	pkgDir = strings.ReplaceAll(pkgDir, string(filepath.Separator), "/")
	pkgPrefix := idx.module
	if pkgDir != "." {
		pkgPrefix = idx.module + "/" + pkgDir
	}

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

		callerName := funcDecl.Name.Name
		if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
			recvType := exprTypeName(funcDecl.Recv.List[0].Type)
			callerName = recvType + "." + callerName
		}

		// Resolve caller FQN: prefer localFQN (exact match), then splitFQN
		// (chunker-split _part1), then fall back to pkgPrefix.
		var callerFQN string
		if fqn, ok := localFQN[callerName]; ok {
			callerFQN = fqn
		} else if fqn, ok := splitFQN[callerName]; ok {
			callerFQN = fqn
		} else {
			callerFQN = pkgPrefix + "." + callerName
		}

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

			nodes = append(nodes, kuzu_symbol(calleeFQN))
			edges = append(edges, kuzu.EdgeCalls{CallerFQN: callerFQN, CalleeFQN: calleeFQN})
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

// kuzu_symbol builds a minimal SymbolNode for an unknown callee.
func kuzu_symbol(fqn string) kuzu.SymbolNode {
	parts := strings.SplitN(fqn, ".", -1)
	name := parts[len(parts)-1]
	return kuzu.SymbolNode{FQN: fqn, Kind: "function", Name: name}
}
