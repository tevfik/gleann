package background

import (
	"context"
	"strings"
	"testing"
)

func TestBgTriggerFor_InvalidStatus(t *testing.T) {
	_, err := bgTriggerFor(TaskStatus("bogus"))
	if err == nil {
		t.Fatal("expected error for invalid status")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention the status, got: %v", err)
	}
}

func TestNewTaskFSM_ValidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		initial TaskStatus
		target  TaskStatus
		wantErr bool
	}{
		{"queued→running", StatusQueued, StatusRunning, false},
		{"queued→cancelled", StatusQueued, StatusCancelled, false},
		{"running→completed", StatusRunning, StatusCompleted, false},
		{"running→failed", StatusRunning, StatusFailed, false},
		{"running→cancelled", StatusRunning, StatusCancelled, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewTaskFSM(tt.initial)
			err := FireTaskTransition(sm, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("FireTaskTransition() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewTaskFSM_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		initial TaskStatus
		target  TaskStatus
	}{
		{"queued→completed", StatusQueued, StatusCompleted},
		{"queued→failed", StatusQueued, StatusFailed},
		{"completed→running", StatusCompleted, StatusRunning},
		{"failed→running", StatusFailed, StatusRunning},
		{"cancelled→running", StatusCancelled, StatusRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewTaskFSM(tt.initial)
			err := FireTaskTransition(sm, tt.target)
			if err == nil {
				t.Error("expected error for invalid transition")
			}
		})
	}
}

func TestNewTaskFSM_FullLifecycle(t *testing.T) {
	sm := NewTaskFSM(StatusQueued)

	// queued → running → completed
	if err := FireTaskTransition(sm, StatusRunning); err != nil {
		t.Fatal(err)
	}
	state, _ := sm.State(context.Background())
	if state != StatusRunning {
		t.Fatalf("expected running, got %v", state)
	}

	if err := FireTaskTransition(sm, StatusCompleted); err != nil {
		t.Fatal(err)
	}
	state, _ = sm.State(context.Background())
	if state != StatusCompleted {
		t.Fatalf("expected completed, got %v", state)
	}
}
