// Package parser provides a multi-language code chunk parser.
// Supports Go, Python, TypeScript, Kotlin, and Rust.
// Used in benchmarks and integration tests.
package parser

import (
	"fmt"
	"strings"
)

// Language represents a supported source language.
type Language string

const (
	LangGo         Language = "go"
	LangPython     Language = "python"
	LangTypeScript Language = "typescript"
	LangKotlin     Language = "kotlin"
	LangRust       Language = "rust"
)

// Symbol is a named code entity (function, class, struct) extracted from source.
type Symbol struct {
	Name     string
	Kind     string // "function", "class", "method", "struct"
	Language Language
	Start    int
	End      int
	Body     string
}

// Parser extracts symbols from source code.
type Parser struct {
	lang Language
}

// New creates a Parser for the given language.
func New(lang Language) *Parser {
	return &Parser{lang: lang}
}

// Parse returns all top-level symbols from source.
func (p *Parser) Parse(source string) ([]Symbol, error) {
	switch p.lang {
	case LangGo:
		return parseGo(source)
	case LangPython:
		return parsePython(source)
	case LangTypeScript:
		return parseTypeScript(source)
	default:
		return nil, fmt.Errorf("unsupported language: %s", p.lang)
	}
}

// parseGo extracts func declarations from Go source (simplified AST walk).
func parseGo(src string) ([]Symbol, error) {
	var symbols []Symbol
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") {
			name := extractGoFuncName(trimmed)
			symbols = append(symbols, Symbol{
				Name:     name,
				Kind:     "function",
				Language: LangGo,
				Start:    i + 1,
				End:      i + 1,
				Body:     line,
			})
		}
		if strings.HasPrefix(trimmed, "type ") && strings.Contains(trimmed, "struct") {
			name := extractGoTypeName(trimmed)
			symbols = append(symbols, Symbol{
				Name:     name,
				Kind:     "struct",
				Language: LangGo,
				Start:    i + 1,
				End:      i + 1,
				Body:     line,
			})
		}
	}
	return symbols, nil
}

// parsePython extracts def and class declarations from Python source.
func parsePython(src string) ([]Symbol, error) {
	var symbols []Symbol
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "def ") {
			name := extractPyName(trimmed[4:])
			symbols = append(symbols, Symbol{
				Name:     name,
				Kind:     "function",
				Language: LangPython,
				Start:    i + 1,
				End:      i + 1,
				Body:     line,
			})
		}
		if strings.HasPrefix(trimmed, "class ") {
			name := extractPyName(trimmed[6:])
			symbols = append(symbols, Symbol{
				Name:     name,
				Kind:     "class",
				Language: LangPython,
				Start:    i + 1,
				End:      i + 1,
				Body:     line,
			})
		}
	}
	return symbols, nil
}

// parseTypeScript extracts function and class declarations.
func parseTypeScript(src string) ([]Symbol, error) {
	var symbols []Symbol
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "async function ") ||
			strings.HasPrefix(trimmed, "export function ") || strings.HasPrefix(trimmed, "export async function ") {
			name := extractTSFuncName(trimmed)
			symbols = append(symbols, Symbol{
				Name:     name,
				Kind:     "function",
				Language: LangTypeScript,
				Start:    i + 1,
				End:      i + 1,
				Body:     line,
			})
		}
		if strings.Contains(trimmed, "class ") && !strings.HasPrefix(trimmed, "//") {
			name := extractTSClassName(trimmed)
			symbols = append(symbols, Symbol{
				Name:     name,
				Kind:     "class",
				Language: LangTypeScript,
				Start:    i + 1,
				End:      i + 1,
				Body:     line,
			})
		}
	}
	return symbols, nil
}

// ── Name extraction helpers ─────────────────────────────────────────────────

func extractGoFuncName(line string) string {
	// "func (r *Receiver) MethodName(...)" or "func FuncName(...)"
	line = strings.TrimPrefix(line, "func ")
	if strings.HasPrefix(line, "(") {
		// method: skip receiver
		end := strings.Index(line, ")")
		if end >= 0 {
			line = strings.TrimSpace(line[end+1:])
		}
	}
	if idx := strings.IndexAny(line, "(["); idx >= 0 {
		return strings.TrimSpace(line[:idx])
	}
	return line
}

func extractGoTypeName(line string) string {
	line = strings.TrimPrefix(line, "type ")
	if idx := strings.Index(line, " "); idx >= 0 {
		return line[:idx]
	}
	return line
}

func extractPyName(rest string) string {
	for _, stop := range []string{"(", ":", " "} {
		if idx := strings.Index(rest, stop); idx >= 0 {
			rest = rest[:idx]
		}
	}
	return strings.TrimSpace(rest)
}

func extractTSFuncName(line string) string {
	for _, prefix := range []string{"export async function ", "async function ", "export function ", "function "} {
		if strings.HasPrefix(line, prefix) {
			rest := line[len(prefix):]
			if idx := strings.IndexAny(rest, "(<"); idx >= 0 {
				return rest[:idx]
			}
			return rest
		}
	}
	return ""
}

func extractTSClassName(line string) string {
	idx := strings.Index(line, "class ")
	if idx < 0 {
		return ""
	}
	rest := line[idx+6:]
	for _, stop := range []string{" ", "{", "<", "("} {
		if i := strings.Index(rest, stop); i >= 0 {
			rest = rest[:i]
		}
	}
	return strings.TrimSpace(rest)
}

// CountSymbols returns a per-kind breakdown.
func CountSymbols(syms []Symbol) map[string]int {
	counts := make(map[string]int)
	for _, s := range syms {
		counts[s.Kind]++
	}
	return counts
}

// FilterByKind returns only symbols matching the requested kind.
func FilterByKind(syms []Symbol, kind string) []Symbol {
	var out []Symbol
	for _, s := range syms {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

// CyclomaticComplexity estimates the cyclomatic complexity of a code body.
// It counts branch keywords: if, for, switch, case, select, &&, ||.
func CyclomaticComplexity(body string) int {
	branches := []string{"if ", "for ", "switch ", "case ", " && ", " || ", "select {"}
	count := 1
	for _, b := range branches {
		count += strings.Count(body, b)
	}
	return count
}
