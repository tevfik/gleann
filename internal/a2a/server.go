// Package a2a implements the Agent-to-Agent (A2A) protocol for gleann.
// It exposes gleann's capabilities (search, RAG, memory, code graph) as
// discoverable agent skills via the A2A v1.0 HTTP+JSON binding.
//
// A2A complements MCP: MCP is for agent↔tool access, A2A is for agent↔agent
// collaboration. Together they let external systems use gleann as both a
// tool and a collaborating peer.
package a2a

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ── A2A Data Model (HTTP+JSON binding, v1.0) ───────────────────

// AgentCard is the self-describing manifest published at
// /.well-known/agent-card.json. External agents use it to discover
// gleann's capabilities.
type AgentCard struct {
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	Version             string            `json:"version"`
	SupportedInterfaces []AgentInterface  `json:"supportedInterfaces"`
	Capabilities        AgentCapabilities `json:"capabilities"`
	DefaultInputModes   []string          `json:"defaultInputModes"`
	DefaultOutputModes  []string          `json:"defaultOutputModes"`
	Skills              []AgentSkill      `json:"skills"`
	DocumentationURL    string            `json:"documentationUrl,omitempty"`
	IconURL             string            `json:"iconUrl,omitempty"`
}

// AgentInterface declares a protocol binding endpoint.
type AgentInterface struct {
	URL             string `json:"url"`
	ProtocolBinding string `json:"protocolBinding"`
	ProtocolVersion string `json:"protocolVersion"`
}

// AgentCapabilities declares optional features.
type AgentCapabilities struct {
	Streaming         bool `json:"streaming"`
	PushNotifications bool `json:"pushNotifications"`
}

// AgentSkill describes a specific capability.
type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

// ── Task lifecycle ─────────────────────────────────────────────

// TaskState represents the A2A task lifecycle states.
type TaskState string

const (
	TaskStateSubmitted     TaskState = "TASK_STATE_SUBMITTED"
	TaskStateWorking       TaskState = "TASK_STATE_WORKING"
	TaskStateCompleted     TaskState = "TASK_STATE_COMPLETED"
	TaskStateFailed        TaskState = "TASK_STATE_FAILED"
	TaskStateCanceled      TaskState = "TASK_STATE_CANCELED"
	TaskStateInputRequired TaskState = "TASK_STATE_INPUT_REQUIRED"
)

// Task is the core unit of work in A2A.
type Task struct {
	ID        string     `json:"id"`
	ContextID string     `json:"contextId,omitempty"`
	Status    TaskStatus `json:"status"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	History   []Message  `json:"history,omitempty"`
}

// TaskStatus holds the current state and optional status message.
type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp string    `json:"timestamp,omitempty"`
}

// Message is a communication turn between client and agent.
type Message struct {
	MessageID string `json:"messageId"`
	Role      string `json:"role"` // "ROLE_USER" or "ROLE_AGENT"
	Parts     []Part `json:"parts"`
}

// Part is the smallest unit of content.
type Part struct {
	Text      string `json:"text,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
}

// Artifact is a task output.
type Artifact struct {
	ArtifactID string `json:"artifactId"`
	Name       string `json:"name,omitempty"`
	Parts      []Part `json:"parts"`
}

// ── Request/Response ───────────────────────────────────────────

// SendMessageRequest is the input for POST /message:send.
type SendMessageRequest struct {
	Message  Message                `json:"message"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// SendMessageResponse wraps either a Task or a Message.
type SendMessageResponse struct {
	Task    *Task    `json:"task,omitempty"`
	Message *Message `json:"message,omitempty"`
}

// ErrorResponse is the A2A error envelope (HTTP+JSON binding).
type ErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"error"`
}

// ── Server ─────────────────────────────────────────────────────

// SkillHandler processes an A2A message for a specific skill.
// It receives the user's text and returns an artifact text.
type SkillHandler func(ctx SkillContext) (string, error)

// SkillContext carries information for a skill handler invocation.
type SkillContext struct {
	Query    string
	Metadata map[string]interface{}
}

