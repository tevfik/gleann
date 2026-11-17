package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
)

// Helpers shared by the Kùzu-path golden test (treesitter build) and the
// SQLite-path tests (every build).

type toolHandler = func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error)

// callToolResult calls a handler and returns its result, error results included.
func callToolResult(t *testing.T, h toolHandler, args map[string]interface{}) *mcpsdk.CallToolResult {
	t.Helper()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	return res
}

func callTool(t *testing.T, h func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error), args map[string]interface{}) string {
	t.Helper()
	var req mcpsdk.CallToolRequest
	req.Params.Arguments = args
	res, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	return res.Content[0].(mcpsdk.TextContent).Text
}

// canonicalNavigate sorts the caller/callee lines of every match and the
// matches themselves (Kùzu returns both in engine order), renumbering them.
func canonicalNavigate(text string) string {
	blocks := strings.Split(strings.TrimRight(text, "\n"), "\n\n")
	if len(blocks) == 0 {
		return text
	}
	title := blocks[0]
	var matches []string
	for _, b := range blocks[1:] {
		lines := strings.Split(b, "\n")
		var out []string
		var run []string
		flush := func() {
			sort.Strings(run)
			out = append(out, run...)
			run = nil
		}
		for _, ln := range lines {
			if strings.HasPrefix(ln, "    ") {
				run = append(run, ln)
				continue
			}
			flush()
			out = append(out, ln)
		}
		flush()
		matches = append(matches, strings.Join(out, "\n"))
	}
	strip := func(b string) string {
		_, rest, _ := strings.Cut(b, ". ")
		return rest
	}
	sort.Slice(matches, func(i, j int) bool { return strip(matches[i]) < strip(matches[j]) })
	for i, m := range matches {
		matches[i] = fmt.Sprintf("── %d. %s", i+1, strip(m))
	}
	return strings.Join(append([]string{title}, matches...), "\n\n") + "\n"
}
