package mcp

import (
	"context"
	"encoding/json"
	"fmt"

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
		Description: "Read a file with mode-aware compression. Supports 10 modes: " +
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
					"description": "Absolute or relative path to the file to read.",
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

	content, err := gleann.ReadFileWithMode(path, opts)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("read error: %v", err)), nil
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
		Description: "Report cumulative token savings from shell compression and mode-aware reads. " +
			"Shows raw vs compressed token counts and the overall compression ratio.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"reset": map[string]interface{}{
					"type":        "boolean",
					"description": "If true, reset the gain tracker to zero.",
				},
			},
		},
	}
}

// sessionGain tracks cumulative token savings within a session.
var sessionGain gleann.TokenGain

func (s *Server) handleGain(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if ok {
		if reset, ok := args["reset"].(bool); ok && reset {
			sessionGain = gleann.TokenGain{}
			return mcp.NewToolResultText(`{"status": "reset", "message": "Token gain tracker reset to zero."}`), nil
		}
	}

	data, _ := json.MarshalIndent(sessionGain, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}
