// Package background provides a lightweight task manager for long-running
// background operations. It gives users visibility into what gleann is
// doing behind the scenes (indexing, memory consolidation, health checks).
package background

import (
	"fmt"
	"sync"
	"time"
)

// TaskType identifies the kind of background task.
type TaskType string

const (
	TaskTypeSleepTimeCompute  TaskType = "SleepTimeCompute"
	TaskTypeAutoIndex         TaskType = "AutoIndex"
	TaskTypeMemoryConsolidate TaskType = "MemoryConsolidate"
	TaskTypeHealthCheck       TaskType = "HealthCheck"
	TaskTypeReIndex           TaskType = "ReIndex"
	TaskTypeCustom            TaskType = "Custom"
)

// TaskStatus represents the lifecycle state of a background task.
type TaskStatus string

const (
	StatusQueued    TaskStatus = "queued"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

// Task represents a background operation.
type Task struct {
	ID        string     `json:"id"`
	Type      TaskType   `json:"type"`
	Status    TaskStatus `json:"status"`
	Progress  float64    `json:"progress"` // 0.0 - 1.0
	Message   string     `json:"message"`  // Human-readable status
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt time.Time  `json:"started_at,omitempty"`
	EndedAt   time.Time  `json:"ended_at,omitempty"`
}

// TaskFunc is a function that performs background work.
// It receives a progress callback to report completion percentage.
type TaskFunc func(progress func(pct float64, msg string)) error

// Manager manages background tasks with a bounded worker pool.
type Manager struct {
	mu      sync.RWMutex
	tasks   map[string]*Task
	queue   chan *taskEntry
	nextID  int
	stopCh  chan struct{}
	stopped bool
	workers int
}

type taskEntry struct {
	task *Task
	fn   TaskFunc
}

// NewManager creates a background task manager with the given number of workers.
// Workers default to 2 if n <= 0 (low CPU impact for background tasks).
func NewManager(workers int) *Manager {
	if workers <= 0 {
		workers = 2
	}
	m := &Manager{
		tasks:   make(map[string]*Task),
		queue:   make(chan *taskEntry, 100),
		stopCh:  make(chan struct{}),
		workers: workers,
	}

	// Start worker goroutines.
	for i := 0; i < workers; i++ {
		go m.worker()
	}
	return m
}

// Submit adds a new task to the queue and returns its ID.
func (m *Manager) Submit(taskType TaskType, fn TaskFunc) string {
	m.mu.Lock()
	m.nextID++
	id := fmt.Sprintf("bg-%d", m.nextID)
	task := &Task{
		ID:        id,
		Type:      taskType,
		Status:    StatusQueued,
		Message:   "Waiting in queue",
		CreatedAt: time.Now(),
	}
	m.tasks[id] = task
	m.mu.Unlock()

	// Non-blocking send — if queue is full, run inline.
	select {
	case m.queue <- &taskEntry{task: task, fn: fn}:
	default:
		go m.executeTask(&taskEntry{task: task, fn: fn})
	}

	return id
}

// Get returns a task by ID, or nil if not found.
func (m *Manager) Get(id string) *Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t := m.tasks[id]
	if t == nil {
		return nil
	}
	// Return a copy.
	cp := *t
	return &cp
}

// List returns all tasks, optionally filtered by status.
func (m *Manager) List(statusFilter TaskStatus) []Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []Task
	for _, t := range m.tasks {
		if statusFilter == "" || t.Status == statusFilter {
			result = append(result, *t)
		}
	}
	return result
}

// ActiveCount returns the number of queued or running tasks.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, t := range m.tasks {
		if t.Status == StatusQueued || t.Status == StatusRunning {
			count++
		}
	}
	return count
}

// Stop signals all workers to exit and waits briefly for cleanup.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	m.mu.Unlock()
	close(m.stopCh)
}

// Cleanup removes completed/failed tasks older than the given duration.
func (m *Manager) Cleanup(maxAge time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for id, t := range m.tasks {
		if (t.Status == StatusCompleted || t.Status == StatusFailed) && t.EndedAt.Before(cutoff) {
			delete(m.tasks, id)
			removed++
		}
	}
	return removed
}

// worker processes tasks from the queue.
func (m *Manager) worker() {
	for {
		select {
		case <-m.stopCh:
			return
		case entry := <-m.queue:
			m.executeTask(entry)
		}
	}
}

// executeTask runs a single task and updates its status.
func (m *Manager) executeTask(entry *taskEntry) {
	m.mu.Lock()
	entry.task.Status = StatusRunning
	entry.task.StartedAt = time.Now()
	entry.task.Message = "Running"
	m.mu.Unlock()

	progress := func(pct float64, msg string) {
		m.mu.Lock()
		entry.task.Progress = pct
		if msg != "" {
			entry.task.Message = msg
		}
		m.mu.Unlock()
	}

	err := entry.fn(progress)

	m.mu.Lock()
	entry.task.EndedAt = time.Now()
	if err != nil {
		entry.task.Status = StatusFailed
		entry.task.Error = err.Error()
		entry.task.Message = "Failed: " + err.Error()
	} else {
		entry.task.Status = StatusCompleted
		entry.task.Progress = 1.0
		entry.task.Message = "Completed"
	}
	m.mu.Unlock()
}
