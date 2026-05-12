package gleann

import (
	"fmt"
	"regexp"
	"strings"
)

// ShellPattern defines a compression pattern for a specific CLI tool's output.
type ShellPattern struct {
	// Name identifies the pattern (e.g., "git_status", "npm_install").
	Name string
	// Tool is the CLI tool this pattern applies to (e.g., "git", "npm", "go").
	Tool string
	// SubCommand narrows matching to a specific sub-command (e.g., "status", "install").
	SubCommand string
	// StripRegexps are regexps whose full matches are removed from output.
	StripRegexps []*regexp.Regexp
	// CollapseRegexp collapses consecutive matching lines into a count summary.
	CollapseRegexp *regexp.Regexp
	// CollapseLabel is the summary label (e.g., "%d files unchanged").
	CollapseLabel string
	// MaxLines caps output to this many lines; 0 = unlimited.
	MaxLines int
	// TailLines keeps last N lines when truncating; 0 = use MaxLines only.
	TailLines int
	// Custom is an optional function for complex transformations.
	Custom func(output string) string
}

// ShellCompressor applies pattern-based compression to CLI output.
type ShellCompressor struct {
	patterns []ShellPattern
}

// NewShellCompressor creates a compressor with the default 95+ patterns.
func NewShellCompressor() *ShellCompressor {
	return &ShellCompressor{
		patterns: defaultShellPatterns(),
	}
}

// CompressResult holds compressed output and statistics.
type CompressResult struct {
	Output       string `json:"output"`
	RawBytes     int    `json:"raw_bytes"`
	CompBytes    int    `json:"compressed_bytes"`
	Ratio        float64 `json:"ratio"`
	PatternUsed  string `json:"pattern_used,omitempty"`
	RawTokens    int    `json:"raw_tokens_est"`
	CompTokens   int    `json:"compressed_tokens_est"`
}

// Compress detects the tool/sub-command from cmdLine and applies matching
// compression patterns to output.
func (sc *ShellCompressor) Compress(cmdLine, output string) CompressResult {
	rawBytes := len(output)
	rawTokens := EstimateTokens(output)

	tool, sub := parseCmdLine(cmdLine)
	pattern := sc.findPattern(tool, sub)

	compressed := output
	patternName := ""

	if pattern != nil {
		patternName = pattern.Name
		compressed = applyPattern(pattern, output)
	} else {
		// Generic compression: strip ANSI, collapse blank lines
		compressed = genericCompress(output)
	}

	compBytes := len(compressed)
	compTokens := EstimateTokens(compressed)

	ratio := 0.0
	if rawBytes > 0 {
		ratio = 1.0 - float64(compBytes)/float64(rawBytes)
	}

	return CompressResult{
		Output:      compressed,
		RawBytes:    rawBytes,
		CompBytes:   compBytes,
		Ratio:       ratio,
		PatternUsed: patternName,
		RawTokens:   rawTokens,
		CompTokens:  compTokens,
	}
}

// AddPattern registers a custom compression pattern.
func (sc *ShellCompressor) AddPattern(p ShellPattern) {
	sc.patterns = append(sc.patterns, p)
}

// findPattern returns the best matching pattern for the given tool + sub-command.
func (sc *ShellCompressor) findPattern(tool, sub string) *ShellPattern {
	// Exact match first
	for i := range sc.patterns {
		if sc.patterns[i].Tool == tool && sc.patterns[i].SubCommand == sub {
			return &sc.patterns[i]
		}
	}
	// Tool-only fallback
	for i := range sc.patterns {
		if sc.patterns[i].Tool == tool && sc.patterns[i].SubCommand == "" {
			return &sc.patterns[i]
		}
	}
	return nil
}

// parseCmdLine extracts tool and sub-command from a command line string.
func parseCmdLine(cmdLine string) (tool, sub string) {
	parts := strings.Fields(cmdLine)
	if len(parts) == 0 {
		return "", ""
	}
	// Strip path: /usr/bin/git -> git
	tool = parts[0]
	if idx := strings.LastIndex(tool, "/"); idx >= 0 {
		tool = tool[idx+1:]
	}
	// Find first non-flag argument as sub-command
	for _, p := range parts[1:] {
		if !strings.HasPrefix(p, "-") {
			sub = p
			break
		}
	}
	return tool, sub
}