// Server implements the A2A HTTP+JSON protocol binding.
type Server struct {
	card     AgentCard
	handlers map[string]SkillHandler // skill ID → handler
	tasks    map[string]*Task
	mu       sync.RWMutex
}

// NewServer creates an A2A server with the given agent card.
func NewServer(card AgentCard) *Server {
	return &Server{
		card:     card,
		handlers: make(map[string]SkillHandler),
		tasks:    make(map[string]*Task),
	}
}

// RegisterSkill associates a handler with a skill ID.
func (s *Server) RegisterSkill(skillID string, handler SkillHandler) {
	s.handlers[skillID] = handler
}

// Mount registers A2A HTTP handlers on the given mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/agent-card.json", s.handleAgentCard)
	mux.HandleFunc("POST /a2a/v1/message:send", s.handleSendMessage)
	mux.HandleFunc("GET /a2a/v1/tasks/{id}", s.handleGetTask)
}

// handleAgentCard serves the agent discovery document.
func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	json.NewEncoder(w).Encode(s.card)
}

// handleSendMessage processes an incoming A2A message.
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeA2AError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request body: "+err.Error())
		return
	}

	// Extract text from message parts.
	query := extractText(req.Message.Parts)
	if query == "" {
		writeA2AError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "message must contain at least one text part")
		return
	}

	// Route to the best matching skill.
	skillID := s.routeSkill(query, req.Metadata)
	handler, ok := s.handlers[skillID]
	if !ok {
		writeA2AError(w, http.StatusBadRequest, "UNSUPPORTED_OPERATION", fmt.Sprintf("no handler for skill %q", skillID))
		return
	}

	// Create task.
	now := time.Now().UTC().Format(time.RFC3339)
	task := &Task{
		ID:        uuid.New().String(),
		ContextID: uuid.New().String(),
		Status: TaskStatus{
			State:     TaskStateWorking,
			Timestamp: now,
		},
		History: []Message{req.Message},
	}

	// Execute synchronously (blocking mode — default per spec).
	result, err := handler(SkillContext{
		Query:    query,
		Metadata: req.Metadata,
	})

	if err != nil {
		task.Status = TaskStatus{
			State:     TaskStateFailed,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Message: &Message{
				MessageID: uuid.New().String(),
				Role:      "ROLE_AGENT",
				Parts:     []Part{{Text: err.Error()}},
			},
		}
	} else {
		task.Status = TaskStatus{
			State:     TaskStateCompleted,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		task.Artifacts = []Artifact{{
			ArtifactID: uuid.New().String(),
			Name:       skillID + "-result",
			Parts:      []Part{{Text: result, MediaType: "text/plain"}},
		}}
	}

	// Store task for later retrieval.
	s.mu.Lock()
	s.tasks[task.ID] = task
	// Simple eviction: keep only last 1000 tasks.
	if len(s.tasks) > 1000 {
		s.evictOldTasks()
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SendMessageResponse{Task: task})
}

