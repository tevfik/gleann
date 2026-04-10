package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

func cmdTasks(args []string) {
	if len(args) > 0 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		printTasksUsage()
		return
	}

	asJSON := hasFlag(args, "--json")
	statusFilter := getFlag(args, "--status")
	serverAddr := getFlag(args, "--addr")
	if serverAddr == "" {
		serverAddr = "http://localhost:8080"
	}
	serverAddr = strings.TrimRight(serverAddr, "/")

	url := serverAddr + "/api/tasks"
	if statusFilter != "" {
		url += "?status=" + statusFilter
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach gleann server at %s: %v\n", serverAddr, err)
		fmt.Fprintln(os.Stderr, "hint: is 'gleann serve' running?")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "error: server returned %d\n", resp.StatusCode)
		os.Exit(1)
	}

	var result struct {
		Tasks []taskInfo `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid response: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(result)
		return
	}

	tasks := result.Tasks
	if len(tasks) == 0 {
		fmt.Println("No background tasks.")
		return
	}

	// Sort: running first, then queued, then completed/failed (most recent first).
	sort.Slice(tasks, func(i, j int) bool {
		return taskSortOrder(tasks[i].Status) < taskSortOrder(tasks[j].Status)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tSTATUS\tPROGRESS\tMESSAGE\tAGE")
	for _, t := range tasks {
		progress := fmt.Sprintf("%.0f%%", t.Progress*100)
		age := taskAge(t.CreatedAt)
		status := colorStatus(t.Status)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Type, status, progress, truncate(t.Message, 40), age)
	}
	w.Flush()

	// Summary line.
	running := 0
	queued := 0
	for _, t := range tasks {
		switch t.Status {
		case "running":
			running++
		case "queued":
			queued++
		}
	}
	if running > 0 || queued > 0 {
		fmt.Printf("\n%d running, %d queued, %d total\n", running, queued, len(tasks))
	}
}

type taskInfo struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	Progress  float64   `json:"progress"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

func taskSortOrder(status string) int {
	switch status {
	case "running":
		return 0
	case "queued":
		return 1
	case "failed":
		return 2
	case "completed":
		return 3
	default:
		return 4
	}
}

func colorStatus(status string) string {
	switch status {
	case "running":
		return "🔄 running"
	case "queued":
		return "⏳ queued"
	case "completed":
		return "✅ completed"
	case "failed":
		return "❌ failed"
	default:
		return status
	}
}

func taskAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func printTasksUsage() {
	fmt.Println(`gleann tasks — view background tasks

Usage:
  gleann tasks                     List all background tasks
  gleann tasks --status running    Filter by status (queued|running|completed|failed)
  gleann tasks --json              Output as JSON
  gleann tasks --addr <url>        Server address (default: http://localhost:8080)

Background tasks include:
  SleepTimeCompute     Extract memories from conversations
  AutoIndex            Automatic re-indexing on file changes
  MemoryConsolidate    Promote short→medium→long term memories  
  HealthCheck          Periodic Ollama & system health checks
  ReIndex              Manual re-index operations

Requires: gleann serve running`)
}
