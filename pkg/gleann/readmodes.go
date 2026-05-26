package gleann

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ReadMode defines how a file is read and presented to the LLM.
type ReadMode string

const (
	// ReadModeFull returns the entire file content (default).
	ReadModeFull ReadMode = "full"
	// ReadModeMap returns a structural overview: headings, function/class names, and line counts.
	ReadModeMap ReadMode = "map"
	// ReadModeSignatures returns only function/method/class signatures (no bodies).
	ReadModeSignatures ReadMode = "signatures"
	// ReadModeDiff returns only git-modified hunks since the last commit.
	ReadModeDiff ReadMode = "diff"
	// ReadModeEntropy returns high-information-density lines (complex logic, decisions, errors).
	ReadModeEntropy ReadMode = "entropy"
	// ReadModeLines returns specific line ranges (e.g., "10:50" or "1:20,80:100").
	ReadModeLines ReadMode = "lines"
	// ReadModeTask returns lines relevant to a given task/query description.
	ReadModeTask ReadMode = "task"
	// ReadModeReference returns imports, exports, and type definitions only.
	ReadModeReference ReadMode = "reference"
	// ReadModeAggressive strips all comments, blank lines, and non-essential formatting.
	ReadModeAggressive ReadMode = "aggressive"
	// ReadModeAuto selects the optimal mode based on file size and type.
	ReadModeAuto ReadMode = "auto"
)

// AllReadModes returns the list of all supported read modes.
func AllReadModes() []ReadMode {
	return []ReadMode{
		ReadModeFull, ReadModeMap, ReadModeSignatures, ReadModeDiff,
		ReadModeEntropy, ReadModeLines, ReadModeTask, ReadModeReference,
		ReadModeAggressive, ReadModeAuto,
	}
}

// ReadModeOptions configures mode-specific parameters.
type ReadModeOptions struct {
	// Mode selects the read mode.
	Mode ReadMode
	// LineRanges for ReadModeLines (e.g., "10:50" or "1:20,80:100").
	LineRanges string
	// TaskQuery for ReadModeTask — natural language description of what to look for.
	TaskQuery string
	// MaxLines limits output for any mode; 0 = unlimited.
	MaxLines int
}

// ReadFileWithMode reads a file applying the specified mode.
func ReadFileWithMode(filePath string, opts ReadModeOptions) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(data)

	mode := opts.Mode
	if mode == ReadModeAuto {
		mode = autoSelectMode(filePath, content)
	}

	var result string
	switch mode {
	case ReadModeFull:
		result = content
	case ReadModeMap:
		result = extractMap(filePath, content)
	case ReadModeSignatures:
		result = extractSignatures(filePath, content)
	case ReadModeDiff:
		result, err = extractDiff(filePath)
		if err != nil {
			return "", err
		}
	case ReadModeEntropy:
		result = extractHighEntropy(content)
	case ReadModeLines:
		result, err = extractLineRanges(content, opts.LineRanges)
		if err != nil {
			return "", err
		}
	case ReadModeTask:
		result = extractTaskRelevant(content, opts.TaskQuery)
	case ReadModeReference:
		result = extractReferences(filePath, content)
	case ReadModeAggressive:
		result = extractAggressive(content)
	default:
		result = content
	}

	if opts.MaxLines > 0 {
		lines := strings.Split(result, "\n")
		if len(lines) > opts.MaxLines {
			result = strings.Join(lines[:opts.MaxLines], "\n") +
				fmt.Sprintf("\n  [...%d lines omitted...]", len(lines)-opts.MaxLines)
		}
	}

	return result, nil
}

// autoSelectMode picks the best mode based on file characteristics.
func autoSelectMode(filePath, content string) ReadMode {
	lines := strings.Count(content, "\n") + 1
	ext := strings.ToLower(filepath.Ext(filePath))

	// Small files: full is fine
	if lines <= 100 {
		return ReadModeFull
	}

	// Large code files: use map
	codeExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".rs": true,
		".java": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true,
		".rb": true, ".swift": true, ".kt": true, ".cs": true, ".php": true,
	}
	if codeExts[ext] {
		if lines > 500 {
			return ReadModeMap
		}
		return ReadModeSignatures
	}

	// Config/data files: full for small, aggressive for large
	dataExts := map[string]bool{
		".json": true, ".yaml": true, ".yml": true, ".toml": true,
		".xml": true, ".csv": true, ".env": true,
	}
	if dataExts[ext] {
		if lines > 200 {
			return ReadModeAggressive
		}
		return ReadModeFull
	}

	// Markdown/docs: map for large
	if ext == ".md" || ext == ".rst" || ext == ".txt" {
		if lines > 300 {
			return ReadModeMap
		}
		return ReadModeFull
	}

	// Default: full for small, map for large
	if lines > 300 {
		return ReadModeMap
	}
	return ReadModeFull
}

