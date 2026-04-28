// Package eventbus provides an in-process pub/sub event bus backed by
// Watermill's GoChannel for gleann. It publishes lifecycle events for
// indexing, search, memory, and background tasks — enabling plugins,
// TUI dashboards, and external consumers to react to gleann activity.
package eventbus

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
)

// ── Topic constants ────────────────────────────────────────────

const (
	// Index lifecycle
	TopicIndexStarted   = "index.started"
	TopicIndexProgress  = "index.progress"
	TopicIndexCompleted = "index.completed"
	TopicIndexFailed    = "index.failed"

	// Search events
	TopicSearchRequest   = "search.request"
	TopicSearchCompleted = "search.completed"

	// Memory events
	TopicMemoryInject  = "memory.inject"
	TopicMemoryForget  = "memory.forget"
	TopicMemorySearch  = "memory.search"
	TopicMemoryUpdated = "memory.updated"

	// Background tasks
	TopicTaskQueued    = "task.queued"
	TopicTaskStarted   = "task.started"
	TopicTaskCompleted = "task.completed"
	TopicTaskFailed    = "task.failed"

	// A2A events
	TopicA2ARequest  = "a2a.request"
	TopicA2AResponse = "a2a.response"

	// Plugin events
	TopicPluginLoaded   = "plugin.loaded"
	TopicPluginUnloaded = "plugin.unloaded"
	TopicPluginError    = "plugin.error"
)

// Bus is an in-process event bus backed by Watermill GoChannel.
type Bus struct {
	pubSub *gochannel.GoChannel
	log    *slog.Logger
	closed bool
	mu     sync.RWMutex
}

// New creates a new event bus with the given buffer size per subscriber.
func New(bufferSize int64, log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}

	wmLog := watermill.NewSlogLogger(log)
	pubSub := gochannel.NewGoChannel(gochannel.Config{
		OutputChannelBuffer:            bufferSize,
		Persistent:                     false,
		BlockPublishUntilSubscriberAck: false,
	}, wmLog)

	return &Bus{
		pubSub: pubSub,
		log:    log,
	}
}

// Publish sends an event on the given topic with arbitrary payload data.
func (b *Bus) Publish(topic string, payload map[string]any) error {
	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		return nil
	}
	b.mu.RUnlock()

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	msg := message.NewMessage(watermill.NewUUID(), data)
	msg.Metadata.Set("topic", topic)

	return b.pubSub.Publish(topic, msg)
}

// Subscribe returns a channel of Watermill messages for the given topic.
func (b *Bus) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	return b.pubSub.Subscribe(ctx, topic)
}

// Close shuts down the event bus and all subscriptions.
func (b *Bus) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return b.pubSub.Close()
}

// DecodePayload unmarshals a Watermill message body into a map.
func DecodePayload(msg *message.Message) (map[string]any, error) {
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
