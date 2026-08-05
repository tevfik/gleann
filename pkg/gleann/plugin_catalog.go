package gleann

import (
	"encoding/json"
	"net/http"
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

// defaultCatalog is the fallback catalog used if fetching from the registry fails.
var defaultCatalog = []PluginMeta{
	{
		Name:               "gleann-plugin-docs",
		Icon:               "📄",
		Description:        "Document extraction via markitdown/docling (fast, broad format coverage). Best default for mixed corpora.",
		RepoURL:            "https://github.com/tevfik/gleann-plugin-marker",
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
		Icon:        "🔊",
		Description: "Speech-to-text via whisper.cpp / ONNX. Auto-discovered by the multimodal pipeline.",
		RepoURL:     "https://github.com/tevfik/gleann-plugin-sound",
		Language:    "go (whisper.cpp / onnxruntime)",
		Extensions:  []string{".wav", ".mp3", ".flac", ".ogg", ".m4a", ".webm", ".mp4", ".mkv"},
		HasSettings: true,
		SettingsCmd: []string{"gleann-plugin-sound", "tui"},
	},
}

// FetchPluginCatalog attempts to load the latest plugin catalog from the remote registry.
// If it fails (e.g., offline, timeout, or 404), it silently falls back to the default catalog.
func FetchPluginCatalog() []PluginMeta {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(RegistryURL)
	if err != nil {
		return defaultCatalog
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return defaultCatalog
	}

	var remoteCatalog []PluginMeta
	if err := json.NewDecoder(resp.Body).Decode(&remoteCatalog); err != nil {
		return defaultCatalog
	}

	if len(remoteCatalog) == 0 {
		return defaultCatalog
	}

	return remoteCatalog
}
