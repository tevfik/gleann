//go:build treesitter

// Package indexer — language-specific symbol weighting.
//
// Different languages emphasise different constructs: in Java, an
// interface or abstract class is the dominant design surface; in Python,
// a @decorator marks a "wire-up" function; in Rust, a trait or impl block
// is the abstraction unit. A flat 1.0 weight for every symbol throws
// away that signal and makes retrieval/risk analysis treat a private
// helper identically to a public interface contract.
//
// heuristics.applyHeuristics returns a per-symbol weight in the range
// [defaultWeight, maxWeight] (typically [1.0, 2.0]) based on:
//   - the language's `nodeTypeRules` (interface, trait, impl, decorated, …)
//   - visibility markers captured in the chunk (export, pub, public)
//   - a small "importance bonus" map per language for the constructs
//     that gate the largest fan-out (callers, callees, implementers)
//
// The weights are written to the Symbol.weight column in KuzuDB and
// consumed by internal/graph/community.ComputeRiskScores (PageRank
// weighted by node weight) and the reranker in gleann/rerank.
//
// This file is intentionally data-driven: heuristics is a small map and
// adding a new language is a one-line entry. New "rules of thumb" can
// be added without touching the indexer hot path.
package indexer

import (
	"strings"

	"github.com/tevfik/gleann/modules/chunking"
)

// defaultWeight is the neutral baseline. Symbols not matched by any
// rule keep this value, so we never accidentally demote a legitimate
// symbol below 1.0.
const defaultWeight = 1.0

// maxWeight caps the highest possible score. We bound the range so a
// hypothetical "very important" rule can't dwarf PageRank and dominate
// the community/risk results.
const maxWeight = 2.0

// languageHeuristics maps a language to its per-rule weight modifiers.
// The map values are additive: a Java abstract class that is also
// exported gets the abstract boost plus the export boost.
//
// Rules are kept conservative (≤0.3 per rule) so the result stays in
// a meaningful, comparable band across languages.
var languageHeuristics = map[chunking.Language]map[string]float64{
	chunking.LangJava: {
		"interface_declaration": 0.5, // interfaces define the public contract
		"abstract":              0.3, // abstract class or method
		"annotation_declaration": 0.2,
	},
	chunking.LangKotlin: {
		"interface_declaration": 0.5,
		"abstract":              0.3,
	},
	chunking.LangScala: {
		"trait_definition": 0.5,
		"abstract":         0.3,
	},
	chunking.LangPython: {
		"decorated_definition": 0.4, // @decorator — entry point / hook
		"async_function":       0.2, // concurrency entry point
		"dunder":               0.3, // __init__ / __call__ etc.
	},
	chunking.LangTypeScript: {
		"interface_declaration": 0.5,
		"abstract_class":        0.3,
		"export_statement":      0.2, // public surface
	},
	chunking.LangJavaScript: {
		"export_statement": 0.2,
	},
	chunking.LangRust: {
		"trait_item": 0.5, // trait = interface
		"impl_item":  0.2, // implements a contract
		"pub":        0.2, // public visibility
	},
	chunking.LangCSharp: {
		"interface_declaration": 0.5,
		"abstract":              0.3,
	},
	chunking.LangSwift: {
		"protocol_declaration": 0.5,
		"open":                 0.2, // overridable / public
	},
	chunking.LangCPP: {
		"virtual":    0.3, // overridable method
		"abstract":   0.3,
		"public":     0.1,
	},
	chunking.LangPHP: {
		"interface_declaration": 0.5,
		"trait_declaration":     0.3,
		"abstract":              0.2,
	},
	chunking.LangGo: {
		// Go is intentionally minimal: a struct or interface IS the
		// abstraction. We do not add weight for "public" (capitalised
		// identifiers) because the chunker already filters named symbols
		// and we don't want to silently double-count visibility.
		"interface": 0.4,
		"struct":    0.2,
	},
	chunking.LangRuby: {
		"module":  0.3,
		"class":   0.2,
	},
	chunking.LangLua: {
		// Lua has no strong "interface" concept; module-level functions
		// are the most reusable unit. Keep weight neutral here.
	},
	chunking.LangElixir: {
		"defmodule": 0.3, // defines a module (Elixir's namespace/unit)
		"protocol":  0.5, // protocol = interface
	},
}

// nodeTypeWeight returns the base weight contributed by a chunk's
// semantic node type. The `kind` argument is the chunker output
// (NodeType), which is language-agnostic for the common values
// ("function", "method", "class", "interface", "struct", "type",
// "const", "var", "module", "trait", "namespace", "impl").
//
// We do NOT special-case "function" — every language has many
// functions, so boosting all of them would dilute the signal. We DO
// boost "interface", "trait", "module", "namespace" because those
// represent the public design surface of a package in most languages.
func nodeTypeWeight(kind string) float64 {
	switch kind {
	case "interface", "trait":
		return 0.3
	case "module", "namespace":
		return 0.2
	case "impl":
		return 0.15
	case "class":
		return 0.1
	}
	return 0.0
}

