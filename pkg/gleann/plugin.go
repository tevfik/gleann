package gleann

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultPluginTimeout is the default HTTP timeout for plugin requests in seconds.
const DefaultPluginTimeout = 120

// Plugin defines a registered Gleann plugin
type Plugin struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Command      []string `json:"command"` // Command to run if the plugin is not running
	Capabilities []string `json:"capabilities"`
	Extensions   []string `json:"extensions"`
	Timeout      int      `json:"timeout,omitempty"` // HTTP timeout in seconds (0 = DefaultPluginTimeout)
}

// PluginRegistry holds the currently discovered plugins
type PluginRegistry struct {
	Plugins []Plugin `json:"plugins"`
}

// PluginManager manages auto-started plugins, handling lifecycle and timeouts.
type PluginManager struct {
	Registry   *PluginRegistry
	activeCmds map[string]*exec.Cmd
	logFiles   map[string]*os.File
	mu         sync.Mutex
}

// LoadPlugins reads the plugin registry from ~/.gleann/plugins.json
// (Kept for compatibility, but NewPluginManager is preferred for lifecycle management)
func LoadPlugins() (*PluginRegistry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("user home dir: %w", err)
	}

	pluginFile := filepath.Join(home, ".gleann", "plugins.json")
	data, err := os.ReadFile(pluginFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &PluginRegistry{}, nil // No plugins registered yet
		}
		return nil, fmt.Errorf("read plugins file: %w", err)
	}

	var registry PluginRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse plugins file: %w", err)
	}

	return &registry, nil
}

// NewPluginManager loads the registry and returns a new manager instance.
func NewPluginManager() (*PluginManager, error) {
	reg, err := LoadPlugins()
	if err != nil {
		return nil, err
	}
	return &PluginManager{
		Registry:   reg,
		activeCmds: make(map[string]*exec.Cmd),
		logFiles:   make(map[string]*os.File),
	}, nil
}

// Close gracefully stops any plugins that were spawned by this manager.
func (m *PluginManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cmd := range m.activeCmds {
		if cmd.Process != nil {
			proc := cmd.Process // capture by value to avoid closure race
			_ = proc.Signal(os.Interrupt)
			time.AfterFunc(2*time.Second, func() { _ = proc.Kill() })
		}
	}
	for _, f := range m.logFiles {
		f.Close()
	}
	m.activeCmds = make(map[string]*exec.Cmd)
	m.logFiles = make(map[string]*os.File)
}

// FindDocumentExtractor returns the first plugin capable of extracting the given extension.
func (m *PluginManager) FindDocumentExtractor(ext string) *Plugin {
	if m.Registry == nil {
		return nil
	}
	ext = strings.ToLower(ext)
	for _, p := range m.Registry.Plugins {
		hasCap := false
		for _, cap := range p.Capabilities {
			if cap == "document-extraction" {
				hasCap = true
				break
			}
		}
		if !hasCap {
			continue
		}
		for _, supportedExt := range p.Extensions {
			if strings.ToLower(supportedExt) == ext {
				return &p
			}
		}
	}
	return nil
}

// EnsurePluginRunning pings the plugin's /health and starts it via exec.Cmd if down.
func (m *PluginManager) EnsurePluginRunning(p *Plugin) error {
	healthURL := p.URL
	if !strings.HasSuffix(healthURL, "/") {
		healthURL += "/"
	}
	healthURL += "health"

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(healthURL)
	if err == nil {
		resp.Body.Close()
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double check under lock
	resp, err = client.Get(healthURL)
	if err == nil {
		resp.Body.Close()
		return nil
	}

	if len(p.Command) == 0 {
		return fmt.Errorf("plugin %s is down and has no auto-start command defined", p.Name)
	}

	cmd := exec.Command(p.Command[0], p.Command[1:]...)

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".gleann", "logs")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(logDir, p.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		m.logFiles[p.Name] = logFile
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to auto-start plugin %s: %w", p.Name, err)
	}

	m.activeCmds[p.Name] = cmd

	// Poll until healthy
	timeout := time.Now().Add(10 * time.Second)
	for time.Now().Before(timeout) {
		time.Sleep(500 * time.Millisecond)
		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			return nil
		}
	}

	return fmt.Errorf("plugin %s did not become healthy after 10s. Check logs at %s", p.Name, filepath.Join(logDir, p.Name+".log"))
}

// PluginNode represents a graph node returned by a structured plugin.
type PluginNode struct {
	Type string         `json:"_type"` // "Document" or "Section"
	Data map[string]any `json:"-"`     // all other fields
}

// PluginEdge represents a graph edge returned by a structured plugin.
type PluginEdge struct {
	Type string `json:"_type"` // "HAS_SECTION", "HAS_SUBSECTION"
	From string `json:"from"`
	To   string `json:"to"`
}

// PluginResult holds the graph-ready response from a structured plugin.
type PluginResult struct {
	Nodes    []PluginNode `json:"nodes"`
	Edges    []PluginEdge `json:"edges"`
	Markdown string       `json:"markdown,omitempty"` // Raw markdown fallback (e.g. from markitdown)
}

// ProcessStructured sends a file to a plugin's /convert endpoint and returns
// graph-ready nodes and edges, following the same pattern as the AST code indexer.
func (m *PluginManager) ProcessStructured(plugin *Plugin, filePath string) (*PluginResult, error) {
	raw, err := m.processRaw(plugin, filePath)
	if err != nil {
		return nil, err
	}

	if errMsg, ok := raw["error"].(string); ok && errMsg != "" {
		return nil, fmt.Errorf("plugin logic error: %s", errMsg)
	}

	result := &PluginResult{}

	// Capture raw markdown if present (markitdown backend).
	if md, ok := raw["markdown"].(string); ok {
		result.Markdown = md
	}

	// Parse nodes
	if rawNodes, ok := raw["nodes"].([]any); ok {
		for _, rn := range rawNodes {
			nodeMap, ok := rn.(map[string]any)
			if !ok {
				continue
			}
			nodeType, _ := nodeMap["_type"].(string)
			if nodeType == "" {
				continue
			}
			node := PluginNode{
				Type: nodeType,
				Data: nodeMap,
			}
			result.Nodes = append(result.Nodes, node)
		}
	}

	// Parse edges
	if rawEdges, ok := raw["edges"].([]any); ok {
		for _, re := range rawEdges {
			edgeMap, ok := re.(map[string]any)
			if !ok {
				continue
			}
			edgeType, _ := edgeMap["_type"].(string)
			from, _ := edgeMap["from"].(string)
			to, _ := edgeMap["to"].(string)
			if edgeType == "" || from == "" || to == "" {
				continue
			}
			result.Edges = append(result.Edges, PluginEdge{
				Type: edgeType,
				From: from,
				To:   to,
			})
		}
	}

	return result, nil
}

// processRaw is the internal HTTP logic for ProcessStructured.
func (m *PluginManager) processRaw(plugin *Plugin, filePath string) (map[string]any, error) {
	if err := m.EnsurePluginRunning(plugin); err != nil {
		return nil, fmt.Errorf("ensure plugin %s running: %w", plugin.Name, err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file for plugin: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("copy file to form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	endpoint := plugin.URL
	if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}
	endpoint += "convert"

	req, err := http.NewRequest("POST", endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("new http request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	timeout := DefaultPluginTimeout
	if plugin.Timeout > 0 {
		timeout = plugin.Timeout
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("plugin returned status %d (body unreadable: %v)", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("plugin returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode plugin response: %w", err)
	}

	return result, nil
}
