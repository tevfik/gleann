// Memory CLI ↔ running server fallback.
//
// When `gleann serve` is running it holds an exclusive bbolt lock on
// ~/.gleann/memory/memory.db. Any local CLI call that tries to open the
// same database deadlocks for 5 s and then fails with `open memory db:
// timeout`. To make the CLI robust in that situation we probe a running
// server at the configured address and — when reachable — route the
// affected memory commands through the REST surface that is already
// exposed at /api/blocks.
//
// The probe is intentionally cheap (200 ms GET /health) and per-process
// cached, so commands that don't need REST never pay the cost. The
// address can be overridden via GLEANN_REMOTE_ADDR (default
// http://localhost:8080); set GLEANN_REMOTE_ADDR=off to force local
// bbolt access regardless.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tevfik/gleann/pkg/memory"
)

const (
	defaultRemoteAddr = "http://localhost:8080"
	remoteProbePath   = "/health"
	remoteProbeTOms   = 200
)

type memoryRemote struct {
	base   string
	client *http.Client
}

var (
	remoteOnce sync.Once
	remoteVal  *memoryRemote
)

// remoteMemoryClient returns a REST-backed shim if a gleann server is
// reachable, otherwise nil. The result is cached for the lifetime of the
// CLI process.
func remoteMemoryClient() *memoryRemote {
	remoteOnce.Do(func() {
		addr := os.Getenv("GLEANN_REMOTE_ADDR")
		if addr == "off" {
			return
		}
		if addr == "" {
			addr = defaultRemoteAddr
		}
		addr = strings.TrimRight(addr, "/")

		client := &http.Client{Timeout: remoteProbeTOms * time.Millisecond}
		resp, err := client.Get(addr + remoteProbePath)
		if err != nil {
			return
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		// Full-fat client gets a longer timeout for the real ops.
		remoteVal = &memoryRemote{
			base:   addr,
			client: &http.Client{Timeout: 30 * time.Second},
		}
	})
	return remoteVal
}

// ── Read ─────────────────────────────────────────────────────────────────────

// List mirrors *memory.Manager.List(tier). When tier == "" all tiers are
// returned. The server returns an envelope {blocks, count}; we unwrap.
func (r *memoryRemote) List(tier memory.Tier) ([]memory.Block, error) {
	u := r.base + "/api/blocks"
	if tier != "" {
		u += "?tier=" + string(tier)
	}
	return r.fetchBlocks(u)
}

func (r *memoryRemote) Search(query string) ([]memory.Block, error) {
	u := fmt.Sprintf("%s/api/blocks/search?q=%s", r.base, url.QueryEscape(query))
	return r.fetchBlocks(u)
}

func (r *memoryRemote) Stats() (memory.Stats, error) {
	var stats memory.Stats
	resp, err := r.client.Get(r.base + "/api/blocks/stats")
	if err != nil {
		return stats, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return stats, decodeError(resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return stats, fmt.Errorf("decode stats: %w", err)
	}
	return stats, nil
}

// ── Write ────────────────────────────────────────────────────────────────────

// Add mirrors *memory.Manager.AddNote / Remember by writing through the
// REST surface. Tier "" defaults to long-term on the server side.
func (r *memoryRemote) Add(tier memory.Tier, label, content string, tags []string) (*memory.Block, error) {
	body := map[string]any{
		"content": content,
		"tier":    string(tier),
		"label":   label,
		"tags":    tags,
		"source":  "cli",
	}
	raw, _ := json.Marshal(body)
	resp, err := r.client.Post(r.base+"/api/blocks", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, decodeError(resp)
	}
	var b memory.Block
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return nil, fmt.Errorf("decode block: %w", err)
	}
	return &b, nil
}

// Forget accepts either an exact block ID or a content-match query — the
// server-side handler maps both via memory.Manager.Forget. Returns the
// number of blocks deleted.
func (r *memoryRemote) Forget(idOrQuery string) (int, error) {
	u := fmt.Sprintf("%s/api/blocks/%s", r.base, url.PathEscape(idOrQuery))
	req, err := http.NewRequest(http.MethodDelete, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, decodeError(resp)
	}
	var env struct {
		Deleted int `json:"deleted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, fmt.Errorf("decode delete: %w", err)
	}
	return env.Deleted, nil
}

func (r *memoryRemote) Clear(tier memory.Tier) (int, error) {
	u := r.base + "/api/blocks"
	if tier != "" {
		u += "?tier=" + string(tier)
	}
	req, err := http.NewRequest(http.MethodDelete, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, decodeError(resp)
	}
	var env struct {
		Deleted int `json:"deleted"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, fmt.Errorf("decode clear: %w", err)
	}
	return env.Deleted, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func (r *memoryRemote) fetchBlocks(u string) ([]memory.Block, error) {
	resp, err := r.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	var env struct {
		Blocks []memory.Block `json:"blocks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode blocks: %w", err)
	}
	return env.Blocks, nil
}

func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var env struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && env.Error != "" {
		return fmt.Errorf("server %d: %s", resp.StatusCode, env.Error)
	}
	return fmt.Errorf("server %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
}
