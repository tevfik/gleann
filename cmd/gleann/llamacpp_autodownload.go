package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/tevfik/gleann/internal/backend/llamacpp"
	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/pkg/gleann"
)

// llamaCatalogLookup finds a catalogue entry by id or file name; a seam for
// tests, which point it at an httptest model server.
var llamaCatalogLookup = modelcatalog.Find

// llamaModelOptions are the shared resolver options (internal/backend/llamacpp)
// for this config: the opt-in flag, progress on stderr unless quiet, and the
// CLI's test seams.
func llamaModelOptions(config *gleann.Config, quiet bool) llamacpp.ModelOptions {
	o := llamacpp.ModelOptions{
		AutoDownload: config.LlamaCPPConfig.AutoDownload,
		Lookup:       llamaCatalogLookup,
		Download:     downloadModel,
	}
	if !quiet {
		o.Progress = os.Stderr
	}
	return o
}

// ensureLlamaCPPModels is the explicit opt-in download: only when
// llamacpp_config.auto_download is true, a configured llamacpp embedding model
// (and llamacpp LLM) that is missing is downloaded in the FOREGROUND through
// the same catalogue path as `gleann models download` (size and sha256
// checked, a unique temp file renamed into place only when complete). Ctrl-C
// or ctx cancels it and leaves no file. With the flag false or unset nothing
// is ever fetched, and nothing is fetched in the background. Resolving the
// model to a file is llamacpp.ResolveModel's job.
func ensureLlamaCPPModels(ctx context.Context, config *gleann.Config, quiet bool) error {
	if !config.LlamaCPPConfig.AutoDownload {
		return nil
	}
	var models []string
	if config.EmbeddingProvider == "llamacpp" && config.EmbeddingModel != "" {
		models = append(models, config.EmbeddingModel)
	}
	if config.LLMProvider == "llamacpp" && config.LLMModel != "" {
		models = append(models, config.LLMModel)
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	o := llamaModelOptions(config, quiet)
	for _, m := range models {
		if err := o.EnsureDownloaded(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// resolveLlamaCPPModel is the CLI's view of llamacpp.ResolveModel (no
// download: ensureLlamaCPPModels ran before).
func resolveLlamaCPPModel(name string) (string, error) {
	return llamacpp.ResolveModel(context.Background(), name, llamacpp.ModelOptions{Lookup: llamaCatalogLookup})
}

// catalogEntryFor is kept for the tests of the first-run default.
func catalogEntryFor(name string) (modelcatalog.Entry, bool) {
	return llamacpp.ModelOptions{Lookup: llamaCatalogLookup}.CatalogEntry(name)
}
