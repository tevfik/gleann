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

// Plugin defines a registered Gleann plugin
type Plugin struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Command      []string `json:"command"` // Command to run if the plugin is not running
	Capabilities []string `json:"capabilities"`
	Extensions   []string `json:"extensions"`
}

// PluginRegistry holds the currently discovered plugins
type PluginRegistry struct {
	Plugins []Plugin `json:"plugins"`
}

// PluginManager manages auto-started plugins, handling lifecycle and timeouts.
type PluginManager struct {
	Registry   *PluginRegistry
	activeCmds map[string]*exec.Cmd
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
	}, nil
}

// Close gracefully stops any plugins that were spawned by this manager.
func (m *PluginManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cmd := range m.activeCmds {
		if cmd.Process != nil {
			cmd.Process.Signal(os.Interrupt)
			time.AfterFunc(2*time.Second, func() { cmd.Process.Kill() })
		}
	}
	m.activeCmds = make(map[string]*exec.Cmd)
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

// Process sends a file to a plugin's /convert endpoint and returns the markdown result.
func (m *PluginManager) Process(plugin *Plugin, filePath string) (string, error) {
	if err := m.EnsurePluginRunning(plugin); err != nil {
		return "", fmt.Errorf("ensure plugin %s running: %w", plugin.Name, err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file for plugin: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy file to form: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close multipart writer: %w", err)
	}

	endpoint := plugin.URL
	if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}
	endpoint += "convert"

	req, err := http.NewRequest("POST", endpoint, body)
	if err != nil {
		return "", fmt.Errorf("new http request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// 30 second strict timeout for plugin parsing
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("plugin request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("plugin returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Markdown string `json:"markdown"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode plugin response: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("plugin logic error: %s", result.Error)
	}

	return result.Markdown, nil
}
