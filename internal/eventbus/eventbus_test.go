package eventbus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
)

// --------------------------------------------------------------------------
// Publish / Subscribe
// --------------------------------------------------------------------------

func TestBus_PublishSubscribe(t *testing.T) {
	bus := New(10, nil)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sub, err := bus.Subscribe(ctx, "test.topic")
	if err != nil {
		t.Fatalf("subscribe failed: %v", err)
	}

	err = bus.Publish("test.topic", map[string]any{
		"action": "index",
		"count":  42,
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	msg := <-sub
	payload, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if payload["action"] != "index" {
		t.Errorf("expected action=index, got %v", payload["action"])
	}

	msg.Ack()
}

func TestBus_MultipleSubscribers(t *testing.T) {
	bus := New(10, nil)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sub1, _ := bus.Subscribe(ctx, "shared.topic")
	sub2, _ := bus.Subscribe(ctx, "shared.topic")

	err := bus.Publish("shared.topic", map[string]any{"id": 1})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	// Both subscribers should receive the message
	var count int32
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		msg := <-sub1
		msg.Ack()
			atomic.AddInt32(&count, 1)
	}()

	go func() {
		defer wg.Done()
		msg := <-sub2
		msg.Ack()
		atomic.AddInt32(&count, 1)
	}()

	wg.Wait()
	if count != 2 {
		t.Errorf("expected 2 subscribers to receive, got %d", count)
	}
}

func TestBus_PublishAfterClose(t *testing.T) {
	bus := New(10, nil)
	bus.Close()

	err := bus.Publish("any.topic", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("publish after close should return nil (no-op), got error: %v", err)
	}
}

func TestBus_NackMessage(t *testing.T) {
	bus := New(10, nil)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sub, _ := bus.Subscribe(ctx, "nack.topic")

	err := bus.Publish("nack.topic", map[string]any{"status": "fail"})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	msg := <-sub
	// Nack should not panic
	msg.Nack()
}

func TestBus_MultipleTopics(t *testing.T) {
	bus := New(10, nil)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	topicA, _ := bus.Subscribe(ctx, "a.topic")
	topicB, _ := bus.Subscribe(ctx, "b.topic")

	bus.Publish("a.topic", map[string]any{"src": "a"})
	bus.Publish("b.topic", map[string]any{"src": "b"})

	msgA := <-topicA
	payloadA, _ := DecodePayload(msgA)
	msgA.Ack()

	msgB := <-topicB
	payloadB, _ := DecodePayload(msgB)
	msgB.Ack()

	if payloadA["src"] != "a" {
		t.Errorf("topic A expected src=a, got %v", payloadA["src"])
	}
	if payloadB["src"] != "b" {
		t.Errorf("topic B expected src=b, got %v", payloadB["src"])
	}
}

// --------------------------------------------------------------------------
// Lifecycle Topics (smoke test)
// --------------------------------------------------------------------------

func TestBus_LifecycleTopics(t *testing.T) {
	bus := New(10, nil)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	topic := "index.started"
	sub, _ := bus.Subscribe(ctx, topic)

	err := bus.Publish(topic, map[string]any{
		"path":   "/tmp/test",
		"files":  5,
	})
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	msg := <-sub
	payload, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if payload["path"] != "/tmp/test" {
		t.Errorf("expected path=/tmp/test, got %v", payload["path"])
	}

	msg.Ack()
}

func TestBus_BroadcastStress(t *testing.T) {
	bus := New(100, nil)
	defer bus.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const numSubscribers = 5
	const numMessages = 20

	var subs [numSubscribers]<-chan *message.Message
	for i := 0; i < numSubscribers; i++ {
		ch, err := bus.Subscribe(ctx, "stress.topic")
		if err != nil {
			t.Fatalf("subscribe %d failed: %v", i, err)
		}
		subs[i] = ch
	}

	for i := 0; i < numMessages; i++ {
		err := bus.Publish("stress.topic", map[string]any{"seq": i})
		if err != nil {
			t.Fatalf("publish %d failed: %v", i, err)
		}
	}

	received := make([]int, numSubscribers)
	var wg sync.WaitGroup
	wg.Add(numSubscribers)

	for i := 0; i < numSubscribers; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < numMessages; j++ {
				msg := <-subs[idx]
				received[idx]++
				msg.Ack()
			}
		}(i)
	}

	wg.Wait()

	for i := 0; i < numSubscribers; i++ {
		if received[i] != numMessages {
			t.Errorf("subscriber %d: expected %d, got %d", i, numMessages, received[i])
		}
	}
}
