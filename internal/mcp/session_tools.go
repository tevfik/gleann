package mcp

// Session tracking tools:
//
//   gleann_session_start  — begin a named work session; all subsequent
//                           gleann_search_ids / gleann_search / gleann_ask
//                           calls are automatically logged to BBolt memory
//                           under that session's scope.
//
//   gleann_session_end    — end the active session and write a summary block.
//
//   gleann_session_status — show the currently active session and its log count.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/tevfik/gleann/pkg/memory"
)

// activeSession records the currently running work session.
type activeSession struct {
	mu        sync.Mutex
	name      string
	startedAt time.Time
	logCount  int
}

// serverSession is embedded in Server to hold per-server session state.
// It is initialised lazily on first call to gleann_session_start.
var serverSession activeSession

// ── sessionLog is called by other MCP handlers ────────────────────────────────

// sessionLog records a search or ask event to BBolt if a session is active.
// It is intentionally fire-and-forget (errors are silently dropped) so that
// session tracking never disrupts the primary tool call.
func (s *Server) sessionLog(action, index, query string, resultCount int) {
	serverSession.mu.Lock()
	name := serverSession.name
	serverSession.logCount++
	count := serverSession.logCount
	serverSession.mu.Unlock()

	if name == "" {
		return
	}

	mgr, err := s.blockMem.get()
	if err != nil {
		return
	}

	content := fmt.Sprintf("[%s] %s on index=%q query=%q → %d results",
		time.Now().Format("15:04:05"), action, index, query, resultCount)
	_, _ = mgr.AddScopedNote(name, memory.TierShort, fmt.Sprintf("log#%d", count), content)
}

// ── gleann_session_start ──────────────────────────────────────────────────────

func (s *Server) buildSessionStartTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_session_start",
		Description: "Begin a named work session.  All gleann_search, gleann_search_ids and " +
			"gleann_ask calls made while the session is active are automatically logged to " +
			"gleann's persistent memory so the next session can resume context.  " +
			"Sessions are scoped to BBolt and survive server restarts.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "Session name, e.g. \"refactor-auth\" or \"debug-oom-crash\"",
				},
			},
			Required: []string{"name"},
		},
	}
}

func (s *Server) handleSessionStart(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("invalid arguments format"), nil
	}
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return mcp.NewToolResultError("name must not be empty"), nil
	}

	serverSession.mu.Lock()
	prev := serverSession.name
	serverSession.name = name
	serverSession.startedAt = time.Now()
	serverSession.logCount = 0
	serverSession.mu.Unlock()

	mgr, err := s.blockMem.get()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("session storage unavailable: %v", err)), nil
	}

	note := fmt.Sprintf("Session started at %s", time.Now().Format(time.RFC3339))
	_, _ = mgr.AddScopedNote(name, memory.TierMedium, "session_start", note)

	msg := fmt.Sprintf("Session %q started. All search/ask calls will be logged.", name)
	if prev != "" && prev != name {
		msg += fmt.Sprintf(" (previous session %q was open and has been replaced)", prev)
	}
	return mcp.NewToolResultText(msg), nil
}

// ── gleann_session_end ────────────────────────────────────────────────────────

func (s *Server) buildSessionEndTool() mcp.Tool {
	return mcp.Tool{
		Name: "gleann_session_end",
		Description: "End the active work session.  Writes a summary block to persistent " +
			"memory with any notes you provide.  Use gleann_session_status to see what " +
			"was logged this session.",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "Optional 1-3 sentence summary of what was accomplished",
				},
			},
		},
	}
}

func (s *Server) handleSessionEnd(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serverSession.mu.Lock()
	name := serverSession.name
	started := serverSession.startedAt
	count := serverSession.logCount
	serverSession.name = ""
	serverSession.logCount = 0
	serverSession.mu.Unlock()

	if name == "" {
		return mcp.NewToolResultText("No active session."), nil
	}

	args, _ := request.Params.Arguments.(map[string]interface{})
	userSummary, _ := args["summary"].(string)

	dur := time.Since(started).Round(time.Second)
	content := fmt.Sprintf("Session %q ended after %s (%d events logged).", name, dur, count)
	if userSummary != "" {
		content += "\n\nSummary: " + userSummary
	}

	mgr, err := s.blockMem.get()
	if err == nil {
		_, _ = mgr.AddScopedNote(name, memory.TierLong, "session_summary", content)
	}

	return mcp.NewToolResultText(content), nil
}

// ── gleann_session_status ─────────────────────────────────────────────────────

func (s *Server) buildSessionStatusTool() mcp.Tool {
	return mcp.Tool{
		Name:        "gleann_session_status",
		Description: "Show the currently active session name and how many events have been logged.",
		InputSchema: mcp.ToolInputSchema{Type: "object"},
	}
}

func (s *Server) handleSessionStatus(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	serverSession.mu.Lock()
	name := serverSession.name
	started := serverSession.startedAt
	count := serverSession.logCount
	serverSession.mu.Unlock()

	if name == "" {
		return mcp.NewToolResultText("No active session. Use gleann_session_start to begin one."), nil
	}

	dur := time.Since(started).Round(time.Second)
	return mcp.NewToolResultText(fmt.Sprintf(
		"Active session: %q\nStarted: %s (%s ago)\nEvents logged: %d",
		name, started.Format(time.RFC3339), dur, count,
	)), nil
}
