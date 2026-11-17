package indexer

import (
	"fmt"
	"strings"
)

// rationaleComment represents a design rationale comment extracted from source code.
type rationaleComment struct {
	Line    int
	Tag     string // WHY, NOTE, HACK, TODO, IMPORTANT, FIXME
	Content string
}

// rationaleTagPrefixes are the comment tags that indicate design rationale.
var rationaleTagPrefixes = []string{
	"WHY:", "WHY ", "NOTE:", "NOTE ", "HACK:", "HACK ",
	"TODO:", "TODO ", "IMPORTANT:", "IMPORTANT ",
	"FIXME:", "FIXME ", "BUG:", "BUG ",
}

// extractRationale scans source code for rationale comments and returns them keyed by line number.
func extractRationale(source string) map[int]rationaleComment {
	result := make(map[int]rationaleComment)
	lines := strings.Split(source, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Strip comment prefix (// or # or /* or --)
		comment := ""
		if strings.HasPrefix(trimmed, "//") {
			comment = strings.TrimSpace(trimmed[2:])
		} else if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
			comment = strings.TrimSpace(trimmed[1:])
		} else if strings.HasPrefix(trimmed, "--") {
			comment = strings.TrimSpace(trimmed[2:])
		}

		if comment == "" {
			continue
		}

		upper := strings.ToUpper(comment)
		for _, prefix := range rationaleTagPrefixes {
			if strings.HasPrefix(upper, prefix) {
				tag := strings.TrimRight(prefix, ": ")
				content := strings.TrimSpace(comment[len(prefix):])
				if len(content) > 0 {
					// Also try extracting from original case
					if len(comment) > len(prefix) {
						content = strings.TrimSpace(comment[len(prefix):])
					}
				}
				result[i+1] = rationaleComment{
					Line:    i + 1,
					Tag:     tag,
					Content: content,
				}
				break
			}
		}
	}
	return result
}

// attachRationale collects rationale comments within or near a symbol's line range
// and returns them as a combined doc string.
func attachRationale(rationale map[int]rationaleComment, startLine, endLine int) string {
	if len(rationale) == 0 {
		return ""
	}

	var parts []string
	// Check 3 lines before the symbol (preamble comments) through end of symbol
	for line := startLine - 3; line <= endLine; line++ {
		if r, ok := rationale[line]; ok {
			parts = append(parts, fmt.Sprintf("[%s] %s", r.Tag, r.Content))
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ")
}
