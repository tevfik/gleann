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
	"syscall"
	"time"
)

// DefaultPluginTimeout is the default HTTP timeout for plugin requests in seconds.
const DefaultPluginTimeout = 120

// Well-known capability names. Plugins declare these in their /info.capabilities
// list and in their entry in ~/.gleann/plugins.json. The core dispatch
// (FindByCapability) is keyed on the exact string match.
const (
	CapDocumentExtraction = "document-extraction"
	CapAudioTranscription = "audio-transcription"
	CapVisionExtraction   = "vision-extraction"
)

// Plugin defines a registered Gleann plugin
type Plugin struct {
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Command      []string `json:"command"` // Command to run if the plugin is not running
	Capabilities []string `json:"capabilities"`
	Extensions   []string `json:"extensions"`
	Timeout      int      `json:"timeout,omitempty"` // HTTP timeout in seconds (0 = DefaultPluginTimeout)

	// Version of the plugin binary/package as reported by its own install
	// step (e.g. "0.2.0"). Empty for plugins built before schema v2.
	Version string `json:"version,omitempty"`

	// SchemaVersion of the plugin HTTP protocol this entry was built
	// against. The manager refuses to call schema versions newer than
	// MaxSupportedSchemaVersion to avoid silent JSON-shape mismatches.
	SchemaVersion int `json:"schema_version,omitempty"`

	// Priority orders multiple plugins that claim the same capability+ext.
	// Higher wins. Zero (default) preserves legacy first-match behaviour.
	Priority int `json:"priority,omitempty"`
}

// MaxSupportedSchemaVersion is the highest plugin protocol version this
// build of Gleann knows how to parse.
const MaxSupportedSchemaVersion = 2

// PluginRegistry holds the currently discovered plugins
type PluginRegistry struct {
	Plugins []Plugin `json:"plugins"`
}

// PluginManager manages auto-started plugins, handling lifecycle and timeouts.
type PluginManager struct {
	Registry   *PluginRegistry
	activeCmds map[string]*exec.Cmd
	logFiles   map[string]*os.File
	// exited holds, per spawned command, a channel closed once cmd.Wait
	// returns. Every started process is reaped by a dedicated goroutine so
	// plugins that exit on their own never linger as zombies.
	exited map[*exec.Cmd]chan struct{}
	// declared remembers each plugin's /health timeout: a plugin busy with a
	// long conversion may be too slow to answer the next health probe.
	declared map[string]int
	mu       sync.Mutex
}

// pluginStartTimeout bounds how long EnsurePluginRunning waits for an
// auto-started plugin to become healthy. Variable so tests can shorten it.
var pluginStartTimeout = 10 * time.Second

// pluginStopGrace is how long a plugin gets to exit after SIGINT before it
// is killed, and how long we then wait for the kill to be reaped.
var pluginStopGrace = 2 * time.Second

