package memory

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestRemoteClient(t *testing.T) {
	var storedBlocks []Block

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"engine":"gleann-go","status":"ok"}`))

		case r.URL.Path == "/api/blocks" && r.Method == http.MethodPost:
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			b := Block{
				ID:        "blk-123",
				Tier:      Tier(req["tier"].(string)),
				Label:     req["label"].(string),
				Content:   req["content"].(string),
				Source:    req["source"].(string),
				Scope:     req["scope"].(string),
				CreatedAt: time.Now(),
			}
			storedBlocks = append(storedBlocks, b)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(b)

		case r.URL.Path == "/api/blocks" && r.Method == http.MethodGet:
			scope := r.URL.Query().Get("scope")
			tier := r.URL.Query().Get("tier")
			var filtered []Block
			for _, b := range storedBlocks {
				if scope != "" && b.Scope != scope {
					continue
				}
				if tier != "" && string(b.Tier) != tier {
					continue
				}
				filtered = append(filtered, b)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"blocks": filtered})

		case r.URL.Path == "/api/blocks/search" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"blocks": storedBlocks})

		case r.URL.Path == "/api/blocks/stats" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Stats{TotalCount: len(storedBlocks)})

		case r.URL.Path == "/api/blocks/blk-123" && r.Method == http.MethodDelete:
			storedBlocks = nil
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": 1})

		case r.URL.Path == "/api/blocks" && r.Method == http.MethodDelete:
			count := len(storedBlocks)
			storedBlocks = nil
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"deleted": count})

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	// 1. Test NewRemoteClient direct operations
	client := NewRemoteClient(ts.URL)

	// Add block
	b, err := client.Add(TierLong, "test-label", "some important memory", []string{"tag1"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if b.ID != "blk-123" {
		t.Fatalf("unexpected id: %s", b.ID)
	}

	// Add Scoped note
	note, err := client.AddScopedNote("session-abc", TierShort, "step1", "Ran a search")
	if err != nil {
		t.Fatalf("AddScopedNote failed: %v", err)
	}
	if note.Scope != "session-abc" {
		t.Fatalf("expected scope session-abc, got %s", note.Scope)
	}

	// List Scoped
	scoped, err := client.ListScoped("session-abc", TierShort)
	if err != nil {
		t.Fatalf("ListScoped failed: %v", err)
	}
	if len(scoped) != 1 || scoped[0].Scope != "session-abc" {
		t.Fatalf("expected 1 scoped block, got %d", len(scoped))
	}

	// Search
	res, err := client.Search("important")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(res))
	}

	// Stats
	stats, err := client.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if stats.TotalCount != 2 {
		t.Fatalf("expected 2 total blocks in stats, got %d", stats.TotalCount)
	}

	// Forget
	deleted, err := client.Forget("blk-123")
	if err != nil {
		t.Fatalf("Forget failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// 2. Test Remote() singleton probing
	ResetRemoteForTesting()
	origAddr := os.Getenv("GLEANN_REMOTE_ADDR")
	defer os.Setenv("GLEANN_REMOTE_ADDR", origAddr)

	os.Setenv("GLEANN_REMOTE_ADDR", ts.URL)
	c := Remote()
	if c == nil {
		t.Fatalf("expected Remote() to discover server at %s", ts.URL)
	}

	// Disabled via off
	os.Setenv("GLEANN_REMOTE_ADDR", "off")
	ResetRemoteForTesting()
	if cOff := Remote(); cOff != nil {
		t.Fatalf("expected nil when GLEANN_REMOTE_ADDR=off")
	}
}

func TestRemote_RejectsNonGleannServer(t *testing.T) {
	// A server that returns HTTP 200 on /health but is not gleann (e.g. Strata or other daemon)
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok","model":"strata"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer foreign.Close()

	ResetRemoteForTesting()
	origAddr := os.Getenv("GLEANN_REMOTE_ADDR")
	defer os.Setenv("GLEANN_REMOTE_ADDR", origAddr)

	os.Setenv("GLEANN_REMOTE_ADDR", foreign.URL)
	c := Remote()
	if c != nil {
		t.Fatalf("expected Remote() to reject non-gleann server at %s", foreign.URL)
	}
}
func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{":8081", "http://localhost:8081"},
		{"8081", "http://localhost:8081"},
		{"http://localhost:8081/", "http://localhost:8081"},
		{"https://remote.server:9000", "https://remote.server:9000"},
		{"127.0.0.1:8081", "http://127.0.0.1:8081"},
	}
	for _, tc := range cases {
		got := normalizeURL(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeURL(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestStoreLockTimeoutError(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test_lock.db")

	// First holder
	s1, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore 1: %v", err)
	}
	defer s1.Close()

	// Second opener: open with OpenStore
	// Note: OpenStore has 5s timeout; test the timeout wrapping directly.
	_, err = bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("expected second bolt.Open to fail with timeout")
	}
	if !errors.Is(err, bolt.ErrTimeout) && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("unexpected error: %v", err)
	}
}