// handleGetTask returns a previously created task.
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.RLock()
	task, ok := s.tasks[id]
	s.mu.RUnlock()

	if !ok {
		writeA2AError(w, http.StatusNotFound, "TASK_NOT_FOUND", fmt.Sprintf("task %q not found", id))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// ── Helpers ────────────────────────────────────────────────────

// routeSkill selects the best skill based on message text and metadata.
// If metadata contains "skill", use it directly. Otherwise, use keyword matching.
func (s *Server) routeSkill(query string, metadata map[string]interface{}) string {
	// Explicit skill selection via metadata.
	if metadata != nil {
		if skill, ok := metadata["skill"].(string); ok {
			return skill
		}
	}

	// Keyword-based routing — check in priority order (most specific first).
	type skillMatch struct {
		id       string
		keywords []string
	}
	priorities := []skillMatch{
		{"code-analysis", []string{"callers", "callees", "impact", "dependency", "graph", "bağımlılık"}},
		{"memory-management", []string{"remember", "forget", "memory", "recall", "hatırla", "unut", "bellek"}},
		{"semantic-search", []string{"search", "find", "ara", "bul", "look for"}},
		{"ask-rag", []string{"ask", "explain", "how", "why", "what", "describe", "sor", "anlat", "nasıl", "neden"}},
	}

	for _, sm := range priorities {
		for _, kw := range sm.keywords {
			if containsCI(query, kw) {
				if _, ok := s.handlers[sm.id]; ok {
					return sm.id
				}
			}
		}
	}

	// Default to ask-rag if registered.
	if _, ok := s.handlers["ask-rag"]; ok {
		return "ask-rag"
	}

	// Return first registered handler.
	for id := range s.handlers {
		return id
	}
	return ""
}

// containsCI checks if s contains substr (case-insensitive).
func containsCI(s, substr string) bool {
	sLower := toLower(s)
	subLower := toLower(substr)
	return len(subLower) > 0 && len(sLower) >= len(subLower) && contains(sLower, subLower)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// extractText concatenates all text parts from a message.
func extractText(parts []Part) string {
	var text string
	for _, p := range parts {
		if p.Text != "" {
			if text != "" {
				text += " "
			}
			text += p.Text
		}
	}
	return text
}

// evictOldTasks removes the oldest tasks to keep the store bounded.
// Must be called with s.mu held.
func (s *Server) evictOldTasks() {
	// Simple strategy: remove completed/failed tasks first.
	for id, t := range s.tasks {
		if t.Status.State == TaskStateCompleted || t.Status.State == TaskStateFailed {
			delete(s.tasks, id)
		}
		if len(s.tasks) <= 500 {
			return
		}
	}
}

func writeA2AError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error: struct {
			Code    int    `json:"code"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}{
			Code:    status,
			Status:  code,
			Message: msg,
		},
	})
}

// DefaultAgentCard creates a standard agent card for gleann.
func DefaultAgentCard(version, baseURL string) AgentCard {
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	if version == "" {
		version = "dev"
	}

	// Allow override via env var.
	if envURL := os.Getenv("GLEANN_A2A_BASE_URL"); envURL != "" {
		baseURL = envURL
	}

	return AgentCard{
		Name:        "gleann",
		Description: "Local-first semantic search, RAG, and persistent memory engine with code graph analysis. Indexes documents and code, answers questions with retrieved context, manages hierarchical long-term memory, and analyzes code dependencies.",
		Version:     version,
		SupportedInterfaces: []AgentInterface{
			{
				URL:             baseURL + "/a2a/v1",
				ProtocolBinding: "HTTP+JSON",
				ProtocolVersion: "1.0",
			},
		},
		Capabilities: AgentCapabilities{
			Streaming:         false,
			PushNotifications: false,
		},
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain", "application/json"},
		Skills: []AgentSkill{
			{
				ID:          "semantic-search",
				Name:        "Semantic Search",
				Description: "Search across indexed documents using hybrid vector + BM25 search with reranking",
				Tags:        []string{"search", "rag", "semantic", "vector", "hybrid"},
				Examples: []string{
					"Search for 'error handling patterns' in my codebase",
					"Find documents about authentication",
				},
			},
			{
				ID:          "ask-rag",
				Name:        "RAG Question Answering",
				Description: "Answer questions using retrieved context from indexed documents with memory-augmented generation",
				Tags:        []string{"rag", "qa", "llm", "memory", "generation"},
				Examples: []string{
					"How does the authentication module work?",
					"Explain the data flow in the payment system",
				},
			},
			{
				ID:          "code-analysis",
				Name:        "Code Graph Analysis",
				Description: "Analyze code dependencies, find callers/callees, and assess impact of changes using AST-based knowledge graphs",
				Tags:        []string{"code", "graph", "dependencies", "impact", "ast"},
				Examples: []string{
					"What functions call handleAuth?",
					"Show the impact of changing the User struct",
				},
			},
			{
				ID:          "memory-management",
				Name:        "Persistent Memory",
				Description: "Store, retrieve, search, and manage long-term memory blocks with hierarchical tiers, scoping, and character limits",
				Tags:        []string{"memory", "context", "persistence", "knowledge", "blocks"},
				Examples: []string{
					"Remember that the API uses JWT tokens",
					"What do you know about the deployment process?",
				},
			},
		},
	}
}