// pluginStartedHook, when non-nil, is called after a plugin process has been
// spawned with the channel that is closed once it is reaped. Test-only.
var pluginStartedHook func(cmd *exec.Cmd, exited <-chan struct{})

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
	if registry.Plugins == nil {
		registry.Plugins = make([]Plugin, 0)
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
// Each process is sent SIGINT, given pluginStopGrace to exit, then killed;
// Close waits (bounded) for the processes to be reaped.
func (m *PluginManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	type running struct {
		proc *os.Process
		done chan struct{}
	}
	var procs []running
	for _, cmd := range m.activeCmds {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		done := m.exited[cmd]
		if done == nil {
			// Not started by EnsurePluginRunning (no reaper goroutine);
			// nothing to wait on, just make sure it is gone.
			_ = cmd.Process.Kill()
			continue
		}
		_ = cmd.Process.Signal(os.Interrupt)
		procs = append(procs, running{cmd.Process, done})
	}

	deadline := time.After(pluginStopGrace)
	for _, p := range procs {
		select {
		case <-p.done:
		case <-deadline:
			deadline = nil // expired: stop waiting for the rest gracefully
		}
		if deadline == nil {
			break
		}
	}
	killWait := time.After(pluginStopGrace)
	for _, p := range procs {
		select {
		case <-p.done:
			continue
		default:
		}
		_ = p.proc.Kill()
		select {
		case <-p.done:
		case <-killWait:
		}
	}

	for _, f := range m.logFiles {
		if f != nil {
			f.Close()
		}
	}
	m.activeCmds = make(map[string]*exec.Cmd)
	m.logFiles = make(map[string]*os.File)
	m.exited = make(map[*exec.Cmd]chan struct{})
}

// stopPluginLocked stops (if still running) and forgets the process tracked
// for name, and closes its log file. Caller must hold m.mu.
func (m *PluginManager) stopPluginLocked(name string) {
	if cmd := m.activeCmds[name]; cmd != nil {
		if cmd.Process != nil {
			done := m.exited[cmd]
			select {
			case <-done: // already exited and reaped (nil done never matches)
			default:
				_ = cmd.Process.Kill()
				if done != nil {
					select {
					case <-done:
					case <-time.After(pluginStopGrace):
					}
				}
			}
		}
		delete(m.exited, cmd)
	}
	delete(m.activeCmds, name)
	if f := m.logFiles[name]; f != nil {
		f.Close()
	}
	delete(m.logFiles, name)
}

// deprecatedMediaExts lists media extensions (audio) not handled by document extraction plugins.
// Plugins targeting audio are deprecated in favor of CapAudioTranscription or --multimodal-model.
var deprecatedMediaExts = map[string]bool{
	".mp3": true, ".wav": true, ".flac": true, ".ogg": true,
	".m4a": true, ".aac": true, ".wma": true, ".opus": true,
}

// FindByCapability returns the highest-priority plugin advertising `cap`
// that also supports `ext` (when `ext` is non-empty). Ties are broken by
// registration order so the legacy first-match behaviour is preserved for
// plugins that don't set Priority. SchemaVersion is respected: plugins
// declaring a version newer than MaxSupportedSchemaVersion are skipped.
func (m *PluginManager) FindByCapability(cap, ext string) *Plugin {
	if m == nil || m.Registry == nil {
		return nil
	}
	ext = strings.ToLower(ext)
	var best *Plugin
	bestPrio := -1 << 30
	for i := range m.Registry.Plugins {
		p := &m.Registry.Plugins[i]
		if p.SchemaVersion > MaxSupportedSchemaVersion {
			continue
		}
		hasCap := false
		for _, c := range p.Capabilities {
			if c == cap {
				hasCap = true
				break
			}
		}
		if !hasCap {
			continue
		}
		if ext != "" && len(p.Extensions) > 0 {
			matched := false
			for _, e := range p.Extensions {
				if strings.ToLower(e) == ext {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if p.Priority > bestPrio {
			best = p
			bestPrio = p.Priority
		}
	}
	return best
}

// PluginInfo mirrors the JSON returned by a plugin's GET /info endpoint.
type PluginInfo struct {
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	SchemaVersion int      `json:"schema_version"`
	Capabilities  []string `json:"capabilities"`
	Extensions    []string `json:"extensions"`
}

// ResolveMultimodalPluginEnv populates the GLEANN_AUDIO_PLUGIN_URL and
// GLEANN_VISION_PLUGIN_URL environment variables from the plugin registry
// if the user has not already set them. This is what makes plugin
// installation "just work" with `gleann build` — once gleann-plugin-sound
// or gleann-plugin-vision is registered, no extra config is needed.
//
// Existing env values always win (so users can still override or set
// "off" to disable). Returns the URLs that were chosen (empty if none).
func (m *PluginManager) ResolveMultimodalPluginEnv() (audioURL, visionURL string) {
	if m == nil || m.Registry == nil {
		return "", ""
	}
	audioURL = os.Getenv("GLEANN_AUDIO_PLUGIN_URL")
	if audioURL == "" {
		if p := m.FindByCapability(CapAudioTranscription, ""); p != nil && p.URL != "" {
			audioURL = p.URL
			_ = os.Setenv("GLEANN_AUDIO_PLUGIN_URL", audioURL)
		}
	}
	visionURL = os.Getenv("GLEANN_VISION_PLUGIN_URL")
	if visionURL == "" {
		if p := m.FindByCapability(CapVisionExtraction, ""); p != nil && p.URL != "" {
			visionURL = p.URL
			_ = os.Setenv("GLEANN_VISION_PLUGIN_URL", visionURL)
		}
	}
	if os.Getenv("GLEANN_DOCLING_VLM") == "" {
		home, _ := os.UserHomeDir()
		doclingModel := filepath.Join(home, ".gleann", "models", "granite-docling", "granite_docling_bitnet_dense.eifm")
		if _, err := os.Stat(doclingModel); err == nil {
			_ = os.Setenv("GLEANN_DOCLING_VLM", "1")
		}
	}
	return audioURL, visionURL
}

// FetchPluginInfo issues a GET /info to the plugin and returns the parsed
// metadata. Used by the TUI and `gleann doctor` to report the live version
// and schema_version of a running plugin (vs the registry snapshot).
func (m *PluginManager) FetchPluginInfo(p *Plugin) (*PluginInfo, error) {
	if p == nil || p.URL == "" {
		return nil, fmt.Errorf("plugin has no URL")
	}
	url := strings.TrimRight(p.URL, "/") + "/info"
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugin /info returned %d", resp.StatusCode)
	}
	var info PluginInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode /info: %w", err)
	}
	return &info, nil
}

// PluginHealth is a tiny subset of the plugin /health response that the
// core needs. Plugins are free to add extra fields.
type PluginHealth struct {
	Status        string `json:"status"`
	Ready         bool   `json:"ready"`
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	// Timeout is the longest request the plugin says it may need, in seconds.
	Timeout int `json:"timeout,omitempty"`
}

// PingPlugin issues a GET /health with a short timeout and returns the
// parsed response. Used by the TUI to render "● Running" vs "● Installed".
func (m *PluginManager) PingPlugin(p *Plugin) (*PluginHealth, error) {
	if p == nil || p.URL == "" {
		return nil, fmt.Errorf("plugin has no URL")
	}
	url := strings.TrimRight(p.URL, "/") + "/health"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("plugin /health returned %d", resp.StatusCode)
	}
	var h PluginHealth
	// Best-effort decode; older plugins may return a different shape.
	_ = json.NewDecoder(resp.Body).Decode(&h)
	if h.Status == "" {
		h.Status = "ok"
	}
	return &h, nil
}

// requestTimeout returns the HTTP timeout in seconds for one /convert call. The
// registry value is written at install time and goes stale: a plugin that
// gained OCR still carries the old 120 s, which kills every multi-page scan. The
// plugin's own /health timeout wins when it is larger.
func (m *PluginManager) requestTimeout(p *Plugin) int {
	timeout := DefaultPluginTimeout
	if p.Timeout > 0 {
		timeout = p.Timeout
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if h, err := m.PingPlugin(p); err == nil {
		if m.declared == nil {
			m.declared = map[string]int{}
		}
		m.declared[p.Name] = h.Timeout
	}
	if d := m.declared[p.Name]; d > timeout {
		timeout = d
	}
	return timeout
}

// FindDocumentExtractor returns the highest-priority plugin capable of extracting the given extension.
func (m *PluginManager) FindDocumentExtractor(ext string) *Plugin {
	if deprecatedMediaExts[strings.ToLower(ext)] {
		return nil
	}
	return m.FindByCapability(CapDocumentExtraction, ext)
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
	client = &http.Client{Timeout: 2 * time.Second}
	resp, err = client.Get(healthURL)
	if err == nil {
		resp.Body.Close()
		return nil
	}

	// If the plugin process was started by us and is still running, do not kill it
	// just because it's temporarily busy processing an extraction under heavy CPU load.
	if existing := m.activeCmds[p.Name]; existing != nil && existing.Process != nil {
		if err := existing.Process.Signal(syscall.Signal(0)); err == nil {
			// Process is alive and running
			return nil
		}
	}

	if len(p.Command) == 0 {
		return fmt.Errorf("plugin %s is down and has no auto-start command defined", p.Name)
	}

	if m.activeCmds == nil {
		m.activeCmds = make(map[string]*exec.Cmd)
	}
	if m.logFiles == nil {
		m.logFiles = make(map[string]*os.File)
	}
	if m.exited == nil {
		m.exited = make(map[*exec.Cmd]chan struct{})
	}

	// A previous instance (crashed, or never became healthy) may still be
	// tracked: stop it and close its log file before starting a new one.
	m.stopPluginLocked(p.Name)

	cmd := exec.Command(p.Command[0], p.Command[1:]...)

	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".gleann", "logs")
	os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(logDir, p.Name+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		logFile = nil
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("failed to auto-start plugin %s: %w", p.Name, err)
	}

	// Reap the process whenever it exits so it never becomes a zombie.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	m.activeCmds[p.Name] = cmd
	m.exited[cmd] = done
	if pluginStartedHook != nil {
		pluginStartedHook(cmd, done)
	}
	if logFile != nil {
		m.logFiles[p.Name] = logFile
	}

	// Poll until healthy (or the process exits early).
	timeout := time.Now().Add(pluginStartTimeout)
	for time.Now().Before(timeout) {
		select {
		case <-done:
			m.stopPluginLocked(p.Name)
			return fmt.Errorf("plugin %s exited during startup. Check logs at %s", p.Name, filepath.Join(logDir, p.Name+".log"))
		case <-time.After(500 * time.Millisecond):
		}
		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			return nil
		}
	}

	// Never became healthy: kill it and stop tracking it.
	m.stopPluginLocked(p.Name)
	return fmt.Errorf("plugin %s did not become healthy after %s. Check logs at %s", p.Name, pluginStartTimeout, filepath.Join(logDir, p.Name+".log"))
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

	client := &http.Client{Timeout: time.Duration(m.requestTimeout(plugin)) * time.Second}
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

// GetInstalledPluginVersions returns a map of registered plugin names and their versions.
func GetInstalledPluginVersions() map[string]string {
	reg, err := LoadPlugins()
	if err != nil || reg == nil || len(reg.Plugins) == 0 {
		return nil
	}
	versions := make(map[string]string, len(reg.Plugins))
	for _, p := range reg.Plugins {
		if p.Name != "" {
			v := p.Version
			if v == "" {
				v = "v1-legacy"
			}
			versions[p.Name] = v
		}
	}
	return versions
}
