package main

import (
	"testing"
	"time"
)

func TestTaskSortOrder(t *testing.T) {
	tests := []struct {
		status string
		want   int
	}{
		{"running", 0},
		{"queued", 1},
		{"failed", 2},
		{"completed", 3},
		{"unknown", 4},
	}
	for _, tt := range tests {
		got := taskSortOrder(tt.status)
		if got != tt.want {
			t.Errorf("taskSortOrder(%q) = %d, want %d", tt.status, got, tt.want)
		}
	}
}

func TestColorStatus(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"running", "🔄 running"},
		{"queued", "⏳ queued"},
		{"completed", "✅ completed"},
		{"failed", "❌ failed"},
		{"other", "other"},
	}
	for _, tt := range tests {
		got := colorStatus(tt.status)
		if got != tt.want {
			t.Errorf("colorStatus(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestTaskAge(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "-"},
		{"just now", now.Add(-10 * time.Second), "just now"},
		{"minutes", now.Add(-5 * time.Minute), "5m ago"},
		{"hours", now.Add(-3 * time.Hour), "3h ago"},
		{"days", now.Add(-48 * time.Hour), "2d ago"},
	}
	for _, tt := range tests {
		got := taskAge(tt.t)
		if got != tt.want {
			t.Errorf("taskAge(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
