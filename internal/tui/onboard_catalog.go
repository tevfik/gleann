package tui

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/modules/settings"
)

// catalogOptions are the catalog models of a kind that are not on disk yet,
// offered as "(Download) …" next to the local files. Picking one downloads it
// once the setup is saved (downloadChosenModels). asPath stores the full path
// (the eif provider) instead of the file name (llamacpp).
func catalogOptions(kind string, asPath bool, have []ModelInfo) []ModelInfo {
	present := map[string]bool{}
	for _, m := range have {
		present[strings.ToLower(filepath.Base(m.Tag))] = true
		present[strings.ToLower(filepath.Base(m.Name))] = true
	}
	var out []ModelInfo
	for _, e := range modelcatalog.OfKind(kind) {
		if e.Installed() || present[strings.ToLower(e.File)] {
			continue
		}
		tag := e.File
		if asPath {
			tag = e.Path()
		}
		out = append(out, ModelInfo{Name: "(Download) " + e.Name, Tag: tag, Size: settings.HumanSize(e.Bytes)})
	}
	return out
}

// withCatalog appends the downloadable catalog models to a local model list.
func withCatalog(models []ModelInfo, kind string, asPath bool) []ModelInfo {
	return append(models, catalogOptions(kind, asPath, models)...)
}

// catalogEntryFor returns the catalog entry a chosen local model names. Only
// .gguf file names count: a server model such as Ollama's "bge-m3" is not a
// file to download.
func catalogEntryFor(value string) (modelcatalog.Entry, bool) {
	if !strings.HasSuffix(strings.ToLower(value), ".gguf") {
		return modelcatalog.Entry{}, false
	}
	return modelcatalog.Find(filepath.Base(value))
}

// downloadChosenModels downloads the catalog models the saved setup uses but
// that are not on disk yet, reporting to out. It returns whether it did
// anything, so the caller can wait before redrawing the menu.
func downloadChosenModels(ctx context.Context, r OnboardResult, out io.Writer) bool {
	local := func(p string) bool { return p == "llamacpp" || p == "eif" }
	var chosen []string
	if local(r.EmbeddingProvider) {
		chosen = append(chosen, r.EmbeddingModel, r.CodeEmbeddingModel, r.DocEmbeddingModel)
		if r.RerankEnabled {
			chosen = append(chosen, r.RerankModel)
		}
	}
	if local(r.LLMProvider) {
		chosen = append(chosen, r.LLMModel)
	}

	seen := map[string]bool{}
	did := false
	for _, v := range chosen {
		e, ok := catalogEntryFor(v)
		if !ok || e.Installed() || seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		did = true
		fmt.Fprintf(out, "\n⬇  Downloading %s (%s) to %s\n", e.Name, settings.HumanSize(e.Bytes), modelcatalog.Dir())
		err := modelcatalog.Download(ctx, e, func(done, total int64) {
			if total > 0 {
				fmt.Fprintf(out, "\r   %3d%%  %d / %d MB   ", done*100/total, done>>20, total>>20)
			}
		})
		if err != nil {
			fmt.Fprintf(out, "\n✗ %v\n  Download it later from the web UI (System) or with: gleann models download %s\n", err, e.ID)
			continue
		}
		fmt.Fprintf(out, "\n✓ %s\n", e.Path())
	}
	return did
}
