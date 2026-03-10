package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tevfik/gleann/pkg/gleann"
)

func resetWebhooks() {
	globalWebhooks.mu.Lock()
	globalWebhooks.webhooks = nil
	globalWebhooks.mu.Unlock()
}

func TestHandleListWebhooksEmpty(t *testing.T) {
	resetWebhooks()
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}

	req := httptest.NewRequest(http.MethodGet, "/api/webhooks", nil)
	w := httptest.NewRecorder()
	s.handleListWebhooks(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 0 {
		t.Errorf("expected 0 webhooks, got %v", resp["count"])
	}
}

func TestHandleRegisterWebhook(t *testing.T) {
	resetWebhooks()
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}

	body, _ := json.Marshal(webhookRegisterRequest{
		URL:    "https://example.com/hook",
		Events: []string{"build_complete"},
		Secret: "mysecret",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleRegisterWebhook(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "registered" {
		t.Errorf("expected status registered, got %v", resp["status"])
	}

	// Verify it's stored.
	globalWebhooks.mu.RLock()
	if len(globalWebhooks.webhooks) != 1 {
		t.Errorf("expected 1 webhook stored, got %d", len(globalWebhooks.webhooks))
	}
	globalWebhooks.mu.RUnlock()
}

func TestHandleRegisterWebhookMissingURL(t *testing.T) {
	resetWebhooks()
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}

	body, _ := json.Marshal(webhookRegisterRequest{Events: []string{"build_complete"}})
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleRegisterWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRegisterWebhookMissingEvents(t *testing.T) {
	resetWebhooks()
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}

	body, _ := json.Marshal(webhookRegisterRequest{URL: "https://example.com/hook"})
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleRegisterWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleDeleteWebhook(t *testing.T) {
	resetWebhooks()
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}

	// Register first.
	globalWebhooks.mu.Lock()
	globalWebhooks.webhooks = append(globalWebhooks.webhooks, Webhook{
		URL: "https://example.com/hook", Events: []string{"build_complete"},
	})
	globalWebhooks.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"url": "https://example.com/hook"})
	req := httptest.NewRequest(http.MethodDelete, "/api/webhooks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleDeleteWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	globalWebhooks.mu.RLock()
	if len(globalWebhooks.webhooks) != 0 {
		t.Errorf("expected 0 webhooks after delete, got %d", len(globalWebhooks.webhooks))
	}
	globalWebhooks.mu.RUnlock()
}

func TestHandleDeleteWebhookNotFound(t *testing.T) {
	resetWebhooks()
	s := &Server{config: gleann.DefaultConfig(), searchers: make(map[string]*gleann.LeannSearcher)}

	body, _ := json.Marshal(map[string]string{"url": "https://nonexistent.com/hook"})
	req := httptest.NewRequest(http.MethodDelete, "/api/webhooks", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.handleDeleteWebhook(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestNotifyWebhooksNoTargets(t *testing.T) {
	resetWebhooks()
	// Should not panic when no webhooks are registered.
	notifyWebhooks("build_complete", map[string]any{"index": "test"})
}

func TestNotifyWebhooksMatchesEvent(t *testing.T) {
	resetWebhooks()

	// Set up a webhook receiver.
	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		received <- buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	globalWebhooks.mu.Lock()
	globalWebhooks.webhooks = append(globalWebhooks.webhooks, Webhook{
		URL: srv.URL, Events: []string{"build_complete"},
	})
	globalWebhooks.mu.Unlock()

	notifyWebhooks("build_complete", map[string]any{"index": "test"})

	// Wait for async delivery.
	select {
	case body := <-received:
		var payload map[string]any
		json.Unmarshal(body, &payload)
		if payload["event"] != "build_complete" {
			t.Errorf("expected event build_complete, got %v", payload["event"])
		}
		if payload["index"] != "test" {
			t.Errorf("expected index test, got %v", payload["index"])
		}
	case <-make(chan struct{}):
		// Give goroutine time.
	}
}

func TestNotifyWebhooksWildcard(t *testing.T) {
	resetWebhooks()

	received := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		buf.ReadFrom(r.Body)
		received <- buf.Bytes()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	globalWebhooks.mu.Lock()
	globalWebhooks.webhooks = append(globalWebhooks.webhooks, Webhook{
		URL: srv.URL, Events: []string{"*"},
	})
	globalWebhooks.mu.Unlock()

	notifyWebhooks("index_deleted", map[string]any{"index": "some-idx"})

	select {
	case body := <-received:
		var payload map[string]any
		json.Unmarshal(body, &payload)
		if payload["event"] != "index_deleted" {
			t.Errorf("expected event index_deleted, got %v", payload["event"])
		}
	case <-make(chan struct{}):
	}
}
