package server

import (
	"testing"
)

// TestEventBusWired verifies that the server initializes an event bus.
func TestEventBusWired(t *testing.T) {
	s := testServer()
	if s.bus == nil {
		t.Fatal("server.bus is nil; eventbus is not wired")
	}
}

// TestPublishNilSafe verifies the publish helper does not panic when the
// bus is nil.
func TestPublishNilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publish on nil bus panicked: %v", r)
		}
	}()
	var s *Server
	s.publish("any", map[string]any{"k": "v"})

	s2 := &Server{}
	s2.publish("any", map[string]any{"k": "v"})
}
