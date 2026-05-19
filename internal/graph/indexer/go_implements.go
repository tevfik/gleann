//go:build treesitter

package indexer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"github.com/tevfik/gleann/internal/graph/kuzu"
	"github.com/tevfik/gleann/modules/chunking"
)

// collectGoImplementsEdges inspects Go source with go/ast and returns
// IMPLEMENTS edges based on:
//   - Struct embedding an interface type (explicit embedding)
//   - Interface embedding another interface
//
// Also extracts REFERENCES edges from:
//   - Function parameter types referencing imported/local types
//   - Function return types referencing imported/local types
//   - Struct field types referencing imported/local types
func collectGoImplementsEdges(idx *Indexer, relPath, source string, chunks []chunking.CodeChunk) (
	impls []kuzu.EdgeImplements, refs []kuzu.EdgeReferences, extraSyms []kuzu.SymbolNode,
) {
	fset := token.NewFileSet()
	file, parseErr := parser.ParseFile(fset, relPath, source, 0)
	if parseErr != nil {
		return nil, nil, nil
	}

	// Build local FQN map.
	localFQN := make(map[string]string)
	for _, ch := range chunks {
		if ch.Name != "" {
			localFQN[ch.Name] = idx.buildFQN(relPath, ch.Name)
		}
	}

	// Build import map.
	importMap := make(map[string]string) // alias → import path
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

	// Track all interface type names declared in this file.
	localInterfaces := make(map[string]bool)
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, isIface := ts.Type.(*ast.InterfaceType); isIface {
				localInterfaces[ts.Name.Name] = true
			}
		}
	}

	seenImpl := make(map[string]bool)
	seenRef := make(map[string]bool)

	pkgPrefix := idx.buildPkgPrefix(relPath)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			typeName := ts.Name.Name
			typeFQN := resolveFQN(typeName, localFQN, pkgPrefix)

			switch t := ts.Type.(type) {
			case *ast.StructType:
				// Check embedded fields for interface embedding → IMPLEMENTS.
				if t.Fields != nil {
					for _, field := range t.Fields.List {
						if len(field.Names) == 0 { // Anonymous (embedded) field
							embeddedFQN := resolveTypeRefFQN(field.Type, importMap, localFQN, pkgPrefix)
							embeddedName := typeExprName(field.Type)
							if embeddedFQN != "" && localInterfaces[embeddedName] {
								key := typeFQN + "→" + embeddedFQN
								if !seenImpl[key] {
									seenImpl[key] = true
									impls = append(impls, kuzu.EdgeImplements{
										ImplFQN:  typeFQN,
										IfaceFQN: embeddedFQN,
									})
								}
							} else if embeddedFQN != "" {
								// Embedding a struct → REFERENCES
								key := typeFQN + "→" + embeddedFQN
								if !seenRef[key] {
									seenRef[key] = true
									extraSyms = append(extraSyms, kuzu_symbol(embeddedFQN))
									refs = append(refs, kuzu.EdgeReferences{
										RefererFQN: typeFQN,
										RefereeFQN: embeddedFQN,
										Confidence: "extracted",
									})
								}
							}
						} else {
							// Named field — extract type reference.
							refFQN := resolveTypeRefFQN(field.Type, importMap, localFQN, pkgPrefix)
							if refFQN != "" && refFQN != typeFQN {
								key := typeFQN + "→" + refFQN
								if !seenRef[key] {
									seenRef[key] = true
									extraSyms = append(extraSyms, kuzu_symbol(refFQN))
									refs = append(refs, kuzu.EdgeReferences{
										RefererFQN: typeFQN,
										RefereeFQN: refFQN,
										Confidence: "extracted",
									})
								}
							}
						}
					}
				}

			case *ast.InterfaceType:
				// Interface embedding another interface → IMPLEMENTS.
				if t.Methods != nil {
					for _, method := range t.Methods.List {
						if len(method.Names) == 0 { // embedded interface
							embFQN := resolveTypeRefFQN(method.Type, importMap, localFQN, pkgPrefix)
							if embFQN != "" {
								key := typeFQN + "→" + embFQN
								if !seenImpl[key] {
									seenImpl[key] = true
									extraSyms = append(extraSyms, kuzu_symbol(embFQN))
									impls = append(impls, kuzu.EdgeImplements{
										ImplFQN:  typeFQN,
										IfaceFQN: embFQN,
									})
								}
							}
						}
					}
				}
			}
		}
	}

	// Extract REFERENCES from function signatures (params + returns).
	for _, decl := range file.Decls {
		funcDecl, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		funcName := funcDecl.Name.Name
		if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
			recvType := exprTypeName(funcDecl.Recv.List[0].Type)
			funcName = recvType + "." + funcName
		}
		funcFQN := resolveFQN(funcName, localFQN, pkgPrefix)

		// Params
		if funcDecl.Type.Params != nil {
			for _, param := range funcDecl.Type.Params.List {
				refFQN := resolveTypeRefFQN(param.Type, importMap, localFQN, pkgPrefix)
				if refFQN != "" && refFQN != funcFQN {
					key := funcFQN + "→" + refFQN
					if !seenRef[key] {
						seenRef[key] = true
						extraSyms = append(extraSyms, kuzu_symbol(refFQN))
						refs = append(refs, kuzu.EdgeReferences{
							RefererFQN: funcFQN,
							RefereeFQN: refFQN,
							Confidence: "extracted",
						})
					}
				}
			}
		}
		// Returns
		if funcDecl.Type.Results != nil {
			for _, result := range funcDecl.Type.Results.List {
				refFQN := resolveTypeRefFQN(result.Type, importMap, localFQN, pkgPrefix)
				if refFQN != "" && refFQN != funcFQN {
					key := funcFQN + "→" + refFQN
					if !seenRef[key] {
						seenRef[key] = true
						extraSyms = append(extraSyms, kuzu_symbol(refFQN))
						refs = append(refs, kuzu.EdgeReferences{
							RefererFQN: funcFQN,
							RefereeFQN: refFQN,
							Confidence: "extracted",
						})
					}
				}
			}
		}
	}

	return impls, refs, extraSyms
}