// extractMap creates a structural overview with line numbers.
func extractMap(filePath, content string) string {
	lines := strings.Split(content, "\n")
	ext := strings.ToLower(filepath.Ext(filePath))

	var sections []string
	sections = append(sections, fmt.Sprintf("# %s (%d lines)", filepath.Base(filePath), len(lines)))
	sections = append(sections, "")

	patterns := getStructurePatterns(ext)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, p := range patterns {
			if p.re.MatchString(trimmed) {
				indent := strings.Repeat("  ", p.level)
				sections = append(sections, fmt.Sprintf("%sL%-4d %s", indent, i+1, trimmed))
				break
			}
		}
	}

	return strings.Join(sections, "\n")
}

type structurePattern struct {
	re    *regexp.Regexp
	level int
}

// getStructurePatterns returns language-specific patterns for structure detection.
func getStructurePatterns(ext string) []structurePattern {
	switch ext {
	case ".go":
		return []structurePattern{
			{regexp.MustCompile(`^package\s`), 0},
			{regexp.MustCompile(`^type\s+\w+\s+(struct|interface)`), 0},
			{regexp.MustCompile(`^func\s`), 1},
			{regexp.MustCompile(`^\s+func\s`), 2},
		}
	case ".py":
		return []structurePattern{
			{regexp.MustCompile(`^class\s`), 0},
			{regexp.MustCompile(`^\s*def\s`), 1},
			{regexp.MustCompile(`^import\s|^from\s`), 0},
		}
	case ".js", ".ts", ".tsx", ".jsx":
		return []structurePattern{
			{regexp.MustCompile(`^(export\s+)?(class|interface)\s`), 0},
			{regexp.MustCompile(`^(export\s+)?(function|const|let|var)\s+\w+`), 1},
			{regexp.MustCompile(`^\s+(async\s+)?\w+\s*\(`), 2},
		}
	case ".rs":
		return []structurePattern{
			{regexp.MustCompile(`^(pub\s+)?(struct|enum|trait|impl)\s`), 0},
			{regexp.MustCompile(`^(pub\s+)?(fn|async fn)\s`), 1},
			{regexp.MustCompile(`^\s+(pub\s+)?(fn|async fn)\s`), 2},
		}
	case ".java", ".kt":
		return []structurePattern{
			{regexp.MustCompile(`^(public|private|protected)?\s*(class|interface|enum)\s`), 0},
			{regexp.MustCompile(`^\s+(public|private|protected)?\s*(static\s+)?\w+\s+\w+\s*\(`), 1},
		}
	case ".c", ".cpp", ".h", ".hpp":
		return []structurePattern{
			{regexp.MustCompile(`^(class|struct|enum|namespace)\s`), 0},
			{regexp.MustCompile(`^\w[\w\s*]+\w+\s*\([^)]*\)\s*\{?$`), 1},
			{regexp.MustCompile(`^#(include|define|ifdef|ifndef)\s`), 0},
		}
	case ".rb":
		return []structurePattern{
			{regexp.MustCompile(`^(class|module)\s`), 0},
			{regexp.MustCompile(`^\s*def\s`), 1},
		}
	case ".md", ".rst":
		return []structurePattern{
			{regexp.MustCompile(`^#{1,2}\s`), 0},
			{regexp.MustCompile(`^#{3,4}\s`), 1},
			{regexp.MustCompile(`^#{5,6}\s`), 2},
		}
	default:
		return []structurePattern{
			{regexp.MustCompile(`^(func|function|def|class|type|struct|interface|impl|pub fn)\s`), 0},
		}
	}
}

