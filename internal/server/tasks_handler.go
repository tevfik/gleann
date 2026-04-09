package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/tevfik/gleann/internal/background"
)

// mountBackgroundTasks registers background task API endpoints on the given mux.
func (s *Server) mountBackgroundTasks(mux *http.ServeMux) {
	if s.bgManager == nil {
		return
	}
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("DELETE /api/tasks", s.handleCleanupTasks)
}

// handleListTasks returns all background tasks, optionally filtered by status.
func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	status := background.TaskStatus(r.URL.Query().Get("status"))
	tasks := s.bgManager.List(status)
	if tasks == nil {
		tasks = []background.Task{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"tasks": tasks,
		"count": len(tasks),
	})
}

// handleGetTask returns a single task by ID.
func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := s.bgManager.Get(id)
	if task == nil {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// handleCleanupTasks removes completed/failed tasks older than 1 hour.
func (s *Server) handleCleanupTasks(w http.ResponseWriter, r *http.Request) {
	removed := s.bgManager.Cleanup(1 * time.Hour)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"removed": removed,
	})
}
