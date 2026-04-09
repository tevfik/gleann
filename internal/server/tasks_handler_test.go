package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/background"
	"github.com/tevfik/gleann/pkg/gleann"
)

func TestHandleListTasks(t *testing.T) {
	s := NewServer(gleann.Config{IndexDir: t.TempDir()}, ":0", "test")
	defer s.bgManager.Stop()

	// Submit a quick task.
	s.bgManager.Submit(background.TaskTypeHealthCheck, func(p func(float64, string)) error {
		return nil
	})
	time.Sleep(100 * time.Millisecond)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	s.handleListTasks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("expected 1 task, got %v", resp["count"])
	}
}

func TestHandleListTasksFilter(t *testing.T) {
	s := NewServer(gleann.Config{IndexDir: t.TempDir()}, ":0", "test")
	defer s.bgManager.Stop()

	s.bgManager.Submit(background.TaskTypeCustom, func(p func(float64, string)) error {
		return nil
	})
	time.Sleep(100 * time.Millisecond)

	req := httptest.NewRequest("GET", "/api/tasks?status=completed", nil)
	w := httptest.NewRecorder()
	s.handleListTasks(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 1 {
		t.Errorf("expected 1, got %v", resp["count"])
	}

	req = httptest.NewRequest("GET", "/api/tasks?status=running", nil)
	w = httptest.NewRecorder()
	s.handleListTasks(w, req)

	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 0 {
		t.Errorf("expected 0, got %v", resp["count"])
	}
}

func TestHandleGetTask(t *testing.T) {
	s := NewServer(gleann.Config{IndexDir: t.TempDir()}, ":0", "test")
	defer s.bgManager.Stop()

	id := s.bgManager.Submit(background.TaskTypeCustom, func(p func(float64, string)) error {
		return nil
	})
	time.Sleep(100 * time.Millisecond)

	req := httptest.NewRequest("GET", "/api/tasks/"+id, nil)
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.handleGetTask(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var task background.Task
	json.NewDecoder(w.Body).Decode(&task)
	if task.ID != id {
		t.Errorf("expected ID %s, got %s", id, task.ID)
	}
}

func TestHandleGetTask_NotFound(t *testing.T) {
	s := NewServer(gleann.Config{IndexDir: t.TempDir()}, ":0", "test")
	defer s.bgManager.Stop()

	req := httptest.NewRequest("GET", "/api/tasks/nope", nil)
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	s.handleGetTask(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleCleanupTasks(t *testing.T) {
	s := NewServer(gleann.Config{IndexDir: t.TempDir()}, ":0", "test")
	defer s.bgManager.Stop()

	s.bgManager.Submit(background.TaskTypeCustom, func(p func(float64, string)) error {
		return nil
	})
	time.Sleep(100 * time.Millisecond)

	// Cleanup with maxAge 1h — nothing should be removed.
	req := httptest.NewRequest("DELETE", "/api/tasks", nil)
	w := httptest.NewRecorder()
	s.handleCleanupTasks(w, req)

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["removed"].(float64) != 0 {
		t.Errorf("expected 0 removed (too recent), got %v", resp["removed"])
	}
}