// applyHeuristics returns the symbol weight for a given language,
// semantic kind, and source-level hint. The hint argument may be empty
// when no decorator/visibility marker is available; callers should
// pass the literal node-type string from the tree-sitter parse (e.g.
// "decorated_definition", "export_statement", "trait_item", "pub",
// "abstract") so the per-language map can match it.
func applyHeuristics(lang chunking.Language, kind, hint string) float64 {
	w := defaultWeight
	w += nodeTypeWeight(kind)
	if rules, ok := languageHeuristics[lang]; ok {
		if boost, ok := rules[hint]; ok {
			w += boost
		}
		// Common case: the hint is the same as the kind for some
		// languages (e.g. Go's "interface" kind has matching rule key).
		if hint != kind {
			if boost, ok := rules[kind]; ok {
				w += boost
			}
		}
	}
	if w > maxWeight {
		w = maxWeight
	}
	return w
}

// symbolHintFromKind returns a hint string that can be matched against
// languageHeuristics[lang] keys. For the common Go/Python/Java cases
// the chunker kind is already meaningful; for other languages we
// derive a hint from the symbol name or the chunk's text.
func symbolHintFromKind(name, kind string) string {
	// Decorated functions in Python are usually surfaced as the inner
	// definition kind ("function"); the chunker may not propagate the
	// "decorated_definition" wrapper. Caller may pass it as hint.
	switch kind {
	case "interface":
		return "interface"
	case "trait":
		return "trait"
	case "module", "namespace":
		return "module"
	}
	// Dunder detection (Python) is cheap and high-signal.
	if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
		return "dunder"
	}
	return kind
}

// detectSourceHint inspects the bytes immediately preceding a symbol's
// start line and upgrades the hint when language-specific markers are
// found. The chunker collapses many language constructs (Python
// decorated_definition, Rust pub/impl, TS/JS export_statement, C++
// virtual/abstract) into bare "function"/"struct"/"class" kinds, so
// the kind alone under-weights the most important symbols.
//
// This is a small, bounded scan: at most 5 lines, early-exit on first
// match. It runs once per symbol (not per AST node) so the cost is
// amortised away.
func detectSourceHint(lang chunking.Language, source string, startLine int, kindHint string) string {
	if startLine <= 0 || source == "" {
		return kindHint
	}
	lines := strings.Split(source, "\n")
	if startLine > len(lines) {
		return kindHint
	}
	// Stage 1: check the declaration line itself for in-line markers
	// (e.g. "async def f()", "pub fn f()", "abstract void f()",
	// "export function f()"). These markers sit on the same line as
	// the keyword, so we have to look at startLine-1 before walking
	// upwards.
	if startLine >= 1 && startLine-1 < len(lines) {
		decl := strings.TrimSpace(lines[startLine-1])
		if hint := matchMarker(lang, decl); hint != "" {
			return hint
		}
	}

	// Stage 2: walk back up to 5 lines above the declaration. The
	// first non-empty, non-marker line is where we stop — anything
	// past it is unrelated context (e.g. an earlier statement in
	// the same function body or block).
	from := startLine - 6
	if from < 0 {
		from = 0
	}
	for i := startLine - 2; i >= from; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if hint := matchMarker(lang, trimmed); hint != "" {
			return hint
		}
		// Not a marker → stop.
		break
	}
	return kindHint
}

// matchMarker returns the heuristic hint name for a single source
// line, or "" if the line carries no marker for this language.
// Per-language cases mirror the main switch below but return the
// hint string instead of writing to a return statement, so the same
// rule set can be invoked twice (once for the declaration line, once
// for the lines above).
func matchMarker(lang chunking.Language, trimmed string) string {
	switch lang {
	case chunking.LangPython:
		if strings.HasPrefix(trimmed, "@") {
			return "decorated_definition"
		}
		if strings.HasPrefix(trimmed, "async def ") {
			return "async_function"
		}
	case chunking.LangJavaScript, chunking.LangTypeScript, chunking.LangVue, chunking.LangSvelte:
		if strings.HasPrefix(trimmed, "export ") || strings.Contains(trimmed, " export ") {
			return "export_statement"
		}
	case chunking.LangRust:
		if strings.HasPrefix(trimmed, "pub ") {
			return "pub"
		}
	case chunking.LangCPP, chunking.LangC, chunking.LangObjectiveC:
		if strings.HasPrefix(trimmed, "virtual ") {
			return "virtual"
		}
		if strings.HasPrefix(trimmed, "abstract ") {
			return "abstract"
		}
	case chunking.LangJava, chunking.LangCSharp, chunking.LangKotlin, chunking.LangScala:
		if strings.HasPrefix(trimmed, "abstract ") {
			return "abstract"
		}
	}
	return ""
}
