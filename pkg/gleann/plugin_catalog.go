package gleann

import (
	"encoding/json"
	"net/http"
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
		Description:        "Document extraction via markitdown/docling (fast, broad format coverage). Best default for mixed corpora.",
		RepoURL:            "https://github.com/tevfik/gleann-plugin-docs",
		Language:           "python (markitdown, docling)",
		Extensions:         []string{".pdf", ".docx", ".xlsx", ".pptx", ".csv"},
		RequiresMarkitdown: true,
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
		return defaultCatalog
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if cachedCatalog != nil {
			result := make([]PluginMeta, len(cachedCatalog))
			copy(result, cachedCatalog)
			return result
		}
		return defaultCatalog
	}

	var remoteCatalog []PluginMeta
	if err := json.NewDecoder(resp.Body).Decode(&remoteCatalog); err != nil || len(remoteCatalog) == 0 {
		if cachedCatalog != nil {
			result := make([]PluginMeta, len(cachedCatalog))
			copy(result, cachedCatalog)
			return result
		}
		return defaultCatalog
	}

	cachedCatalog = remoteCatalog
	lastCatalogTime = time.Now()

	result := make([]PluginMeta, len(cachedCatalog))
	copy(result, cachedCatalog)
	return result
}

// InvalidatePluginCatalog clears the in-memory catalog cache (useful for tests or forced refresh).
func InvalidatePluginCatalog() {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	cachedCatalog = nil
	lastCatalogTime = time.Time{}
}
