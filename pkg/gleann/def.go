package gleann

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefinitionResult represents the definition location, signature, and documentation of a symbol.
type DefinitionResult struct {
	FQN       string  `json:"fqn"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	File      string  `json:"file"`
	Line      int64   `json:"line"`
	Signature string  `json:"signature,omitempty"`
	Doc       string  `json:"doc,omitempty"`
	Score     float32 `json:"score,omitempty"`
}

// DefinitionOptions configures definition lookup.
type DefinitionOptions struct {
	TopK    int    // Max definitions to return (default: 5)
	Kind    string // Filter by kind (e.g., "function", "method", "struct", "class", "interface")
	RootDir string // Base directory for resolving relative file paths
}

// ExtractSignature parses lines around targetLine (1-indexed) from content.
// It returns the signature and any immediately preceding doc comments.
func ExtractSignature(content string, targetLine int) (string, string) {
	if content == "" || targetLine <= 0 {
		return "", ""
	}

	lines := strings.Split(content, "\n")
	if targetLine > len(lines) {
		return "", ""
	}

	idx := targetLine - 1

	// 1. Extract preceding doc comments
	var docLines []string
	for i := idx - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "//") {
			trimmed := strings.TrimSpace(strings.TrimPrefix(line, "//"))
			docLines = append([]string{trimmed}, docLines...)
		} else if strings.HasPrefix(line, "#") {
			trimmed := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			docLines = append([]string{trimmed}, docLines...)
		} else {
			// Stop at empty line or non-comment
			break
		}
	}
	doc := strings.Join(docLines, " ")

	// 2. Extract signature starting from targetLine
	var sigLines []string
	maxLines := 6
	for i := idx; i < len(lines) && i < idx+maxLines; i++ {
		line := lines[i]
		sigLines = append(sigLines, strings.TrimRight(line, "\r"))

		trimmed := strings.TrimSpace(line)
		// Stop if signature ends
		if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, ":") || strings.HasSuffix(trimmed, ";") {
			break
		}
	}

	signature := strings.Join(sigLines, "\n")
	signature = strings.TrimSpace(signature)
	return signature, doc
}

// ExtractSignatureFromFile reads the file at filePath and extracts the signature at targetLine.
func ExtractSignatureFromFile(filePath string, targetLine int) (string, string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", ""
	}
	return ExtractSignature(string(data), targetLine)
}

// FindDefinitions locates where a symbol is defined in the codebase.
func FindDefinitions(ctx context.Context, searcher *LeannSearcher, query string, opts DefinitionOptions) ([]DefinitionResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("symbol query cannot be empty")
	}

	topK := opts.TopK
	if topK <= 0 {
		topK = 5
	}

	var results []DefinitionResult

	// Path 1: High-precision AST Graph lookup (KùzuDB)
	if searcher != nil && searcher.GraphDB() != nil {
		symbols, err := searcher.GraphDB().SymbolSearch(query)
		if err == nil && len(symbols) > 0 {
			for _, sym := range symbols {
				if opts.Kind != "" && !strings.EqualFold(sym.Kind, opts.Kind) {
					continue
				}

				def := DefinitionResult{
					FQN:   sym.FQN,
					Name:  sym.Name,
					Kind:  sym.Kind,
					File:  sym.File,
					Line:  sym.Line,
					Score: 0.70,
				}

				if strings.EqualFold(sym.Name, query) {
					def.Score = 1.0
				} else if strings.EqualFold(sym.FQN, query) {
					def.Score = 0.95
				} else if strings.HasSuffix(strings.ToLower(sym.FQN), "."+strings.ToLower(query)) {
					def.Score = 0.85
				}

				if sym.IsTest {
					def.Score *= 0.8
				}

				// Resolve signature from file if line and file are available
				if sym.File != "" && sym.Line > 0 {
					targetPath := sym.File
					if !filepath.IsAbs(targetPath) && opts.RootDir != "" {
						targetPath = filepath.Join(opts.RootDir, targetPath)
					}
					sig, doc := ExtractSignatureFromFile(targetPath, int(sym.Line))
					def.Signature = sig
					def.Doc = doc
				}

				results = append(results, def)
			}
		}
	}

	// Path 2: Fallback to passage search when GraphDB is not available or yields 0 results
	if len(results) == 0 && searcher != nil {
		searchResults, err := searcher.Search(ctx, query, WithTopK(topK*2))
		if err == nil {
			for _, sr := range searchResults {
				source, _ := sr.Metadata["source"].(string)
				foundDef := scanPassageForSymbol(sr.Text, query, source)
				if foundDef != nil {
					if opts.Kind != "" && !strings.EqualFold(foundDef.Kind, opts.Kind) {
						continue
					}
					results = append(results, *foundDef)
				}
			}
		}
	}

	// Sort results descending by score
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}

	return results, nil
}

// scanPassageForSymbol scans lines of passage text for common declaration patterns
func scanPassageForSymbol(text, query, source string) *DefinitionResult {
	scanner := bufio.NewScanner(strings.NewReader(text))
	lineNum := 1
	lowerQuery := strings.ToLower(query)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		lowerTrimmed := strings.ToLower(trimmed)

		// Check function, type, struct, class, interface definitions
		isDef := false
		kind := "symbol"

		if strings.HasPrefix(lowerTrimmed, "func ") && strings.Contains(lowerTrimmed, lowerQuery) {
			isDef = true
			kind = "function"
			if strings.Contains(trimmed, ")") && strings.Index(trimmed, ")") < strings.Index(trimmed, query) {
				kind = "method"
			}
		} else if strings.HasPrefix(lowerTrimmed, "type ") && strings.Contains(lowerTrimmed, lowerQuery) {
			isDef = true
			if strings.Contains(lowerTrimmed, "struct") {
				kind = "struct"
			} else if strings.Contains(lowerTrimmed, "interface") {
				kind = "interface"
			} else {
				kind = "type"
			}
		} else if strings.HasPrefix(lowerTrimmed, "def ") && strings.Contains(lowerTrimmed, lowerQuery) {
			isDef = true
			kind = "function"
		} else if strings.HasPrefix(lowerTrimmed, "class ") && strings.Contains(lowerTrimmed, lowerQuery) {
			isDef = true
			kind = "class"
		}

		if isDef {
			sig, doc := ExtractSignature(text, lineNum)
			return &DefinitionResult{
				Name:      query,
				FQN:       query,
				Kind:      kind,
				File:      source,
				Line:      int64(lineNum),
				Signature: sig,
				Doc:       doc,
				Score:     0.80,
			}
		}
		lineNum++
	}
	return nil
}

// FormatDefinitions renders a concise, token-efficient view of symbol definitions.
func FormatDefinitions(defs []DefinitionResult) string {
	if len(defs) == 0 {
		return "No symbol definitions found."
	}

	var sb strings.Builder
	for i, def := range defs {
		sb.WriteString(fmt.Sprintf("---\n[%d] Symbol: %s (%s)\n", i+1, def.FQN, def.Kind))
		if def.File != "" {
			if def.Line > 0 {
				sb.WriteString(fmt.Sprintf("Defined at: %s:%d\n", def.File, def.Line))
			} else {
				sb.WriteString(fmt.Sprintf("Defined at: %s\n", def.File))
			}
		}
		if def.Signature != "" {
			sb.WriteString("Signature:\n  ")
			indented := strings.ReplaceAll(def.Signature, "\n", "\n  ")
			sb.WriteString(indented)
			sb.WriteString("\n")
		}
		if def.Doc != "" {
			sb.WriteString(fmt.Sprintf("Doc:\n  %s\n", def.Doc))
		}
	}

	sb.WriteString("\n---\nTip: To view full surrounding file context, run: gleann read <file> --lines <N>\n")
	return sb.String()
}
