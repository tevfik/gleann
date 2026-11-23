package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/gleann"
)

// ── gleann_shell ──────────────────────────────────────────────────────────

func (s *Server) buildShellTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_shell",
		Description: "Compress shell command output using 95+ built-in patterns. " +
			"Supports git, go, npm, cargo, docker, kubectl, terraform, make, pip, and more. " +
			"Provides token savings of 60-95% on noisy CLI output. " +
			"Returns compressed output plus compression statistics.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"command": map[string]interface{}{
					"type":        "string",
					"description": "The shell command that was run (e.g., 'git log -n 20'). Used to select compression patterns.",
				},
				"output": map[string]interface{}{
					"type":        "string",
					"description": "The raw output from the shell command to compress.",
				},
			},
			Required: []string{"command", "output"},
		},
	}
}

func (s *Server) handleShell(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}

	command, _ := args["command"].(string)
	output, _ := args["output"].(string)

	if command == "" {
		return mcp.NewToolResultError("missing required parameter: command"), nil
	}

	compressor := gleann.NewShellCompressor()
	result := compressor.Compress(command, output)
	s.recordGain(result.RawTokens, result.CompTokens)

	response := map[string]interface{}{
		"output":            result.Output,
		"raw_bytes":         result.RawBytes,
		"compressed_bytes":  result.CompBytes,
		"compression_ratio": fmt.Sprintf("%.1f%%", result.Ratio*100),
		"raw_tokens_est":    result.RawTokens,
		"comp_tokens_est":   result.CompTokens,
		"tokens_saved":      result.RawTokens - result.CompTokens,
		"pattern_used":      result.PatternUsed,
	}

	data, _ := json.MarshalIndent(response, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// ── gleann_read ───────────────────────────────────────────────────────────

func (s *Server) buildReadTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_read",
		Description: "Read a file with mode-aware compression. PREFER this over cat/sed or dumping whole files: pick map/signatures first, then lines/task for the relevant part. Supports 10 modes: " +
			"full (entire file), map (structural overview), signatures (function/class sigs only), " +
			"diff (git-modified hunks), entropy (high-information lines), lines (specific ranges like '10:50'), " +
			"task (lines relevant to a query), reference (imports/exports/types), " +
			"aggressive (strip comments+blanks), auto (picks best mode by file size/type). " +
			"Typically saves 60-90% tokens vs reading an entire file.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"path": map[string]interface{}{
					"type":        "string",
					"description": "Path to a file inside the current project or an indexed source directory (absolute, or relative to either).",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"description": "Read mode: full, map, signatures, diff, entropy, lines, task, reference, aggressive, auto",
					"default":     "auto",
					"enum":        []string{"full", "map", "signatures", "diff", "entropy", "lines", "task", "reference", "aggressive", "auto"},
				},
				"lines": map[string]interface{}{
					"type":        "string",
					"description": "Line range(s) for mode=lines, e.g. '10:50' or '1:20,80:100'",
				},
				"query": map[string]interface{}{
					"type":        "string",
					"description": "Task description for mode=task, e.g. 'error handling in the search function'",
				},
				"max_lines": map[string]interface{}{
					"type":        "integer",
					"description": "Maximum number of lines to return (0 = unlimited)",
				},
			},
			Required: []string{"path"},
		},
	}
}

