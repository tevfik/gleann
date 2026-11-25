package autosetup

// T1 (v1.22): first run without Ollama. When Ollama cannot be reached,
// gleann informs the user and persists actionable guidance instead of
// downloading anything in the background. Model downloads stay an explicit
// user action ('ollama pull', 'gleann models download') or an explicit
// opt-in (llamacpp_config.auto_download).

import (
	"fmt"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

// downloadExampleID is the model id shown in the 'gleann models download'
// hint: the default embedding model when the catalog carries it, else the
// first catalog embedding model, else the "<id>" placeholder.
func downloadExampleID() string {
	if e, ok := modelcatalog.Find(gleann.DefaultConfig().EmbeddingModel); ok {
		return e.ID
	}
	if es := modelcatalog.OfKind(modelcatalog.Embedding); len(es) > 0 {
		return es[0].ID
	}
	return "<id>"
}

// NoOllamaGuidance is the actionable guidance printed when Ollama is absent
// at first run (EnsureConfig, setup --bootstrap, setup --auto). It states
// that nothing was downloaded in the background and lists the ways to get
// models: install Ollama, use the local llamacpp engine with a manual
// 'gleann models download', or opt in to automatic downloads via
// llamacpp_config.auto_download. It never triggers a download itself.
func NoOllamaGuidance() string {
	return fmt.Sprintf(guidanceFormat, downloadExampleID())
}

const guidanceFormat = `No models were downloaded in the background.
To get models, choose one:
  1. Install Ollama (https://ollama.com/download), run 'ollama serve', then
     'ollama pull bge-m3' and 'ollama pull gemma3:4b' — or re-run
     'gleann setup --bootstrap' to auto-detect them.
  2. Without Ollama: set "embedding_provider": "llamacpp" in ~/.gleann/config.json
     (keep "embedding_model": "%[1]s", gleann maps the catalogue id to the downloaded
     file) and download it once: 'gleann models download %[1]s' (all ids:
     'gleann models list').
  3. Opt in to foreground downloads: with "embedding_provider": "llamacpp", set
     "llamacpp_config": {"auto_download": true} in ~/.gleann/config.json. The next
     time gleann starts (e.g. 'gleann serve') it downloads the missing catalogue
     model named by "embedding_model" right then, with progress and a checksum
     check; Ctrl-C cancels. Never in the background, and never without this flag.`

// NoOllamaNotice is the full first-run notice for an absent Ollama: the host
// line plus NoOllamaGuidance. It is persisted as "setup_notice" in the
// generated config so the guidance survives the terminal session.
func NoOllamaNotice(host string) string {
	return fmt.Sprintf("Ollama not detected at %s.\n%s", host, NoOllamaGuidance())
}
