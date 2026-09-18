// Package memory — REST fallback client.
//
// When `gleann serve` runs, it holds an exclusive bbolt lock on
// ~/.gleann/memory/memory.db. Local processes (CLI, MCP server) that attempt
// to open the same database directly will block on bbolt's file lock and
// timeout after 5 seconds.
//
// RemoteClient routes memory operations to the running server's REST API
// (/api/blocks), completely bypassing the bbolt lock contention.
package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRemoteAddr = "http://localhost:8080"
	remoteProbePath   = "/health"
	remoteProbeTOms   = 200
)

// RemoteClient interacts with a running gleann server via REST.
type RemoteClient struct {
	base   string
	client *http.Client
}

var (
	remoteOnce sync.Once
	remoteVal  *RemoteClient
)

// Remote returns a cached RemoteClient if a gleann server is reachable
// at GLEANN_REMOTE_ADDR (default http://localhost:8080), or nil if unreachable
// or disabled via GLEANN_REMOTE_ADDR="off".
func Remote() *RemoteClient {
	remoteOnce.Do(func() {
		addr := os.Getenv("GLEANN_REMOTE_ADDR")
		if addr == "off" {
			return
		}
		if addr == "" {
			addr = DefaultRemoteAddr
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
		remoteVal = &RemoteClient{
			base:   addr,
			client: &http.Client{Timeout: 30 * time.Second},
		}
	})
	return remoteVal
}

// NewRemoteClient creates a RemoteClient pointing to the given base URL.
func NewRemoteClient(baseURL string) *RemoteClient {
	return &RemoteClient{
		base:   strings.TrimRight(baseURL, "/"),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// ResetRemoteForTesting resets the cached RemoteClient singleton.
func ResetRemoteForTesting() {
	remoteOnce = sync.Once{}
	remoteVal = nil
}

// ── Read operations ──────────────────────────────────────────────────────────

// List returns blocks for the specified tier, or all tiers if tier is empty.
func (r *RemoteClient) List(tier Tier) ([]Block, error) {
	u := r.base + "/api/blocks"
	if tier != "" {
		u += "?tier=" + string(tier)
	}
	return r.fetchBlocks(u)
}

// Search performs full-text search across memory blocks.
func (r *RemoteClient) Search(query string) ([]Block, error) {
	u := fmt.Sprintf("%s/api/blocks/search?q=%s", r.base, url.QueryEscape(query))
	return r.fetchBlocks(u)
}

// Stats returns memory store statistics.
func (r *RemoteClient) Stats() (Stats, error) {
	var stats Stats
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

// Context fetches the compiled LLM context window XML.
func (r *RemoteClient) Context(scope string) (string, error) {
	u := r.base + "/api/blocks/context?format=xml"
	if scope != "" {
		u += "&scope=" + url.QueryEscape(scope)
	}
	resp, err := r.client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", decodeError(resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read context: %w", err)
	}
	return string(body), nil
}

// ── Write operations ─────────────────────────────────────────────────────────

// Add creates a new memory block with basic fields.
func (r *RemoteClient) Add(tier Tier, label, content string, tags []string) (*Block, error) {
	return r.AddBlock(&Block{
		Tier:    tier,
		Label:   label,
		Content: content,
		Tags:    tags,
		Source:  "cli",
	})
}

// AddBlock writes a full memory block to the server.
func (r *RemoteClient) AddBlock(b *Block) (*Block, error) {
	body := map[string]any{
		"content":    b.Content,
		"tier":       string(b.Tier),
		"label":      b.Label,
		"tags":       b.Tags,
		"source":     b.Source,
		"metadata":   b.Metadata,
		"char_limit": b.CharLimit,
		"scope":      b.Scope,
	}
	if b.ExpiresAt != nil {
		// Pass duration from now if expires in future.
		remaining := time.Until(*b.ExpiresAt)
		if remaining > 0 {
			body["expires_in"] = remaining.String()
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal block: %w", err)
	}
	resp, err := r.client.Post(r.base+"/api/blocks", "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return nil, decodeError(resp)
	}
	var created Block
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, fmt.Errorf("decode block: %w", err)
	}
	return &created, nil
}

// Forget deletes blocks matching an exact ID or content query string.
func (r *RemoteClient) Forget(idOrQuery string) (int, error) {
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

// Clear removes all blocks in a tier (or all tiers if tier is empty).
func (r *RemoteClient) Clear(tier Tier) (int, error) {
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

// Compact triggers memory compaction on the server, purging expired and unreliable blocks.
func (r *RemoteClient) Compact(minValidity float64) (int, error) {
	u := fmt.Sprintf("%s/api/blocks/compact?min_validity=%f", r.base, minValidity)
	resp, err := r.client.Post(u, "application/json", nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, decodeError(resp)
	}
	var env struct {
		Pruned int `json:"pruned"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, fmt.Errorf("decode compact: %w", err)
	}
	return env.Pruned, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func (r *RemoteClient) fetchBlocks(u string) ([]Block, error) {
	resp, err := r.client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, decodeError(resp)
	}
	var env struct {
		Blocks []Block `json:"blocks"`
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