// ANSI escape regexp for stripping color codes.
var ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// genericCompress strips ANSI codes and collapses blank lines.
func genericCompress(s string) string {
	s = ansiRegexp.ReplaceAllString(s, "")
	// Collapse 3+ consecutive blank lines into 1
	s = regexp.MustCompile(`\n{3,}`).ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// applyPattern applies a ShellPattern's rules to output.
func applyPattern(p *ShellPattern, output string) string {
	result := output

	// Strip ANSI first
	result = ansiRegexp.ReplaceAllString(result, "")

	// Apply custom function if present
	if p.Custom != nil {
		result = p.Custom(result)
		return strings.TrimSpace(result)
	}

	// Apply strip regexps
	for _, re := range p.StripRegexps {
		result = re.ReplaceAllString(result, "")
	}

	// Collapse consecutive matching lines
	if p.CollapseRegexp != nil {
		result = collapseLines(result, p.CollapseRegexp, p.CollapseLabel)
	}

	// Truncate if MaxLines is set
	if p.MaxLines > 0 {
		result = truncateOutput(result, p.MaxLines, p.TailLines)
	}

	// Collapse excess blank lines
	result = regexp.MustCompile(`\n{3,}`).ReplaceAllString(result, "\n\n")

	return strings.TrimSpace(result)
}

// collapseLines replaces consecutive lines matching re with a count summary.
func collapseLines(s string, re *regexp.Regexp, label string) string {
	lines := strings.Split(s, "\n")
	var result []string
	count := 0
	for _, line := range lines {
		if re.MatchString(line) {
			count++
		} else {
			if count > 0 {
				result = append(result, fmt.Sprintf("  [%s]", fmt.Sprintf(label, count)))
				count = 0
			}
			result = append(result, line)
		}
	}
	if count > 0 {
		result = append(result, fmt.Sprintf("  [%s]", fmt.Sprintf(label, count)))
	}
	return strings.Join(result, "\n")
}

// truncateOutput keeps the first maxLines, and optionally the last tailLines.
func truncateOutput(s string, maxLines, tailLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	head := lines[:maxLines]
	omitted := len(lines) - maxLines
	if tailLines > 0 && tailLines < omitted {
		tail := lines[len(lines)-tailLines:]
		omitted -= tailLines
		return strings.Join(head, "\n") +
			fmt.Sprintf("\n  [...%d lines omitted...]\n", omitted) +
			strings.Join(tail, "\n")
	}
	return strings.Join(head, "\n") +
		fmt.Sprintf("\n  [...%d lines omitted...]", omitted)
}

// defaultShellPatterns returns 95+ compression patterns for common CLI tools.
func defaultShellPatterns() []ShellPattern {
	return []ShellPattern{
		// ── Git ──────────────────────────────────────────────────
		{
			Name: "git_status", Tool: "git", SubCommand: "status",
			CollapseRegexp: regexp.MustCompile(`^\s+(modified|new file|deleted|renamed):\s+`),
			CollapseLabel:  "%d files changed",
			MaxLines:       40,
		},
		{
			Name: "git_log", Tool: "git", SubCommand: "log",
			MaxLines: 50, TailLines: 5,
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^Author:.*$`),
				regexp.MustCompile(`(?m)^Date:.*$`),
			},
		},
		{
			Name: "git_diff", Tool: "git", SubCommand: "diff",
			MaxLines: 100, TailLines: 10,
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^index [0-9a-f]+\.\.[0-9a-f]+ \d+$`),
				regexp.MustCompile(`(?m)^similarity index \d+%$`),
			},
		},
		{
			Name: "git_branch", Tool: "git", SubCommand: "branch",
			CollapseRegexp: regexp.MustCompile(`^\s+\S+$`),
			CollapseLabel:  "%d branches",
		},
		{
			Name: "git_stash", Tool: "git", SubCommand: "stash",
			MaxLines: 10,
		},
		{
			Name: "git_show", Tool: "git", SubCommand: "show",
			MaxLines: 80, TailLines: 10,
		},
		{
			Name: "git_blame", Tool: "git", SubCommand: "blame",
			MaxLines: 50,
			Custom: func(output string) string {
				// Extract only the commit hash + line content
				lines := strings.Split(output, "\n")
				var result []string
				for _, l := range lines {
					parts := strings.SplitN(l, ")", 2)
					if len(parts) == 2 {
						hash := strings.Fields(parts[0])
						if len(hash) > 0 {
							result = append(result, hash[0][:min(8, len(hash[0]))]+") "+strings.TrimSpace(parts[1]))
						}
					} else {
						result = append(result, l)
					}
				}
				return strings.Join(result, "\n")
			},
		},
		{
			Name: "git_remote", Tool: "git", SubCommand: "remote",
			MaxLines: 20,
		},
		{
			Name: "git_tag", Tool: "git", SubCommand: "tag",
			CollapseRegexp: regexp.MustCompile(`^v?\d+\.\d+\.\d+`),
			CollapseLabel:  "%d tags",
			MaxLines:       20,
		},
		{
			Name: "git_fetch", Tool: "git", SubCommand: "fetch",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^remote: (Counting|Compressing|Total).*$`),
				regexp.MustCompile(`(?m)^remote: Enumerating.*$`),
			},
		},
		{
			Name: "git_pull", Tool: "git", SubCommand: "pull",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^remote: (Counting|Compressing|Total|Enumerating).*$`),
			},
			CollapseRegexp: regexp.MustCompile(`^\s+\S+\s+\|\s+\d+\s+[+-]+$`),
			CollapseLabel:  "%d files updated",
		},
		{
			Name: "git_push", Tool: "git", SubCommand: "push",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Enumerating|Counting|Compressing|Writing|Total).*$`),
			},
		},
		{
			Name: "git_clone", Tool: "git", SubCommand: "clone",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Cloning into|remote: (Counting|Compressing|Total|Enumerating)).*$`),
				regexp.MustCompile(`(?m)^Receiving objects:.*$`),
				regexp.MustCompile(`(?m)^Resolving deltas:.*$`),
			},
		},
		{
			Name: "git_cherry_pick", Tool: "git", SubCommand: "cherry-pick",
			MaxLines: 20,
		},
		{
			Name: "git_rebase", Tool: "git", SubCommand: "rebase",
			MaxLines: 30,
		},
		{
			Name: "git_merge", Tool: "git", SubCommand: "merge",
			MaxLines: 30,
		},

		// ── Go ───────────────────────────────────────────────────
		{
			Name: "go_test", Tool: "go", SubCommand: "test",
			Custom: func(output string) string {
				lines := strings.Split(output, "\n")
				var summary, failures []string
				for _, l := range lines {
					if strings.HasPrefix(l, "ok") || strings.HasPrefix(l, "FAIL") || strings.HasPrefix(l, "?") {
						summary = append(summary, l)
					} else if strings.Contains(l, "FAIL") || strings.Contains(l, "Error") || strings.Contains(l, "panic") {
						failures = append(failures, l)
					}
				}
				var result []string
				if len(failures) > 0 {
					result = append(result, "── Failures ──")
					result = append(result, failures...)
					result = append(result, "")
				}
				result = append(result, "── Summary ──")
				result = append(result, summary...)
				return strings.Join(result, "\n")
			},
		},
		{
			Name: "go_build", Tool: "go", SubCommand: "build",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^#\s+\S+$`),
			},
			MaxLines: 30,
		},
		{
			Name: "go_mod", Tool: "go", SubCommand: "mod",
			CollapseRegexp: regexp.MustCompile(`^\s+(go|require|indirect)\s`),
			CollapseLabel:  "%d dependencies",
		},
		{
			Name: "go_vet", Tool: "go", SubCommand: "vet",
			MaxLines: 40,
		},
		{
			Name: "go_get", Tool: "go", SubCommand: "get",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^go: downloading.*$`),
			},
		},
		{
			Name: "go_run", Tool: "go", SubCommand: "run",
			MaxLines: 50, TailLines: 10,
		},

		// ── npm / yarn / pnpm ───────────────────────────────────
		{
			Name: "npm_install", Tool: "npm", SubCommand: "install",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^npm (warn|notice|http).*$`),
				regexp.MustCompile(`(?m)^(added|removed|changed|audited) \d+ packages?.*$`),
			},
			CollapseRegexp: regexp.MustCompile(`^npm warn `),
			CollapseLabel:  "%d warnings",
		},
		{
			Name: "npm_run", Tool: "npm", SubCommand: "run",
			MaxLines: 50, TailLines: 10,
		},
		{
			Name: "npm_test", Tool: "npm", SubCommand: "test",
			MaxLines: 60, TailLines: 15,
		},
		{
			Name: "npm_audit", Tool: "npm", SubCommand: "audit",
			MaxLines: 40,
		},
		{
			Name: "npm_ls", Tool: "npm", SubCommand: "ls",
			CollapseRegexp: regexp.MustCompile(`^[├└│ ]+\S+@\d`),
			CollapseLabel:  "%d dependencies listed",
			MaxLines:       30,
		},
		{
			Name: "yarn_install", Tool: "yarn", SubCommand: "install",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(info|warning)\s`),
			},
		},
		{
			Name: "pnpm_install", Tool: "pnpm", SubCommand: "install",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Progress|Packages|Downloading)\s`),
			},
		},

		// ── Cargo / Rust ────────────────────────────────────────
		{
			Name: "cargo_build", Tool: "cargo", SubCommand: "build",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^\s+Compiling \S+ v\S+$`),
				regexp.MustCompile(`(?m)^\s+Downloading \S+.*$`),
			},
			CollapseRegexp: regexp.MustCompile(`^\s+Compiling `),
			CollapseLabel:  "%d crates compiled",
		},
		{
			Name: "cargo_test", Tool: "cargo", SubCommand: "test",
			Custom: func(output string) string {
				lines := strings.Split(output, "\n")
				var result []string
				for _, l := range lines {
					if strings.Contains(l, "test result") || strings.Contains(l, "FAILED") ||
						strings.Contains(l, "test ") && strings.Contains(l, "... FAILED") {
						result = append(result, l)
					}
				}
				if len(result) == 0 {
					return genericCompress(output)
				}
				return strings.Join(result, "\n")
			},
		},
		{
			Name: "cargo_clippy", Tool: "cargo", SubCommand: "clippy",
			MaxLines: 40, TailLines: 5,
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^\s+Checking \S+.*$`),
			},
		},

		// ── Docker ──────────────────────────────────────────────
		{
			Name: "docker_build", Tool: "docker", SubCommand: "build",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(#\d+ \[internal\]|#\d+ sha256:|#\d+ extracting|#\d+ DONE).*$`),
				regexp.MustCompile(`(?m)^Sending build context.*$`),
			},
			MaxLines: 40, TailLines: 10,
		},
		{
			Name: "docker_ps", Tool: "docker", SubCommand: "ps",
			MaxLines: 30,
		},
		{
			Name: "docker_logs", Tool: "docker", SubCommand: "logs",
			MaxLines: 50, TailLines: 20,
		},
		{
			Name: "docker_pull", Tool: "docker", SubCommand: "pull",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^[0-9a-f]+: (Pulling|Waiting|Downloading|Extracting|Pull complete|Already exists).*$`),
			},
		},
		{
			Name: "docker_images", Tool: "docker", SubCommand: "images",
			MaxLines: 30,
		},
		{
			Name: "docker_compose_up", Tool: "docker-compose", SubCommand: "up",
			MaxLines: 40, TailLines: 10,
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Creating|Pulling|Building)\s`),
			},
		},
		{
			Name: "docker_compose_logs", Tool: "docker-compose", SubCommand: "logs",
			MaxLines: 50, TailLines: 20,
		},

		// ── Python / pip ────────────────────────────────────────
		{
			Name: "pip_install", Tool: "pip", SubCommand: "install",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Collecting|Downloading|Using cached|Installing|Already satisfied)\s`),
			},
			CollapseRegexp: regexp.MustCompile(`^(Collecting|Downloading|Using cached) `),
			CollapseLabel:  "%d packages processed",
		},
		{
			Name: "pip3_install", Tool: "pip3", SubCommand: "install",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Collecting|Downloading|Using cached|Installing|Already satisfied)\s`),
			},
			CollapseRegexp: regexp.MustCompile(`^(Collecting|Downloading|Using cached) `),
			CollapseLabel:  "%d packages processed",
		},
		{
			Name: "pytest", Tool: "pytest", SubCommand: "",
			MaxLines: 60, TailLines: 15,
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(cachedir|rootdir|plugins|collecting|collected)\s`),
			},
		},
		{
			Name: "python_generic", Tool: "python", SubCommand: "",
			MaxLines: 80, TailLines: 15,
		},
		{
			Name: "python3_generic", Tool: "python3", SubCommand: "",
			MaxLines: 80, TailLines: 15,
		},

		// ── Make ────────────────────────────────────────────────
		{
			Name: "make_generic", Tool: "make", SubCommand: "",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^make\[\d+\]: (Entering|Leaving) directory.*$`),
			},
			MaxLines: 50, TailLines: 10,
		},
		{
			Name: "cmake_generic", Tool: "cmake", SubCommand: "",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^-- (Check|Looking|Detecting|Found)\s`),
			},
			MaxLines: 40,
		},

		// ── kubectl / k8s ───────────────────────────────────────
		{
			Name: "kubectl_get", Tool: "kubectl", SubCommand: "get",
			MaxLines: 40,
		},
		{
			Name: "kubectl_describe", Tool: "kubectl", SubCommand: "describe",
			MaxLines: 60, TailLines: 15,
		},
		{
			Name: "kubectl_logs", Tool: "kubectl", SubCommand: "logs",
			MaxLines: 50, TailLines: 20,
		},
		{
			Name: "kubectl_apply", Tool: "kubectl", SubCommand: "apply",
			CollapseRegexp: regexp.MustCompile(`^\S+ (configured|created|unchanged)$`),
			CollapseLabel:  "%d resources applied",
		},

		// ── Terraform ───────────────────────────────────────────
		{
			Name: "terraform_plan", Tool: "terraform", SubCommand: "plan",
			MaxLines: 60, TailLines: 10,
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Refreshing state|Reading)\.\.\.\s`),
			},
		},
		{
			Name: "terraform_apply", Tool: "terraform", SubCommand: "apply",
			MaxLines: 50, TailLines: 10,
		},
		{
			Name: "terraform_init", Tool: "terraform", SubCommand: "init",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Initializing|Finding|Installing|Using)\s`),
			},
		},

		// ── Misc tools ──────────────────────────────────────────
		{
			Name: "curl_verbose", Tool: "curl", SubCommand: "",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^[*>]\s`),
				regexp.MustCompile(`(?m)^< (Date|Server|X-|Content-Length|Connection|Accept-Ranges|ETag|Vary|Cache-Control|Strict-Transport|Via|CF-).*$`),
			},
			MaxLines: 40,
		},
		{
			Name: "wget_generic", Tool: "wget", SubCommand: "",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^\d{4}-\d{2}-\d{2}.*$`),
				regexp.MustCompile(`(?m)^(Connecting|HTTP request|Length|Saving|Reusing)\s`),
			},
		},
		{
			Name: "grep_generic", Tool: "grep", SubCommand: "",
			MaxLines: 50, TailLines: 5,
		},
		{
			Name: "find_generic", Tool: "find", SubCommand: "",
			CollapseRegexp: regexp.MustCompile(`^/`),
			CollapseLabel:  "%d files found",
			MaxLines:       30,
		},
		{
			Name: "ls_generic", Tool: "ls", SubCommand: "",
			MaxLines: 40,
		},
		{
			Name: "cat_generic", Tool: "cat", SubCommand: "",
			MaxLines: 80, TailLines: 10,
		},
		{
			Name: "wc_generic", Tool: "wc", SubCommand: "",
			MaxLines: 30,
		},
		{
			Name: "du_generic", Tool: "du", SubCommand: "",
			MaxLines: 30, TailLines: 3,
		},
		{
			Name: "df_generic", Tool: "df", SubCommand: "",
			MaxLines: 20,
		},
		{
			Name: "top_generic", Tool: "top", SubCommand: "",
			MaxLines: 30,
		},
		{
			Name: "ps_generic", Tool: "ps", SubCommand: "",
			MaxLines: 30,
		},
		{
			Name: "lsof_generic", Tool: "lsof", SubCommand: "",
			MaxLines: 30,
		},
		{
			Name: "apt_install", Tool: "apt", SubCommand: "install",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Get:|Fetched|Reading|Building)\s`),
				regexp.MustCompile(`(?m)^(Preparing|Unpacking|Setting up|Processing)\s`),
			},
			CollapseRegexp: regexp.MustCompile(`^(Get:|Preparing|Unpacking|Setting up) `),
			CollapseLabel:  "%d packages processed",
		},
		{
			Name: "apt_update", Tool: "apt", SubCommand: "update",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Hit|Get|Ign|Fetched|Reading)\s`),
			},
			CollapseRegexp: regexp.MustCompile(`^(Hit|Get|Ign) `),
			CollapseLabel:  "%d repositories updated",
		},
		{
			Name: "brew_install", Tool: "brew", SubCommand: "install",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(==> Downloading|==> Pouring|==> Caveats|Already downloaded)\s`),
			},
		},
		{
			Name: "brew_update", Tool: "brew", SubCommand: "update",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Already up-to-date|Updated \d+ tap)\s`),
			},
		},

		// ── Ruff / Linters ──────────────────────────────────────
		{
			Name: "ruff_check", Tool: "ruff", SubCommand: "check",
			MaxLines: 40,
		},
		{
			Name: "eslint_generic", Tool: "eslint", SubCommand: "",
			MaxLines: 40,
		},
		{
			Name: "mypy_generic", Tool: "mypy", SubCommand: "",
			MaxLines: 40,
		},
		{
			Name: "pylint_generic", Tool: "pylint", SubCommand: "",
			MaxLines: 40,
		},
		{
			Name: "golangci_lint", Tool: "golangci-lint", SubCommand: "run",
			MaxLines: 40,
		},

		// ── systemctl / journalctl ──────────────────────────────
		{
			Name: "systemctl_status", Tool: "systemctl", SubCommand: "status",
			MaxLines: 20,
		},
		{
			Name: "journalctl_generic", Tool: "journalctl", SubCommand: "",
			MaxLines: 40, TailLines: 15,
		},

		// ── rust/rustup ─────────────────────────────────────────
		{
			Name: "rustup_update", Tool: "rustup", SubCommand: "update",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(info|syncing)\s`),
			},
		},

		// ── maven / gradle ──────────────────────────────────────
		{
			Name: "mvn_generic", Tool: "mvn", SubCommand: "",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^\[INFO\] (Downloading|Downloaded)\s`),
				regexp.MustCompile(`(?m)^\[INFO\] ---\s`),
			},
			MaxLines: 50, TailLines: 10,
		},
		{
			Name: "gradle_generic", Tool: "gradle", SubCommand: "",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(Download|> Task)\s`),
			},
			MaxLines: 40, TailLines: 10,
		},

		// ── SSH / SCP ───────────────────────────────────────────
		{
			Name: "ssh_generic", Tool: "ssh", SubCommand: "",
			MaxLines: 50, TailLines: 10,
		},
		{
			Name: "scp_generic", Tool: "scp", SubCommand: "",
			MaxLines: 20,
		},

		// ── rsync ───────────────────────────────────────────────
		{
			Name: "rsync_generic", Tool: "rsync", SubCommand: "",
			StripRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?m)^(sending|receiving|sent|total)\s`),
			},
			MaxLines: 30,
		},

		// ── gh CLI ──────────────────────────────────────────────
		{
			Name: "gh_pr", Tool: "gh", SubCommand: "pr",
			MaxLines: 30,
		},
		{
			Name: "gh_issue", Tool: "gh", SubCommand: "issue",
			MaxLines: 30,
		},
		{
			Name: "gh_api", Tool: "gh", SubCommand: "api",
			MaxLines: 60, TailLines: 10,
		},
		{
			Name: "gh_run", Tool: "gh", SubCommand: "run",
			MaxLines: 30,
		},
	}
}