// extractSignatures extracts function/method/class signatures without bodies.
func extractSignatures(filePath, content string) string {
	lines := strings.Split(content, "\n")
	ext := strings.ToLower(filepath.Ext(filePath))

	var sigPattern *regexp.Regexp
	switch ext {
	case ".go":
		sigPattern = regexp.MustCompile(`^\s*(func\s|type\s+\w+\s+(struct|interface))`)
	case ".py":
		sigPattern = regexp.MustCompile(`^\s*(class\s|def\s)`)
	case ".js", ".ts", ".tsx", ".jsx":
		sigPattern = regexp.MustCompile(`^\s*(export\s+)?(function|class|interface|const\s+\w+\s*=\s*(async\s+)?\()`)
	case ".rs":
		sigPattern = regexp.MustCompile(`^\s*(pub\s+)?(fn|struct|enum|trait|impl)\s`)
	case ".java", ".kt":
		sigPattern = regexp.MustCompile(`^\s*(public|private|protected)?\s*(class|interface|enum|\w+\s+\w+\s*\()`)
	case ".c", ".cpp", ".h", ".hpp":
		sigPattern = regexp.MustCompile(`^\s*(class|struct|enum|\w[\w\s*]+\w+\s*\([^)]*\))`)
	case ".rb":
		sigPattern = regexp.MustCompile(`^\s*(class|module|def)\s`)
	default:
		sigPattern = regexp.MustCompile(`^\s*(func|function|def|class|type|struct|interface|impl|pub fn)\s`)
	}

	var result []string
	result = append(result, fmt.Sprintf("# Signatures: %s", filepath.Base(filePath)))
	result = append(result, "")

	for i, line := range lines {
		if sigPattern.MatchString(line) {
			result = append(result, fmt.Sprintf("L%-4d %s", i+1, strings.TrimRight(line, "{")))
		}
	}

	return strings.Join(result, "\n")
}

// extractDiff runs git diff on a file and returns only changed hunks.
func extractDiff(filePath string) (string, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("abs path: %w", err)
	}

	dir := filepath.Dir(absPath)
	base := filepath.Base(absPath)

	// Check if git is available and if this directory is in a git worktree.
	// Use a 3-second context deadline to avoid hanging the server.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	checkCmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	checkCmd.Dir = dir
	if err := checkCmd.Run(); err != nil {
		return fmt.Sprintf("# diff mode requires git context for %s\n# This file is not inside a git repository or git is not installed.", base), nil
	}

	// Check if file is untracked in git.
	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--", base)
	statusCmd.Dir = dir
	statusOut, _ := statusCmd.Output()
	if strings.HasPrefix(string(statusOut), "??") {
		// Untracked files have no diff history, so we show an explanation.
		return fmt.Sprintf("# File %s is untracked in git.\n# Use full mode or track the file to see diffs.", base), nil
	}

	// Run git diff HEAD -- <base> to get both staged and unstaged differences.
	diffCmd := exec.CommandContext(ctx, "git", "diff", "HEAD", "--", base)
	diffCmd.Dir = dir
	diffOut, err := diffCmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff failed: %w", err)
	}

	diffStr := string(diffOut)
	if trimmed := strings.TrimSpace(diffStr); trimmed == "" {
		return fmt.Sprintf("# No changes detected in git for %s", base), nil
	}

	return diffStr, nil
}

// extractHighEntropy returns lines with high information density.
// Scores each line based on complexity indicators.
func extractHighEntropy(content string) string {
	lines := strings.Split(content, "\n")
	type scoredLine struct {
		index int
		line  string
		score float64
	}

	var scored []scoredLine
	for i, line := range lines {
		s := entropyScore(line)
		if s > 0 {
			scored = append(scored, scoredLine{index: i, line: line, score: s})
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top 30% or at most 100 lines
	maxLines := len(scored) * 30 / 100
	if maxLines < 10 {
		maxLines = 10
	}
	if maxLines > 100 {
		maxLines = 100
	}
	if maxLines > len(scored) {
		maxLines = len(scored)
	}

	// Re-sort by line number for readability
	top := scored[:maxLines]
	sort.Slice(top, func(i, j int) bool {
		return top[i].index < top[j].index
	})

	var result []string
	result = append(result, "# High-entropy lines (complex logic, decisions, errors)")
	result = append(result, "")
	for _, sl := range top {
		result = append(result, fmt.Sprintf("L%-4d %s", sl.index+1, sl.line))
	}

	return strings.Join(result, "\n")
}

// entropyScore scores a line's information density.
func entropyScore(line string) float64 {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) == 0 {
		return 0
	}

	score := 0.0

	// Control flow
	controlFlow := []string{"if ", "else", "switch", "case ", "for ", "while ", "return ", "break", "continue", "goto "}
	for _, kw := range controlFlow {
		if strings.Contains(trimmed, kw) {
			score += 2.0
		}
	}

	// Error handling
	errorKw := []string{"error", "Error", "err ", "panic", "fatal", "warn", "catch", "except", "throw", "raise"}
	for _, kw := range errorKw {
		if strings.Contains(trimmed, kw) {
			score += 3.0
		}
	}

	// Assignments with computation
	if strings.Contains(trimmed, ":=") || strings.Contains(trimmed, " = ") {
		score += 1.0
	}

	// Function calls (parentheses)
	parens := strings.Count(trimmed, "(")
	if parens > 1 {
		score += float64(parens) * 0.5
	}

	// Conditionals
	if strings.Contains(trimmed, "&&") || strings.Contains(trimmed, "||") {
		score += 2.0
	}

	// Negation
	if strings.Contains(trimmed, "!") || strings.Contains(trimmed, "not ") {
		score += 1.0
	}

	// Long lines suggest complexity
	if len(trimmed) > 80 {
		score += math.Log(float64(len(trimmed))/80.0) * 1.5
	}

	// Comments are low-entropy
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "--") {
		score *= 0.2
	}

	return score
}

