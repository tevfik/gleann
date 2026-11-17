package indexing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tevfik/gleann/pkg/gleann"
)

// MigrateOptions configures Migrate.
type MigrateOptions struct {
	Options
	// Name of the index to convert. Required.
	Name string
	// Graph builds the code graph even when the old index had none.
	Graph bool
}

// MigrateResult describes a completed migration.
type MigrateResult struct {
	Name string
	// Store is what the passage conversion did; nil when the index already
	// was format 2.
	Store *gleann.MigrateResult
	// AlreadyFormat2 is true when there was nothing to convert.
	AlreadyFormat2 bool
	// GraphBuilt is true when the code graph was rebuilt from the sources.
	GraphBuilt bool
	// MemoryVectors is the conversion of <name>_memory_vectors, when that
	// index existed in the old format.
	MemoryVectors *gleann.MigrateResult
}

// Migrate converts an index in the old store format to format 2 (RFC 004
// B3): passages are copied and the vectors reused (gleann.MigrateIndex),
// then the code graph is rebuilt through the normal document pipeline from
// meta.SourceDir when the old index had a Kùzu graph (<name>_graph) or
// opts.Graph asks for one, and <name>_memory_vectors is converted too. An
// index that already is format 2 is reported as such (its graph is still
// built when opts.Graph is set).
func Migrate(ctx context.Context, opts MigrateOptions) (*MigrateResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Name == "" {
		return nil, errors.New("index name required")
	}
	cfg := opts.Config
	res := &MigrateResult{Name: opts.Name}

	sr, err := gleann.MigrateIndex(cfg.IndexDir, opts.Name, gleann.MigrateOptions{Config: &cfg})
	switch {
	case errors.Is(err, gleann.ErrAlreadyFormat2):
		res.AlreadyFormat2 = true
	case err != nil:
		return nil, err
	default:
		res.Store = sr
	}

	// The memory vector index of a memory-enabled index is a separate index.
	memName := opts.Name + "_memory_vectors"
	if gleann.DetectStoreFormat(filepath.Join(cfg.IndexDir, memName, memName)) == gleann.StoreOld {
		mr, err := gleann.MigrateIndex(cfg.IndexDir, memName, gleann.MigrateOptions{Config: &cfg})
		if err != nil {
			return res, fmt.Errorf("migrating %s: %w", memName, err)
		}
		res.MemoryVectors = mr
	}

	wantGraph := opts.Graph || (res.Store != nil && res.Store.HadGraph)
	if !wantGraph {
		return res, nil
	}
	if err := rebuildGraphFromSources(ctx, opts.Name, &opts.Options); err != nil {
		return res, fmt.Errorf("%w: %v", ErrGraphUpdate, err)
	}
	res.GraphBuilt = true
	return res, nil
}

// rebuildGraphFromSources builds the code graph of a format-2 index from its
// recorded source directory, reading the documents the way a build does (so
// plugin documents become document nodes) without embedding anything.
func rebuildGraphFromSources(ctx context.Context, name string, opts *Options) error {
	cfg := opts.Config
	meta, err := gleann.GetIndexMeta(cfg.IndexDir, name)
	if err != nil {
		return fmt.Errorf("reading index meta: %w", err)
	}
	src := meta.SourceDir
	if src == "" {
		return fmt.Errorf("index %q records no source directory; build the graph with: gleann index build %s --docs <dir> --graph", name, name)
	}
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return fmt.Errorf("the source directory %s of index %q is not available; build the graph with: gleann index build %s --docs <dir> --graph", src, name, name)
	}
	p := opts.printer()
	unlock, err := lockIndex(ctx, cfg.IndexDir, name)
	if err != nil {
		return err
	}
	defer unlock()
	p.Printf("🕸️  Building the code graph of %q from %s...\n", name, src)
	_, pluginDocs, err := readDocuments(ctx, src, readOptionsFor(cfg, opts, nil, nil), p)
	if err != nil {
		return fmt.Errorf("reading documents: %w", err)
	}
	return buildGraphIndex(GraphOptions{
		Name:              name,
		DocsDir:           src,
		IndexDir:          cfg.IndexDir,
		PluginDocs:        pluginDocs,
		IncludeSubmodules: opts.IncludeSubmodules,
		Stdout:            opts.Stdout,
		Stderr:            opts.Stderr,
	}, p)
}
