// Elixir macro keywords that the call extractor must not treat as callees.
//
// In the Elixir grammar every macro invocation is a `call` node with the macro
// name in its `target` field, so `def`, `defp`, `defmodule`, `alias`, `import`,
// `use` and `require` are structurally indistinguishable from function calls.
// They define or configure something instead of calling a project function, so
// they are filtered by name (they cannot be shadowed: all of them are macros of
// the language core or of Kernel).
package chunking

// elixirNonCallKeywords holds the definition, directive and control-flow macro
// keywords that never denote a project function. The control-flow macros are
// Kernel macros that expand to `case`/anonymous functions, not call sites.
var elixirNonCallKeywords = map[string]bool{
	// Declarations (definitions handled by the chunker as symbols).
	"def":            true,
	"defp":           true,
	"defmodule":      true,
	"defprotocol":    true,
	"defimpl":        true,
	"defmacro":       true,
	"defmacrop":      true,
	"defdelegate":    true,
	"defguard":       true,
	"defguardp":      true,
	"defstruct":      true,
	"defexception":   true,
	"defoverridable": true,
	// Directives.
	"alias":   true,
	"import":  true,
	"require": true,
	"use":     true,
	"quote":   true,
	"unquote": true,
	// Control-flow macros: they expand at compile time and never bind to a
	// function declared in the project.
	"if":      true,
	"unless":  true,
	"case":    true,
	"cond":    true,
	"with":    true,
	"for":     true,
	"try":     true,
	"receive": true,
}

// IsElixirNonCallKeyword reports whether name is an Elixir definition,
// directive or control-flow macro keyword, i.e. a `call` node that must not be
// recorded as a CALLS edge.
func IsElixirNonCallKeyword(name string) bool { return elixirNonCallKeywords[name] }

// elixirDefiningMacros holds the macros that declare a function or guard. Their
// argument list is a definition header (a signature, optionally followed by
// `when` guards), not a call site.
var elixirDefiningMacros = map[string]bool{
	"def":         true,
	"defp":        true,
	"defmacro":    true,
	"defmacrop":   true,
	"defdelegate": true,
	"defguard":    true,
	"defguardp":   true,
}

// IsElixirDefiningMacro reports whether name defines a function or guard.
func IsElixirDefiningMacro(name string) bool { return elixirDefiningMacros[name] }

// elixirTypespecAttributes holds the module attributes whose value is a type
// specification. Every expression inside one declares or references a type, so
// none of them is a function call: in `@spec foo(integer()) :: integer()` the
// attribute name, `foo`, `integer` and the return type are all types.
var elixirTypespecAttributes = map[string]bool{
	"spec":          true,
	"callback":      true,
	"macrocallback": true,
	"type":          true,
	"typep":         true,
	"opaque":        true,
}

// IsElixirTypespecAttribute reports whether name is a module attribute whose
// value is a typespec (`@spec`, `@callback`, `@macrocallback`, `@type`,
// `@typep` or `@opaque`).
func IsElixirTypespecAttribute(name string) bool { return elixirTypespecAttributes[name] }