// extractLineRanges returns specific line ranges.
// Format: "10:50" or "1:20,80:100"
func extractLineRanges(content, ranges string) (string, error) {
	if ranges == "" {
		return content, nil
	}

	lines := strings.Split(content, "\n")
	var result []string

	parts := strings.Split(ranges, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		var start, end int
		n, err := fmt.Sscanf(part, "%d:%d", &start, &end)
		if err != nil || n != 2 {
			return "", fmt.Errorf("invalid line range %q: expected start:end", part)
		}
		if start < 1 {
			start = 1
		}
		if end > len(lines) {
			end = len(lines)
		}
		if start > end {
			continue
		}
		for i := start - 1; i < end; i++ {
			result = append(result, fmt.Sprintf("L%-4d %s", i+1, lines[i]))
		}
		if len(parts) > 1 {
			result = append(result, "---")
		}
	}

	return strings.Join(result, "\n"), nil
}

// extractTaskRelevant returns lines relevant to a task/query.
// Uses keyword-based scoring from the query.
func extractTaskRelevant(content, query string) string {
	if query == "" {
		return content
	}

	// Extract keywords from query
	keywords := extractKeywords(query)
	if len(keywords) == 0 {
		return content
	}

	lines := strings.Split(content, "\n")
	type scoredLine struct {
		index int
		line  string
		score float64
	}

	var scored []scoredLine
	for i, line := range lines {
		lower := strings.ToLower(line)
		s := 0.0
		for _, kw := range keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				s += 1.0
			}
		}
		if s > 0 {
			// Include context: 2 lines before and after
			scored = append(scored, scoredLine{index: i, line: line, score: s})
		}
	}

	if len(scored) == 0 {
		return "# No lines matched task query: " + query
	}

	// Sort by score
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Build result with context
	maxResults := 50
	if len(scored) < maxResults {
		maxResults = len(scored)
	}

	// Collect line indices + context
	lineSet := make(map[int]bool)
	for _, sl := range scored[:maxResults] {
		for c := sl.index - 2; c <= sl.index+2; c++ {
			if c >= 0 && c < len(lines) {
				lineSet[c] = true
			}
		}
	}

	// Sort indices
	sortedIdx := make([]int, 0, len(lineSet))
	for idx := range lineSet {
		sortedIdx = append(sortedIdx, idx)
	}
	sort.Ints(sortedIdx)

	var result []string
	result = append(result, fmt.Sprintf("# Lines relevant to: %s", query))
	result = append(result, "")

	prevIdx := -2
	for _, idx := range sortedIdx {
		if idx > prevIdx+1 {
			result = append(result, "---")
		}
		result = append(result, fmt.Sprintf("L%-4d %s", idx+1, lines[idx]))
		prevIdx = idx
	}

	return strings.Join(result, "\n")
}

