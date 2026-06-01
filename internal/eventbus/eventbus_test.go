package eventbus

import (
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
)

func TestDecodePayload_Valid(t *testing.T) {
	msg := message.NewMessage(watermill.NewUUID(), []byte(`{"key":"value","num":42}`))
	payload, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["key"] != "value" {
		t.Errorf("expected key=value, got %v", payload["key"])
	}
	if payload["num"] != float64(42) {
		t.Errorf("expected num=42, got %v", payload["num"])
	}
}

func TestDecodePayload_InvalidJSON(t *testing.T) {
	msg := message.NewMessage(watermill.NewUUID(), []byte(`not json`))
	_, err := DecodePayload(msg)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDecodePayload_EmptyObject(t *testing.T) {
	msg := message.NewMessage(watermill.NewUUID(), []byte(`{}`))
	payload, err := DecodePayload(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty map, got %v", payload)
	}
}
