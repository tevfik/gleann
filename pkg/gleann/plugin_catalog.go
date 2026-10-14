package gleann

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// PluginMeta describes a known plugin from the catalog.
type PluginMeta struct {
	Name               string   `json:"name"`
	Icon               string   `json:"icon"`
	Description        string   `json:"description"`
	RepoURL            string   `json:"repo_url"`
	Language           string   `json:"language"`
	Extensions         []string `json:"extensions"`
	HasSettings        bool     `json:"has_settings"`
	SettingsCmd        []string `json:"settings_cmd"`
	RequiresMarkitdown bool     `json:"requires_markitdown"`
	Version            string   `json:"version,omitempty"` // Latest version available
}

// RegistryURL is the remote URL where the dynamic catalog is hosted.
var RegistryURL = "https://raw.githubusercontent.com/tevfik/gleann/main/plugins-registry.json"

var (
	catalogMu       sync.RWMutex
	cachedCatalog   []PluginMeta
	lastCatalogTime time.Time
	catalogTTL      = 10 * time.Minute
)

// defaultCatalog is the fallback catalog used if fetching from the registry fails.
var defaultCatalog = []PluginMeta{
	{
		Name:               "gleann-plugin-docs",
		Icon:               "📄",
		Description:        "High-performance native document extraction with Granite Docling BitNet 1.58b VLM, OTSL table parsing, and native DOCX.",
		RepoURL:            "https://github.com/tevfik/gleann-plugin-docs",
		Language:           "go (eif-runtime, docling-vlm)",
		Extensions:         []string{".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt", ".png", ".jpg", ".jpeg", ".csv", ".txt", ".md"},
		RequiresMarkitdown: false,
	},
	{
		Name:        "gleann-plugin-marker",
		Icon:        "🖊️",
		Description: "High-accuracy PDF/image extraction via marker-pdf + surya OCR. Heavier than docs; pick it for table-rich PDFs and scanned documents.",
		RepoURL:     "https://github.com/tevfik/gleann-plugin-marker",
		Language:    "python (marker-pdf, surya OCR)",
		Extensions:  []string{".pdf", ".docx", ".xlsx", ".pptx", ".epub", ".html", ".png", ".jpg"},
	},
	{
		Name:        "gleann-plugin-sound",
		Icon:        "🎙️",
		Description: "Audio and video transcription via local Whisper models. Extracts spoken audio into indexed text passages.",
		RepoURL:     "https://github.com/tevfik/gleann-plugin-sound",
		Language:    "go (whisper.cpp / onnxruntime)",
		Extensions:  []string{".wav", ".mp3", ".flac", ".ogg", ".m4a", ".webm", ".mp4", ".mkv"},
		HasSettings: true,
		SettingsCmd: []string{"gleann-plugin-sound", "tui"},
	},
}

// FetchPluginCatalog attempts to load the latest plugin catalog from the remote registry,
// caching results in memory for catalogTTL (10 minutes) to avoid redundant network I/O.
// If it fails (e.g., offline, timeout, or 404), it silently falls back to the default catalog.
func FetchPluginCatalog() []PluginMeta {
	catalogMu.RLock()
	if cachedCatalog != nil && time.Since(lastCatalogTime) < catalogTTL {
		result := make([]PluginMeta, len(cachedCatalog))
		copy(result, cachedCatalog)
		catalogMu.RUnlock()
		return result
	}
	catalogMu.RUnlock()

	catalogMu.Lock()
	defer catalogMu.Unlock()

	// Double check under write lock
	if cachedCatalog != nil && time.Since(lastCatalogTime) < catalogTTL {
		result := make([]PluginMeta, len(cachedCatalog))
		copy(result, cachedCatalog)
		return result
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(RegistryURL)
	if err != nil {
		if cachedCatalog != nil {
			result := make([]PluginMeta, len(cachedCatalog))
			copy(result, cachedCatalog)
			return result
		}
		return copyCatalog(defaultCatalog)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if cachedCatalog != nil {
			result := make([]PluginMeta, len(cachedCatalog))
			copy(result, cachedCatalog)
			return result
		}
		return copyCatalog(defaultCatalog)
	}

	var rawCatalog []*PluginMeta
	err = json.NewDecoder(resp.Body).Decode(&rawCatalog)
	// Drop null and nameless entries (e.g. `[null, {...}]` in a hand-edited
	// registry); a catalog with no usable entries falls back like a bad fetch.
	remoteCatalog := make([]PluginMeta, 0, len(rawCatalog))
	for _, m := range rawCatalog {
		if m != nil && strings.TrimSpace(m.Name) != "" {
			remoteCatalog = append(remoteCatalog, *m)
		}
	}
	if err != nil || len(remoteCatalog) == 0 {
		if cachedCatalog != nil {
			result := make([]PluginMeta, len(cachedCatalog))
			copy(result, cachedCatalog)
			return result
		}
		return copyCatalog(defaultCatalog)
	}

	cachedCatalog = remoteCatalog
	lastCatalogTime = time.Now()

	result := make([]PluginMeta, len(cachedCatalog))
	copy(result, cachedCatalog)
	return result
}

// copyCatalog returns a copy of c so callers cannot mutate the shared
// built-in default catalog (or the cache) through the returned slice.
func copyCatalog(c []PluginMeta) []PluginMeta {
	out := make([]PluginMeta, len(c))
	copy(out, c)
	return out
}

// InvalidatePluginCatalog clears the in-memory catalog cache (useful for tests or forced refresh).
func InvalidatePluginCatalog() {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	cachedCatalog = nil
	lastCatalogTime = time.Time{}
}
