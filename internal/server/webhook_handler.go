package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// Webhook represents a registered webhook endpoint.
type Webhook struct {
	URL    string   `json:"url"`
	Events []string `json:"events"` // e.g. ["build_complete", "index_deleted"]
	Secret string   `json:"secret,omitempty"`
}

// webhookStore manages registered webhooks (in-memory).
type webhookStore struct {
	mu       sync.RWMutex
	webhooks []Webhook
}

var globalWebhooks = &webhookStore{}

// webhookRegisterRequest is the request body for POST /api/webhooks.
type webhookRegisterRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Secret string   `json:"secret,omitempty"`
}

// handleListWebhooks returns all registered webhooks.
func (s *Server) handleListWebhooks(w http.ResponseWriter, r *http.Request) {
	globalWebhooks.mu.RLock()
	defer globalWebhooks.mu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"webhooks": globalWebhooks.webhooks,
		"count":    len(globalWebhooks.webhooks),
	})
}

// handleRegisterWebhook registers a new webhook.
func (s *Server) handleRegisterWebhook(w http.ResponseWriter, r *http.Request) {
	var req webhookRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if len(req.Events) == 0 {
		writeError(w, http.StatusBadRequest, "events is required (e.g. [\"build_complete\", \"index_deleted\"])")
		return
	}

	wh := Webhook{
		URL:    req.URL,
		Events: req.Events,
		Secret: req.Secret,
	}

	globalWebhooks.mu.Lock()
	globalWebhooks.webhooks = append(globalWebhooks.webhooks, wh)
	globalWebhooks.mu.Unlock()

	writeJSON(w, http.StatusCreated, map[string]any{
		"status":  "registered",
		"webhook": wh,
	})
}

// handleDeleteWebhook removes a webhook by URL.
func (s *Server) handleDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	globalWebhooks.mu.Lock()
	found := false
	filtered := globalWebhooks.webhooks[:0]
	for _, wh := range globalWebhooks.webhooks {
		if wh.URL == req.URL {
			found = true
			continue
		}
		filtered = append(filtered, wh)
	}
	globalWebhooks.webhooks = filtered
	globalWebhooks.mu.Unlock()

	if !found {
		writeError(w, http.StatusNotFound, "webhook not found for url: "+req.URL)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "deleted",
		"url":    req.URL,
	})
}

// notifyWebhooks sends an event payload to all registered webhooks matching the event type.
// It runs asynchronously and does not block the caller.
func notifyWebhooks(event string, payload map[string]any) {
	globalWebhooks.mu.RLock()
	var targets []Webhook
	for _, wh := range globalWebhooks.webhooks {
		for _, e := range wh.Events {
			if e == event || e == "*" {
				targets = append(targets, wh)
				break
			}
		}
	}
	globalWebhooks.mu.RUnlock()

	if len(targets) == 0 {
		return
	}

	payload["event"] = event
	payload["timestamp"] = time.Now().UTC().Format(time.RFC3339)
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	for _, wh := range targets {
		serverMetrics.RecordWebhook()
		go fireWebhookHTTP(wh.URL, wh.Secret, body)
	}
}

// fireWebhookHTTP sends a POST request to the webhook URL with the JSON payload.
// If a secret is configured, it adds an X-Gleann-Signature header (HMAC-SHA256).
func fireWebhookHTTP(url, secret string, body []byte) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("webhook: failed to create request for %s: %v", url, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "gleann-webhook/1.0")

	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Gleann-Signature", "sha256="+sig)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("webhook: failed to deliver to %s: %v", url, err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("webhook: %s returned status %d", url, resp.StatusCode)
	}
}
