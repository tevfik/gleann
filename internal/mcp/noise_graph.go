//go:build treesitter

package mcp

import "strings"

// isNoiseSymbol returns true if the symbol is a trivial boilerplate, compiler-generated block,
// or low-level primitive that adds noise to architectural maps.
func isNoiseSymbol(name, kind string) bool {
	if kind == "variable" || kind == "field" || kind == "parameter" || kind == "constant" || kind == "macro" || kind == "block" {
		return true
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "block_") || strings.HasPrefix(lower, "lambda_") || strings.HasPrefix(lower, "__") {
		return true
	}
	switch lower {
	case "trailing", "init", "main", "str", "get", "set", "size", "len", "free", "malloc", "memcpy", "memset", "operator()", "operator=", "operator==":
		return true
	}
	return false
}