// extractKeywords pulls meaningful words from a query string.
func extractKeywords(query string) []string {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "can": true, "shall": true,
		"i": true, "me": true, "my": true, "we": true, "our": true,
		"you": true, "your": true, "he": true, "she": true, "it": true,
		"they": true, "them": true, "this": true, "that": true, "these": true,
		"those": true, "what": true, "which": true, "who": true, "whom": true,
		"where": true, "when": true, "how": true, "why": true,
		"in": true, "on": true, "at": true, "to": true, "for": true,
		"with": true, "by": true, "from": true, "of": true, "and": true,
		"or": true, "not": true, "but": true, "if": true, "then": true,
		"than": true, "so": true, "as": true, "into": true, "about": true,
		"find": true, "show": true, "get": true, "all": true, "any": true,
	}

	words := strings.Fields(strings.ToLower(query))
	var keywords []string
	for _, w := range words {
		// Strip punctuation
		w = strings.Trim(w, ".,;:!?\"'()[]{}")
		if len(w) > 2 && !stopWords[w] {
			keywords = append(keywords, w)
		}
	}
	return keywords
}

// extractReferences returns imports, exports, and type definitions.
func extractReferences(filePath, content string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	lines := strings.Split(content, "\n")

	var refPatterns []*regexp.Regexp
	switch ext {
	case ".go":
		refPatterns = []*regexp.Regexp{
			regexp.MustCompile(`^\s*(import|package)\s`),
			regexp.MustCompile(`^\s*type\s+\w+\s`),
			regexp.MustCompile(`^\s*var\s+\w+\s`),
			regexp.MustCompile(`^\s*const\s`),
		}
	case ".py":
		refPatterns = []*regexp.Regexp{
			regexp.MustCompile(`^\s*(import|from)\s`),
			regexp.MustCompile(`^\s*class\s`),
			regexp.MustCompile(`^\s*__all__\s*=`),
		}
	case ".js", ".ts", ".tsx", ".jsx":
		refPatterns = []*regexp.Regexp{
			regexp.MustCompile(`^\s*(import|export|require)\s`),
			regexp.MustCompile(`^\s*(type|interface|enum)\s`),
			regexp.MustCompile(`^\s*module\.exports`),
		}
	case ".rs":
		refPatterns = []*regexp.Regexp{
			regexp.MustCompile(`^\s*(use|mod|pub use|extern crate)\s`),
			regexp.MustCompile(`^\s*(pub\s+)?(type|struct|enum|trait)\s`),
		}
	case ".java", ".kt":
		refPatterns = []*regexp.Regexp{
			regexp.MustCompile(`^\s*(import|package)\s`),
			regexp.MustCompile(`^\s*(public|private)?\s*(class|interface|enum)\s`),
		}
	default:
		refPatterns = []*regexp.Regexp{
			regexp.MustCompile(`^\s*(import|from|require|use|include|#include)\s`),
		}
	}

	var result []string
	result = append(result, fmt.Sprintf("# References: %s", filepath.Base(filePath)))
	result = append(result, "")

	inImportBlock := false
	for i, line := range lines {
		matched := false
		for _, re := range refPatterns {
			if re.MatchString(line) {
				matched = true
				break
			}
		}

		// Track multi-line import blocks (Go-style)
		if strings.Contains(line, "import (") {
			inImportBlock = true
			matched = true
		}
		if inImportBlock {
			matched = true
			if strings.Contains(line, ")") {
				inImportBlock = false
			}
		}

		if matched {
			result = append(result, fmt.Sprintf("L%-4d %s", i+1, line))
		}
	}

	return strings.Join(result, "\n")
}

// extractAggressive strips all comments, blank lines, imports, and non-essential content.
func extractAggressive(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var result []string
	inBlockComment := false
	inImportBlock := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip blank lines
		if trimmed == "" {
			continue
		}

		// Skip block comments
		if strings.HasPrefix(trimmed, "/*") {
			inBlockComment = true
		}
		if inBlockComment {
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
			}
			continue
		}

		// Skip single-line comments
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Skip doc comments
		if strings.HasPrefix(trimmed, "///") || strings.HasPrefix(trimmed, "/**") {
			continue
		}

		// Skip package declarations (Go-specific boilerplate)
		if strings.HasPrefix(trimmed, "package ") {
			continue
		}

		// Skip multi-line import blocks (Go-specific)
		if trimmed == "import (" {
			inImportBlock = true
			continue
		}
		if inImportBlock {
			if trimmed == ")" {
				inImportBlock = false
			}
			continue
		}

		// Skip single-line imports (Go, Python, JS/TS)
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "import \"") || strings.HasPrefix(trimmed, "from ") {
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}
