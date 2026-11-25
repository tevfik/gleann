package llamacpp

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tevfik/gleann/internal/modelcatalog"
	"github.com/tevfik/gleann/modules/settings"
)

// ModelOptions controls ResolveModel. The zero value never downloads.
type ModelOptions struct {
	// AutoDownload is llamacpp_config.auto_download: only when true is a
	// missing catalogue model fetched, in the FOREGROUND (the caller blocks,
	// including an MCP sync), through the same path as 'gleann models
	// download'. False never makes a request.
	AutoDownload bool
	// Progress receives the status and progress lines (nil: silent).
	Progress io.Writer
	// Lookup and Download default to modelcatalog.Find and
	// modelcatalog.Download; they are seams for tests.
	Lookup   func(name string) (modelcatalog.Entry, bool)
	Download func(ctx context.Context, e modelcatalog.Entry, progress func(done, total int64)) error
}

// CatalogEntry maps a configured model name to a catalogue entry: an id
// ("bge-m3"), a file name, or the Ollama-style name the first-run config
// writes ("bge-m3:latest"; the tag is ignored).
func (o ModelOptions) CatalogEntry(name string) (modelcatalog.Entry, bool) {
	lookup := o.Lookup
	if lookup == nil {
		lookup = modelcatalog.Find
	}
	if e, ok := lookup(name); ok {
		return e, true
	}
	if i := strings.Index(name, ":"); i > 0 && !strings.ContainsAny(name, `/\`) {
		return lookup(name[:i])
	}
	return modelcatalog.Entry{}, false
}

// EnsureDownloaded downloads the catalogue model behind name when
// AutoDownload is set and the model is not on disk. Anything else (flag off,
// a file already there, not a catalogue model) is a no-op.
func (o ModelOptions) EnsureDownloaded(ctx context.Context, name string) error {
	if !o.AutoDownload {
		return nil
	}
	if resolved := ResolveModelPath(name); isFile(resolved) {
		return nil
	}
	e, ok := o.CatalogEntry(name)
	if !ok || e.Installed() {
		return nil
	}
	download := o.Download
	if download == nil {
		download = modelcatalog.Download
	}
	w := o.Progress
	if w == nil {
		w = io.Discard
	}
	fmt.Fprintf(w, "⬇  llamacpp_config.auto_download: fetching %s (%s) into %s\n", e.Name, settings.HumanSize(e.Bytes), modelcatalog.Dir())
	err := download(ctx, e, func(done, total int64) {
		if o.Progress != nil && total > 0 {
			fmt.Fprintf(w, "\r   %3d%%  %d / %d MB   ", done*100/total, done>>20, total>>20)
		}
	})
	if o.Progress != nil {
		fmt.Fprintln(w)
	}
	if err != nil {
		return fmt.Errorf("auto_download of %s failed (no partial file was kept; retry, or run 'gleann models download %s'): %w", e.ID, e.ID, err)
	}
	fmt.Fprintf(w, "✓ %s\n", e.Path())
	return nil
}

// ResolveModel turns the configured llamacpp model into the file the runner
// loads, without touching the user's config: it downloads first when opted in
// (EnsureDownloaded), then a file or path is used as is, and a catalogue id
// or the first-run default maps to its installed file. A catalogue model that
// is not installed, and a name that is neither a file nor a catalogue entry,
// give an error that names what to do instead.
func ResolveModel(ctx context.Context, name string, o ModelOptions) (string, error) {
	if name == "" {
		return "", fmt.Errorf("llamacpp provider requires a .gguf model (a path, a file name in %s, or a catalogue id such as 'bge-m3') as the embedding model", modelcatalog.Dir())
	}
	if err := o.EnsureDownloaded(ctx, name); err != nil {
		return "", err
	}
	if resolved := ResolveModelPath(name); isFile(resolved) {
		return resolved, nil
	}
	if e, ok := o.CatalogEntry(name); ok {
		if e.Installed() {
			return e.Path(), nil
		}
		return "", fmt.Errorf("llamacpp model %q is not downloaded yet: run 'gleann models download %s', or set \"llamacpp_config\": {\"auto_download\": true} to fetch it in the foreground on start", name, e.ID)
	}
	if strings.HasSuffix(strings.ToLower(name), ".gguf") || strings.ContainsAny(name, `/\`) {
		return name, nil // a file reference: the runner reports it if missing
	}
	var ids []string
	for _, e := range modelcatalog.OfKind(modelcatalog.Embedding) {
		ids = append(ids, e.ID)
	}
	return "", fmt.Errorf("llamacpp model %q is neither a .gguf file (a path, or a file name in %s) nor a catalogue id (%s; see 'gleann models list'); to keep %q, use embedding_provider \"ollama\" instead", name, modelcatalog.Dir(), strings.Join(ids, ", "), name)
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
