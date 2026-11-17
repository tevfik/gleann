package gleann

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/pkg/configpath"
)

// PluginTokenEnv is the environment variable through which gleann hands the
// per-plugin bearer token to a plugin process it launches. The plugin must
// then require "Authorization: Bearer <token>" on every route except /health.
const PluginTokenEnv = "GLEANN_PLUGIN_TOKEN"

// Environment variables that carry a plugin's token to the code paths that
// reach the audio and vision plugins by URL only (see
// ResolveMultimodalPluginEnv).
const (
	AudioPluginTokenEnv  = "GLEANN_AUDIO_PLUGIN_TOKEN"
	VisionPluginTokenEnv = "GLEANN_VISION_PLUGIN_TOKEN"
)

// newPluginToken returns 256 bits of randomness, hex encoded.
func newPluginToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generating plugin token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// pluginTokenPath is where the launch token of a plugin is kept, shared by
// every gleann process of the user (serve, mcp, CLI): a plugin started by one
// of them must stay reachable from the others. The directory follows
// GLEANN_CONFIG_DIR like the rest of gleann.
func pluginTokenPath(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	dir, err := configpath.GetConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating gleann home: %w", err)
	}
	return filepath.Join(dir, "auth", "plugins", name+".token"), nil
}

func readTokenFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// loadOrCreatePluginToken returns the stored token of a plugin, creating it
// (directory 0700, file 0600, written atomically) on first need. Concurrent
// creators converge on one value: the file is published with a hard link,
// which fails if another process got there first.
func loadOrCreatePluginToken(name string) (string, error) {
	path, err := pluginTokenPath(name)
	if err != nil {
		return "", err
	}
	if t := readTokenFile(path); t != "" {
		return t, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	tok, err := newPluginToken()
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, name+".tmp-*") // created 0600
	if err != nil {
		return "", fmt.Errorf("writing plugin token: %w", err)
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.WriteString(tok + "\n")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return "", fmt.Errorf("writing plugin token: %w", err)
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if t := readTokenFile(path); t != "" {
			return t, nil // lost the race; use the winner's token
		}
		// Filesystem without hard links: plain atomic rename.
		if err := os.Rename(tmp.Name(), path); err != nil {
			return "", fmt.Errorf("publishing plugin token: %w", err)
		}
	}
	return readTokenFile(path), nil
}

// TokenFor returns the bearer token to present to p: the token configured in
// plugins.json, else the stored token shared by all gleann processes (also
// when another process launched the plugin), else "" (a plugin that was never
// launched by gleann and has no configured token, e.g. an older build).
func (m *PluginManager) TokenFor(p *Plugin) string {
	if p == nil {
		return ""
	}
	if p.Token != "" {
		return p.Token
	}
	if m != nil {
		m.tokMu.Lock()
		t := m.tokens[p.Name]
		m.tokMu.Unlock()
		if t != "" {
			return t
		}
	}
	path, err := pluginTokenPath(p.Name)
	if err != nil {
		return ""
	}
	t := readTokenFile(path)
	if t != "" && m != nil {
		m.tokMu.Lock()
		if m.tokens == nil {
			m.tokens = map[string]string{}
		}
		m.tokens[p.Name] = t
		m.tokMu.Unlock()
	}
	return t
}

// AuthorizeRequest adds the plugin's bearer token to req, if there is one.
func (m *PluginManager) AuthorizeRequest(req *http.Request, p *Plugin) {
	if t := m.TokenFor(p); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
}

// launchToken picks the token for a process about to be started: the one
// configured in plugins.json, otherwise the stored per-plugin token (created
// on first use). It is remembered for later requests.
func (m *PluginManager) launchToken(p *Plugin) (string, error) {
	tok := p.Token
	if tok == "" {
		var err error
		if tok, err = loadOrCreatePluginToken(p.Name); err != nil {
			return "", err
		}
	}
	m.tokMu.Lock()
	if m.tokens == nil {
		m.tokens = map[string]string{}
	}
	m.tokens[p.Name] = tok
	m.tokMu.Unlock()
	return tok, nil
}

// forgetToken drops the in-memory copy only; the stored token file stays so
// other gleann processes and later launches keep working.
func (m *PluginManager) forgetToken(name string) {
	m.tokMu.Lock()
	delete(m.tokens, name)
	m.tokMu.Unlock()
}

// exportEnvToken publishes p's token in the audio/vision token variables when
// p is the plugin those URL-only code paths will talk to.
func (m *PluginManager) exportEnvToken(p *Plugin) {
	tok := m.TokenFor(p)
	if tok == "" || p.URL == "" {
		return
	}
	url := strings.TrimRight(p.URL, "/")
	for _, c := range []struct{ cap, urlEnv, tokEnv string }{
		{CapAudioTranscription, "GLEANN_AUDIO_PLUGIN_URL", AudioPluginTokenEnv},
		{CapVisionExtraction, "GLEANN_VISION_PLUGIN_URL", VisionPluginTokenEnv},
	} {
		if !hasCap(p, c.cap) || strings.TrimRight(os.Getenv(c.urlEnv), "/") != url {
			continue
		}
		_ = os.Setenv(c.tokEnv, tok)
	}
}

func hasCap(p *Plugin, cap string) bool {
	for _, c := range p.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}
