package indexer

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/modules/chunking"
	"github.com/tevfik/gleann/internal/graph/kuzu"
)

// extractGoCallEdges inspects Go source code with go/ast to find function call
// relationships and writes CALLS edges into KuzuDB.
//
// Strategy:
// - Parse the file with full comment + declaration info.
// - Walk all FuncDecl nodes to know the "current function context".
// - Inside each FuncDecl, collect all CallExpr nodes.
// - Resolve the callee name and build a best-effort FQN.
// - Write a CALLS edge: callerFQN → calleeFQN.
func extractGoCallEdges(idx *Indexer, relPath, source string, chunks []chunking.CodeChunk) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, 0)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	// Build a quick name → FQN map for locally known symbols (from chunks).
	localFQN := make(map[string]string)
	for _, ch := range chunks {
		if ch.Name != "" {
			localFQN[ch.Name] = idx.buildFQN(relPath, ch.Name)
		}
	}

	// Determine the package prefix for this file.
	pkgDir := filepath.Dir(relPath)
	pkgDir = strings.ReplaceAll(pkgDir, string(filepath.Separator), "/")
	pkgPrefix := idx.module
	if pkgDir != "." {
		pkgPrefix = idx.module + "/" + pkgDir
	}

	// Collect import alias → import path for cross-package resolution.
	importMap := make(map[string]string) // alias → full import path
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			alias = imp.Name.Name
		} else {
			// default alias is the last segment of the import path.
			parts := strings.Split(impPath, "/")
			alias = parts[len(parts)-1]
		}
		if alias != "_" && alias != "." {
			importMap[alias] = impPath
		}
	}

	// Walk all top-level function declarations and inspect their bodies.
	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok || funcDecl.Body == nil {
			continue
		}

		// Build the caller's symbol name (matches the chunker's naming).
		callerName := funcDecl.Name.Name
		if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
			recvType := exprTypeName(funcDecl.Recv.List[0].Type)
			callerName = recvType + "." + callerName
		}
		callerFQN := pkgPrefix + "." + callerName

		// Inspect the function body for call expressions.
		ast.Inspect(funcDecl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			calleeFQN := resolveCallFQN(call.Fun, importMap, localFQN, pkgPrefix)
			if calleeFQN == "" || calleeFQN == callerFQN {
				return true
			}

			// Ensure the callee symbol node exists (best-effort upsert).
			_ = idx.db.UpsertSymbol(kuzu_symbol(calleeFQN))
			_ = idx.db.AddCalls(callerFQN, calleeFQN)
			return true
		})
	}
	return nil
}

// resolveCallFQN attempts to convert a call expression function node into a FQN.
func resolveCallFQN(fun ast.Expr, imports map[string]string, local map[string]string, pkgPrefix string) string {
	switch f := fun.(type) {
	case *ast.Ident:
		// Direct call: fmt.Sprintf → ident is "Sprintf" after selector parsing
		name := f.Name
		if fqn, ok := local[name]; ok {
			return fqn
		}
		// Could be a built-in or local top-level function.
		return pkgPrefix + "." + name

	case *ast.SelectorExpr:
		// pkg.Func or receiver.Method
		pkgAlias := ""
		if ident, ok := f.X.(*ast.Ident); ok {
			pkgAlias = ident.Name
		}
		funcName := f.Sel.Name

		if importPath, ok := imports[pkgAlias]; ok {
			// Cross-package call.
			return importPath + "." + funcName
		}
		// Method call on a local variable/type — use local package prefix.
		return pkgPrefix + "." + pkgAlias + "." + funcName

	case *ast.IndexExpr:
		// Generic instantiation call — recurse on the base.
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

// kuzu_symbol builds a minimal SymbolNode for an unknown callee so that the
// CALLS edge can still be stored even if the callee isn't defined in this codebase.
func kuzu_symbol(fqn string) kuzu.SymbolNode {
	parts := strings.SplitN(fqn, ".", -1)
	name := parts[len(parts)-1]
	return kuzu.SymbolNode{FQN: fqn, Kind: "function", Name: name}
}
