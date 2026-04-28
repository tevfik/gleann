package server

import (
	"context"
	"testing"
	"time"

	"github.com/tevfik/gleann/internal/eventbus"
)

// TestEventBusWired verifies that the server initializes an event bus and
// exposes it via Bus().
func TestEventBusWired(t *testing.T) {
	s := testServer()
	if s.Bus() == nil {
		t.Fatal("server.Bus() returned nil; eventbus is not wired")
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

// TestEventBusSubscribeReceivesPublish confirms that a subscriber receives a
// payload published through the server's helper.
func TestEventBusSubscribeReceivesPublish(t *testing.T) {
	s := testServer()
	bus := s.Bus()
	if bus == nil {
		t.Fatal("bus is nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := bus.Subscribe(ctx, eventbus.TopicSearchCompleted)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		s.publish(eventbus.TopicSearchCompleted, map[string]any{
			"index":   "demo",
			"query":   "hello",
			"results": 3,
		})
	}()

	select {
	case msg := <-ch:
		payload, err := eventbus.DecodePayload(msg)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if payload["index"] != "demo" {
			t.Errorf("index = %v, want demo", payload["index"])
		}
		msg.Ack()
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}
