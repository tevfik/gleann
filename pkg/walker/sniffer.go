package walker

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// FileCategory classifies the nature of file content for semantic pipelines.
type FileCategory string

const (
	CategoryCode     FileCategory = "code"
	CategoryDoc      FileCategory = "doc"
	CategoryConfig   FileCategory = "config"
	CategoryData     FileCategory = "data"
	CategoryBinary   FileCategory = "binary"
	CategoryMinified FileCategory = "minified"
	CategoryUnknown  FileCategory = "unknown"
)

// SniffResult holds content sniffing and classification results.
type SniffResult struct {
	Category   FileCategory `json:"category"`
	Language   string       `json:"language,omitempty"`
	IsBinary   bool         `json:"is_binary"`
	IsMinified bool         `json:"is_minified"`
	Shebang    string       `json:"shebang,omitempty"`
}

var shebangRegex = regexp.MustCompile(`^#!\s*(?:/usr/bin/env\s+|/bin/|/usr/bin/)?([a-zA-Z0-9_\-\.]+)(?:\s+(.*))?`)

// IsBinaryData inspects byte slice for null bytes and excessive non-printable control characters.
func IsBinaryData(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	limit := len(data)
	if limit > 1024 {
		limit = 1024
	}
	sample := data[:limit]

	// Immediate null-byte check
	if bytes.IndexByte(sample, 0x00) != -1 {
		return true
	}

	// Count non-printable control characters (excluding \t, \n, \r, \f)
	controlCount := 0
	for _, b := range sample {
		if b < 0x20 && b != 0x09 && b != 0x0A && b != 0x0D && b != 0x0C {
			controlCount++
		}
	}

	// If more than 10% are control chars, treat as binary
	if float64(controlCount)/float64(limit) > 0.10 {
		return true
	}

	// Also verify UTF-8 validity if mostly non-ASCII
	nonAscii := 0
	for _, b := range sample {
		if b >= 0x80 {
			nonAscii++
		}
	}
	if nonAscii > limit/2 && !utf8.Valid(sample) {
		return true
	}

	return false
}

// SniffBytes determines content type, language, and classification from filename and initial bytes.
func SniffBytes(filename string, data []byte) SniffResult {
	base := filepath.Base(filename)
	ext := strings.ToLower(filepath.Ext(base))

	// 1. Check binary data first
	if IsBinaryData(data) {
		return SniffResult{
			Category: CategoryBinary,
			IsBinary: true,
		}
	}

	// 2. Check minification
	isMin := false
	lowerBase := strings.ToLower(base)
	if strings.Contains(lowerBase, ".min.") {
		isMin = true
	} else if len(data) > 0 {
		firstLine := data
		if idx := bytes.IndexByte(data, '\n'); idx != -1 {
			firstLine = data[:idx]
		}
		if len(firstLine) > 2000 {
			isMin = true
		}
	}

	res := SniffResult{
		IsBinary:   false,
		IsMinified: isMin,
	}

	// 3. Check well-known exact filenames
	switch {
	case lowerBase == "dockerfile" || strings.HasPrefix(lowerBase, "dockerfile.") || lowerBase == "containerfile":
		res.Category = CategoryConfig
		res.Language = "dockerfile"
		return res
	case lowerBase == "makefile" || lowerBase == "gnumakefile" || lowerBase == "justfile" || lowerBase == "earthfile":
		res.Category = CategoryConfig
		res.Language = "makefile"
		return res
	case lowerBase == "cmakelists.txt":
		res.Category = CategoryConfig
		res.Language = "cmake"
		return res
	case lowerBase == "gemfile" || lowerBase == "rakefile":
		res.Category = CategoryCode
		res.Language = "ruby"
		return res
	case lowerBase == "vagrantfile" || lowerBase == "procfile" || lowerBase == "brewfile":
		res.Category = CategoryConfig
		res.Language = "config"
		return res
	}

	// 4. Check shebang line
	if bytes.HasPrefix(data, []byte("#!")) {
		lineEnd := bytes.IndexByte(data, '\n')
		var firstLine string
		if lineEnd != -1 {
			firstLine = string(data[:lineEnd])
		} else {
			firstLine = string(data)
		}

		firstLine = strings.TrimSpace(firstLine)
		matches := shebangRegex.FindStringSubmatch(firstLine)
		if len(matches) > 1 {
			cmd := strings.ToLower(matches[1])
			res.Shebang = firstLine
			switch {
			case strings.HasPrefix(cmd, "python"):
				res.Category = CategoryCode
				res.Language = "python"
				return res
			case cmd == "bash" || cmd == "sh" || cmd == "zsh" || cmd == "ksh":
				res.Category = CategoryCode
				res.Language = "bash"
				return res
			case cmd == "node" || cmd == "nodejs" || cmd == "bun" || cmd == "deno":
				res.Category = CategoryCode
				res.Language = "javascript"
				return res
			case cmd == "ruby":
				res.Category = CategoryCode
				res.Language = "ruby"
				return res
			case cmd == "perl":
				res.Category = CategoryCode
				res.Language = "perl"
				return res
			case cmd == "php":
				res.Category = CategoryCode
				res.Language = "php"
				return res
			}
		}
	}

	// 5. Fallback to extension mapping
	switch ext {
	case ".go":
		res.Category = CategoryCode
		res.Language = "go"
	case ".py", ".pyw":
		res.Category = CategoryCode
		res.Language = "python"
	case ".js", ".mjs", ".cjs":
		res.Category = CategoryCode
		res.Language = "javascript"
	case ".ts", ".mts", ".cts":
		res.Category = CategoryCode
		res.Language = "typescript"
	case ".jsx", ".tsx":
		res.Category = CategoryCode
		res.Language = "react"
	case ".rs":
		res.Category = CategoryCode
		res.Language = "rust"
	case ".c", ".h":
		res.Category = CategoryCode
		res.Language = "c"
	case ".cpp", ".cc", ".cxx", ".hpp", ".hh":
		res.Category = CategoryCode
		res.Language = "cpp"
	case ".java":
		res.Category = CategoryCode
		res.Language = "java"
	case ".kt", ".kts":
		res.Category = CategoryCode
		res.Language = "kotlin"
	case ".cs":
		res.Category = CategoryCode
		res.Language = "csharp"
	case ".rb":
		res.Category = CategoryCode
		res.Language = "ruby"
	case ".php":
		res.Category = CategoryCode
		res.Language = "php"
	case ".sh", ".bash", ".zsh":
		res.Category = CategoryCode
		res.Language = "bash"
	case ".sql":
		res.Category = CategoryCode
		res.Language = "sql"
	case ".md", ".markdown", ".rst", ".adoc", ".txt":
		res.Category = CategoryDoc
		res.Language = "markdown"
	case ".json", ".yaml", ".yml", ".toml", ".xml", ".ini", ".env":
		res.Category = CategoryConfig
		res.Language = strings.TrimPrefix(ext, ".")
	case ".csv", ".tsv", ".jsonl":
		res.Category = CategoryData
		res.Language = strings.TrimPrefix(ext, ".")
	default:
		res.Category = CategoryUnknown
	}

	return res
}

// SniffFile opens the file, reads up to 1024 bytes, and classifies the content.
func SniffFile(filePath string) (SniffResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return SniffResult{Category: CategoryUnknown}, err
	}
	defer f.Close()

	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return SniffResult{Category: CategoryUnknown}, err
	}

	return SniffBytes(filePath, buf[:n]), nil
}