// resolveTypeRefFQN resolves a type expression to an FQN.
// Returns "" if the type can't be resolved or is a builtin.
func resolveTypeRefFQN(expr ast.Expr, imports map[string]string, local map[string]string, pkgPrefix string) string {
	switch t := expr.(type) {
	case *ast.Ident:
		name := t.Name
		// Skip builtins.
		if isBuiltinType(name) {
			return ""
		}
		return resolveFQN(name, local, pkgPrefix)

	case *ast.SelectorExpr:
		ident, ok := t.X.(*ast.Ident)
		if !ok {
			return ""
		}
		if impPath, ok := imports[ident.Name]; ok {
			return impPath + "." + t.Sel.Name
		}
		return ""

	case *ast.StarExpr:
		return resolveTypeRefFQN(t.X, imports, local, pkgPrefix)

	case *ast.ArrayType:
		return resolveTypeRefFQN(t.Elt, imports, local, pkgPrefix)

	case *ast.MapType:
		// Return the value type (more interesting for references).
		return resolveTypeRefFQN(t.Value, imports, local, pkgPrefix)

	case *ast.InterfaceType:
		return "" // anonymous interface

	case *ast.FuncType:
		return "" // function type

	default:
		return ""
	}
}

// typeExprName extracts the simple name from a type expression.
func typeExprName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return typeExprName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

// resolveFQN resolves a name to FQN using local map or package prefix.
func resolveFQN(name string, local map[string]string, pkgPrefix string) string {
	if fqn, ok := local[name]; ok {
		return fqn
	}
	return pkgPrefix + "." + name
}

// buildPkgPrefix derives the FQN prefix for the package containing relPath.
func (idx *Indexer) buildPkgPrefix(relPath string) string {
	dir := strings.ReplaceAll(strings.TrimSuffix(relPath, "/"+lastComponent(relPath)), "\\", "/")
	if dir == "" || dir == "." {
		return idx.module
	}
	return idx.module + "/" + dir
}

func lastComponent(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// isBuiltinType returns true for Go builtin types that should not generate edges.
func isBuiltinType(name string) bool {
	switch name {
	case "bool", "byte", "complex64", "complex128", "error",
		"float32", "float64", "int", "int8", "int16", "int32", "int64",
		"rune", "string", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"any", "comparable":
		return true
	}
	return false
}