func (s *Server) handleRead(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments"), nil
	}

	path, _ := args["path"].(string)
	if path == "" {
		return mcp.NewToolResultError("missing required parameter: path"), nil
	}

	modeStr, _ := args["mode"].(string)
	if modeStr == "" {
		modeStr = "auto"
	}

	lineRanges, _ := args["lines"].(string)
	query, _ := args["query"].(string)
	maxLines := 0
	if ml, ok := args["max_lines"].(float64); ok {
		maxLines = int(ml)
	}

	mode := gleann.ReadMode(modeStr)

	opts := gleann.ReadModeOptions{
		Mode:       mode,
		LineRanges: lineRanges,
		TaskQuery:  query,
		MaxLines:   maxLines,
	}

	// The path is client input: only files inside an indexed source directory
	// are readable, and a refused path looks the same as a missing one.
	var content string
	err := errors.New("file not found in any indexed source directory")
	for _, root := range s.readRoots() {
		full, ok := containedSourcePath(root, path)
		if !ok {
			continue
		}
		if c, rErr := gleann.ReadFileWithMode(full, opts); rErr == nil {
			content, err, path = c, nil, full
			break
		}
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read error: %v", err)), nil
	}

	// Record the gain: whole-file tokens vs the mode-filtered payload (the
	// metadata header below is not counted). The baseline needs the
	// unmodified file; if it cannot be re-read, the event is not measurable
	// and is skipped. On success path holds the resolved full path.
	if raw, rErr := os.ReadFile(path); rErr == nil {
		s.recordGain(gleann.EstimateTokens(string(raw)), gleann.EstimateTokens(content))
	}

	rawTokens := gleann.EstimateTokens(content)

	// Wrap with metadata header
	header := fmt.Sprintf("# %s (mode=%s, ~%d tokens)\n", path, modeStr, rawTokens)

	return mcp.NewToolResultText(header + content), nil
}

// ── gleann_gain ───────────────────────────────────────────────────────────

func (s *Server) buildGainTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_gain",
		Description: "Report cumulative estimated token savings recorded by this server's gleann_shell and gleann_read calls " +
			"since the server started (or the last reset). Gain per call = estimated tokens of the raw input " +
			"(the shell output, or the whole file for reads) minus estimated tokens of the compressed payload returned " +
			"(the compressed output, or the mode-filtered file content); metadata wrappers (JSON stats, read header) " +
			"are not counted. Estimates use the ~4 characters per token heuristic. Shows raw vs compressed token counts, " +
			"the call count and the overall savings ratio. The counter lives in this MCP server process only; " +
			"it is not persisted across restarts.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"reset": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, reset this server's gain tracker to zero.",
				},
			},
		},
	}
}

// recordGain adds one compression event (raw vs delivered payload tokens)
// to the server's cumulative gain tracker.
func (s *Server) recordGain(rawTokens, compTokens int) {
	s.gainMu.Lock()
	defer s.gainMu.Unlock()
	s.gain.Add(rawTokens, compTokens)
}

func (s *Server) handleGain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if ok {
		if reset, ok := args["reset"].(bool); ok && reset {
			s.gainMu.Lock()
			s.gain = gleann.TokenGain{}
			s.gainMu.Unlock()
			return mcp.NewToolResultText(`{"status": "reset", "message": "Token gain tracker reset to zero."}`), nil
		}
	}

	s.gainMu.Lock()
	gain := s.gain
	s.gainMu.Unlock()
	data, _ := json.MarshalIndent(gain, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// readRoots lists the directories gleann_read may read from: the working
// directory of the MCP process (the project the agent runs in) first, so
// relative paths resolve as before, then the resolved index's source
// directory, then every MCP-exposed index. The working directory is skipped
// when it is a file-system root or the home directory itself, where it would
// expose everything below it.
func (s *Server) readRoots() []string {
	var roots []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			roots = append(roots, dir)
		}
	}
	if cwd, err := os.Getwd(); err == nil && filepath.Dir(cwd) != cwd {
		home, _ := os.UserHomeDir()
		if home == "" || filepath.Clean(cwd) != filepath.Clean(home) {
			add(cwd)
		}
	}
	if idxName := s.resolveIndexName(); idxName != "" {
		if meta, err := gleann.GetIndexMeta(s.config.IndexDir, idxName); err == nil {
			add(meta.SourceDir)
		}
	}
	if indexes, err := gleann.ListIndexes(s.config.IndexDir); err == nil {
		for _, idx := range indexes {
			if idx.IsMCPExposed() {
				add(idx.SourceDir)
			}
		}
	}
	return roots
}
