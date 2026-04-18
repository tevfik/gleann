package a2a

import (
	"testing"
)

// ── evictOldTasks ─────────────────────────────────────────────

func TestEvictOldTasks_Cov(t *testing.T) {
	s := NewServer(AgentCard{Name: "test"})

	// Add 1002 completed/failed tasks
	for i := 0; i < 600; i++ {
		id := smallID(i)
		s.tasks[id] = &Task{
			ID: id,
			Status: TaskStatus{
				State: TaskStateCompleted,
			},
		}
	}
	for i := 600; i < 1002; i++ {
		id := smallID(i)
		s.tasks[id] = &Task{
			ID: id,
			Status: TaskStatus{
				State: TaskStateFailed,
			},
		}
	}

	if len(s.tasks) != 1002 {
		t.Fatalf("expected 1002 tasks, got %d", len(s.tasks))
	}

	s.evictOldTasks()

	if len(s.tasks) > 500 {
		t.Fatalf("expected <= 500 tasks after eviction, got %d", len(s.tasks))
	}
}

func TestEvictOldTasks_WorkingKept(t *testing.T) {
	s := NewServer(AgentCard{Name: "test"})

	// Add 600 working tasks
	for i := 0; i < 600; i++ {
		id := smallID(i)
		s.tasks[id] = &Task{
			ID: id,
			Status: TaskStatus{
				State: TaskStateWorking,
			},
		}
	}

	s.evictOldTasks()

	// Working tasks should be kept (evict only completed/failed)
	if len(s.tasks) < 500 {
		t.Fatalf("working tasks should not be evicted, got %d", len(s.tasks))
	}
}

func smallID(i int) string {
	return "task-" + smallItoa(i)
}

func smallItoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
