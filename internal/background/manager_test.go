package background

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubmitAndGet(t *testing.T) {
	m := NewManager(2)
	defer m.Stop()

	id := m.Submit(TaskTypeCustom, func(progress func(float64, string)) error {
		progress(0.5, "halfway")
		progress(1.0, "done")
		return nil
	})

	// Wait for task to complete.
	time.Sleep(100 * time.Millisecond)

	task := m.Get(id)
	if task == nil {
		t.Fatal("task not found")
	}
	if task.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", task.Status)
	}
	if task.Progress != 1.0 {
		t.Errorf("expected progress 1.0, got %f", task.Progress)
	}
}

func TestSubmitFailed(t *testing.T) {
	m := NewManager(1)
	defer m.Stop()

	id := m.Submit(TaskTypeCustom, func(progress func(float64, string)) error {
		return fmt.Errorf("something went wrong")
	})

	time.Sleep(100 * time.Millisecond)

	task := m.Get(id)
	if task == nil {
		t.Fatal("task not found")
	}
	if task.Status != StatusFailed {
		t.Errorf("expected failed, got %s", task.Status)
	}
	if task.Error != "something went wrong" {
		t.Errorf("unexpected error: %s", task.Error)
	}
}

func TestList(t *testing.T) {
	m := NewManager(1)
	defer m.Stop()

	done := make(chan struct{})
	m.Submit(TaskTypeSleepTimeCompute, func(progress func(float64, string)) error {
		// Wait until test signals to complete.
		<-done
		return nil
	})
	m.Submit(TaskTypeHealthCheck, func(progress func(float64, string)) error {
		<-done
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	all := m.List("")
	if len(all) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(all))
	}

	close(done)
	time.Sleep(100 * time.Millisecond)

	completed := m.List(StatusCompleted)
	if len(completed) != 2 {
		t.Errorf("expected 2 completed, got %d", len(completed))
	}
}

func TestActiveCount(t *testing.T) {
	m := NewManager(2)
	defer m.Stop()

	block := make(chan struct{})
	m.Submit(TaskTypeCustom, func(progress func(float64, string)) error {
		<-block
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	if m.ActiveCount() != 1 {
		t.Errorf("expected 1 active, got %d", m.ActiveCount())
	}

	close(block)
	time.Sleep(100 * time.Millisecond)

	if m.ActiveCount() != 0 {
		t.Errorf("expected 0 active, got %d", m.ActiveCount())
	}
}

func TestCleanup(t *testing.T) {
	m := NewManager(2)
	defer m.Stop()

	m.Submit(TaskTypeCustom, func(progress func(float64, string)) error {
		return nil
	})
	m.Submit(TaskTypeCustom, func(progress func(float64, string)) error {
		return nil
	})

	time.Sleep(100 * time.Millisecond)

	// All tasks completed.
	removed := m.Cleanup(0) // maxAge=0 means remove all finished tasks.
	if removed != 2 {
		t.Errorf("expected 2 removed, got %d", removed)
	}

	if len(m.List("")) != 0 {
		t.Error("expected empty task list after cleanup")
	}
}

func TestGetNotFound(t *testing.T) {
	m := NewManager(1)
	defer m.Stop()

	if m.Get("nonexistent") != nil {
		t.Error("expected nil for nonexistent task")
	}
}

func TestConcurrentSubmit(t *testing.T) {
	m := NewManager(4)
	defer m.Stop()

	var completed int64

	for i := 0; i < 20; i++ {
		m.Submit(TaskTypeCustom, func(progress func(float64, string)) error {
			time.Sleep(10 * time.Millisecond)
			atomic.AddInt64(&completed, 1)
			return nil
		})
	}

	// Wait for all to complete.
	time.Sleep(500 * time.Millisecond)

	got := atomic.LoadInt64(&completed)
	if got != 20 {
		t.Errorf("expected 20 completed, got %d", got)
	}

	all := m.List(StatusCompleted)
	if len(all) != 20 {
		t.Errorf("expected 20 completed tasks, got %d", len(all))
	}
}

func TestStopIdempotent(t *testing.T) {
	m := NewManager(1)
	m.Stop()
	m.Stop() // Should not panic.
}
